package sts

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// params wraps the merged Query parameters with typed accessors.
type params struct {
	vals url.Values
}

func (p params) str(key string) string { return p.vals.Get(key) }

// durationSeconds validates the DurationSeconds parameter against the action's
// allowed range, defaulting when absent.
func (p params) durationSeconds(def, min, max int) (int, *awshttp.APIError) {
	raw := p.vals.Get("DurationSeconds")
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, awshttp.Errf(400, "ValidationError", "DurationSeconds must be an integer, got %q", raw)
	}
	if n < min || n > max {
		return 0, awshttp.Errf(400, "ValidationError",
			"DurationSeconds must be between %d and %d seconds, got %d", min, max, n)
	}
	return n, nil
}

// credentials is the shared <Credentials> element.
type credentials struct {
	AccessKeyId     string
	SecretAccessKey string
	SessionToken    string
	Expiration      string
}

type assumedRoleUser struct {
	AssumedRoleId string
	Arn           string
}

type federatedUser struct {
	FederatedUserId string
	Arn             string
}

// mintCredentials fabricates a temporary credential set. The values are random
// so accidentally hard-coded ones stand out, prefixed the way real STS
// prefixes them (ASIA = temporary access key).
func (s *Server) mintCredentials(lifetime time.Duration) credentials {
	return credentials{
		AccessKeyId:     "ASIA" + randToken(16),
		SecretAccessKey: randToken(40),
		SessionToken:    "doze/" + randToken(64),
		Expiration:      awshttp.ISO8601(s.now().Add(lifetime)),
	}
}

// randToken returns n characters of uppercase-alphanumeric randomness.
func randToken(n int) string {
	raw := make([]byte, (n*5+7)/8+1)
	rand.Read(raw)
	return base32.StdEncoding.EncodeToString(raw)[:n]
}

// parseRoleArn validates a role ARN and extracts the role name.
func parseRoleArn(arn string) (roleName string, err *awshttp.APIError) {
	// arn:aws:iam::123456789012:role/path/RoleName
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[2] != "iam" || !strings.HasPrefix(parts[5], "role/") {
		return "", awshttp.Errf(400, "ValidationError",
			"RoleArn %q is not a valid role ARN (want arn:aws:iam::<account>:role/<name>)", arn)
	}
	segs := strings.Split(parts[5], "/")
	return segs[len(segs)-1], nil
}

func validSessionName(name string) *awshttp.APIError {
	if name == "" {
		return awshttp.Errf(400, "ValidationError", "RoleSessionName is required")
	}
	if len(name) < 2 || len(name) > 64 {
		return awshttp.Errf(400, "ValidationError", "RoleSessionName must be 2-64 characters, got %d", len(name))
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '=', r == ',', r == '.', r == '@', r == '-', r == '_', r == '+':
		default:
			return awshttp.Errf(400, "ValidationError", "RoleSessionName contains invalid character %q", r)
		}
	}
	return nil
}

// --- GetCallerIdentity ---

type getCallerIdentityResult struct {
	Arn     string
	UserId  string
	Account string
}

func (s *Server) getCallerIdentity(params) (any, *awshttp.APIError) {
	return getCallerIdentityResult{
		Arn:     awsident.GlobalARN("iam", "user/"+awsident.AccessKeyID),
		UserId:  "AIDADOZE" + strings.ToUpper(awsident.AccessKeyID),
		Account: awsident.AccountID,
	}, nil
}

// --- AssumeRole ---

type assumeRoleResult struct {
	Credentials      credentials
	AssumedRoleUser  assumedRoleUser
	PackedPolicySize int
}

func (s *Server) assumeRole(p params) (any, *awshttp.APIError) {
	roleName, err := parseRoleArn(p.str("RoleArn"))
	if err != nil {
		return nil, err
	}
	session := p.str("RoleSessionName")
	if err := validSessionName(session); err != nil {
		return nil, err
	}
	secs, err := p.durationSeconds(3600, 900, 43200)
	if err != nil {
		return nil, err
	}
	return assumeRoleResult{
		Credentials:     s.mintCredentials(time.Duration(secs) * time.Second),
		AssumedRoleUser: assumedRole(roleName, session),
	}, nil
}

func assumedRole(roleName, session string) assumedRoleUser {
	return assumedRoleUser{
		AssumedRoleId: "AROA" + randToken(17) + ":" + session,
		Arn:           awsident.GlobalARN("sts", fmt.Sprintf("assumed-role/%s/%s", roleName, session)),
	}
}

// --- AssumeRoleWithWebIdentity ---

type assumeRoleWithWebIdentityResult struct {
	Credentials                 credentials
	AssumedRoleUser             assumedRoleUser
	SubjectFromWebIdentityToken string
	Audience                    string
	Provider                    string
	PackedPolicySize            int
}

func (s *Server) assumeRoleWithWebIdentity(p params) (any, *awshttp.APIError) {
	roleName, err := parseRoleArn(p.str("RoleArn"))
	if err != nil {
		return nil, err
	}
	session := p.str("RoleSessionName")
	if err := validSessionName(session); err != nil {
		return nil, err
	}
	token := p.str("WebIdentityToken")
	if token == "" {
		return nil, awshttp.Errf(400, "ValidationError", "WebIdentityToken is required")
	}
	secs, err := p.durationSeconds(3600, 900, 43200)
	if err != nil {
		return nil, err
	}
	// Echo the token's claims where possible. The token is not verified — it
	// only needs to be structurally a JWT for its sub/aud/iss to be reflected,
	// which is what application code reads back.
	sub, aud, iss := jwtClaims(token)
	if sub == "" {
		sub = "doze-web-identity"
	}
	provider := p.str("ProviderId")
	if provider == "" {
		provider = strings.TrimPrefix(iss, "https://")
	}
	return assumeRoleWithWebIdentityResult{
		Credentials:                 s.mintCredentials(time.Duration(secs) * time.Second),
		AssumedRoleUser:             assumedRole(roleName, session),
		SubjectFromWebIdentityToken: sub,
		Audience:                    aud,
		Provider:                    provider,
	}, nil
}

// jwtClaims best-effort decodes a JWT payload for sub, aud, iss. Any parse
// failure returns empties — the token is never rejected for its shape.
func jwtClaims(token string) (sub, aud, iss string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", ""
	}
	var claims struct {
		Sub string          `json:"sub"`
		Aud json.RawMessage `json:"aud"` // string or array of strings
		Iss string          `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", ""
	}
	var audStr string
	var one string
	var many []string
	if json.Unmarshal(claims.Aud, &one) == nil {
		audStr = one
	} else if json.Unmarshal(claims.Aud, &many) == nil && len(many) > 0 {
		audStr = many[0]
	}
	return claims.Sub, audStr, claims.Iss
}

// --- AssumeRoleWithSAML ---

type assumeRoleWithSAMLResult struct {
	Credentials      credentials
	AssumedRoleUser  assumedRoleUser
	Subject          string
	SubjectType      string
	Issuer           string
	Audience         string
	NameQualifier    string
	PackedPolicySize int
}

func (s *Server) assumeRoleWithSAML(p params) (any, *awshttp.APIError) {
	roleName, err := parseRoleArn(p.str("RoleArn"))
	if err != nil {
		return nil, err
	}
	if p.str("PrincipalArn") == "" {
		return nil, awshttp.Errf(400, "ValidationError", "PrincipalArn is required")
	}
	assertion := p.str("SAMLAssertion")
	if assertion == "" {
		return nil, awshttp.Errf(400, "ValidationError", "SAMLAssertion is required")
	}
	secs, err := p.durationSeconds(3600, 900, 43200)
	if err != nil {
		return nil, err
	}
	subject, issuer := samlClaims(assertion)
	if subject == "" {
		subject = "doze-saml-subject"
	}
	// The session name for SAML is derived from the subject.
	session := sanitizeSession(subject)
	return assumeRoleWithSAMLResult{
		Credentials:     s.mintCredentials(time.Duration(secs) * time.Second),
		AssumedRoleUser: assumedRole(roleName, session),
		Subject:         subject,
		SubjectType:     "persistent",
		Issuer:          issuer,
		Audience:        "https://signin.aws.amazon.com/saml",
		NameQualifier:   randToken(24),
	}, nil
}

// samlClaims best-effort extracts NameID and Issuer from a base64 SAML
// assertion. Any parse failure returns empties.
func samlClaims(assertion string) (subject, issuer string) {
	raw, err := base64.StdEncoding.DecodeString(assertion)
	if err != nil {
		return "", ""
	}
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var inNameID, inIssuer bool
	for {
		tok, err := dec.Token()
		if err != nil {
			return subject, issuer
		}
		switch t := tok.(type) {
		case xml.StartElement:
			inNameID = t.Name.Local == "NameID"
			inIssuer = t.Name.Local == "Issuer" && issuer == ""
		case xml.CharData:
			if inNameID && subject == "" {
				subject = strings.TrimSpace(string(t))
			}
			if inIssuer {
				issuer = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			inNameID, inIssuer = false, false
		}
	}
}

// sanitizeSession maps an arbitrary subject onto the session-name alphabet.
func sanitizeSession(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '=', r == ',', r == '.', r == '@', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) < 2 {
		out = "doze-" + out
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// --- AssumeRoot ---

type assumeRootResult struct {
	Credentials    credentials
	SourceIdentity string
}

func (s *Server) assumeRoot(p params) (any, *awshttp.APIError) {
	if p.str("TargetPrincipal") == "" {
		return nil, awshttp.Errf(400, "ValidationError", "TargetPrincipal is required")
	}
	if p.str("TaskPolicyArn.arn") == "" && p.str("TaskPolicyArn") == "" {
		return nil, awshttp.Errf(400, "ValidationError", "TaskPolicyArn is required")
	}
	secs, err := p.durationSeconds(900, 0, 900)
	if err != nil {
		return nil, err
	}
	return assumeRootResult{
		Credentials:    s.mintCredentials(time.Duration(secs) * time.Second),
		SourceIdentity: awsident.AccessKeyID,
	}, nil
}

// --- GetSessionToken ---

type getSessionTokenResult struct {
	Credentials credentials
}

func (s *Server) getSessionToken(p params) (any, *awshttp.APIError) {
	secs, err := p.durationSeconds(43200, 900, 129600)
	if err != nil {
		return nil, err
	}
	return getSessionTokenResult{Credentials: s.mintCredentials(time.Duration(secs) * time.Second)}, nil
}

// --- GetFederationToken ---

type getFederationTokenResult struct {
	Credentials      credentials
	FederatedUser    federatedUser
	PackedPolicySize int
}

func (s *Server) getFederationToken(p params) (any, *awshttp.APIError) {
	name := p.str("Name")
	if name == "" {
		return nil, awshttp.Errf(400, "ValidationError", "Name is required")
	}
	secs, err := p.durationSeconds(43200, 900, 129600)
	if err != nil {
		return nil, err
	}
	return getFederationTokenResult{
		Credentials: s.mintCredentials(time.Duration(secs) * time.Second),
		FederatedUser: federatedUser{
			FederatedUserId: awsident.AccountID + ":" + name,
			Arn:             awsident.GlobalARN("sts", "federated-user/"+name),
		},
	}, nil
}

// --- GetAccessKeyInfo ---

type getAccessKeyInfoResult struct {
	Account string
}

func (s *Server) getAccessKeyInfo(p params) (any, *awshttp.APIError) {
	if p.str("AccessKeyId") == "" {
		return nil, awshttp.Errf(400, "ValidationError", "AccessKeyId is required")
	}
	// Locally every key belongs to the one account.
	return getAccessKeyInfoResult{Account: awsident.AccountID}, nil
}

// --- DecodeAuthorizationMessage ---

func (s *Server) decodeAuthorizationMessage(p params) (any, *awshttp.APIError) {
	// doze-aws never authorizes, so it never produces encoded authorization
	// failure messages; there is nothing meaningful to decode locally.
	return nil, awshttp.Errf(400, "InvalidAuthorizationMessageException",
		"doze-aws does not produce encoded authorization messages, so there is nothing to decode locally")
}
