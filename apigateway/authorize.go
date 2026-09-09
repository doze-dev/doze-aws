package apigateway

// Lambda authorizers: the data plane. The gate runs before the integration:
// the identity is read from the request per the authorizer's identity
// source, the function is invoked with the TOKEN or REQUEST event, its
// policy is evaluated against the method ARN, and the outcome is cached for
// the authorizer's TTL keyed by the identity values, as on AWS.
//
// Errors follow AWS: a missing identity source is 401 {"message":
// "Unauthorized"}; an explicit or implicit deny is 403 with AWS's wording;
// an invoke failure or a malformed answer is a 500 whose message names the
// cause (AWS answers a null message there; the cause is more useful).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// callCtx is what an authorizer contributes to the integration event.
type callCtx struct {
	Principal string
	Context   map[string]any // the authorizer's context, strings/numbers/bools
	// UsageKey is the usageIdentifierKey the authorizer returned, which the
	// API key check reads when the API's key source is AUTHORIZER.
	UsageKey string
	// APIKey and APIKeyID are the key the request presented, for the
	// integration event's identity block.
	APIKey   string
	APIKeyID string
}

// authDenial is a refusal the gate produced.
type authDenial struct {
	status  int
	message string
}

// authorizeRequest runs the method's authorizer against the request.
func (s *Server) authorizeRequest(ctx context.Context, api *RestAPI, stage string, res *Resource, m *Method,
	a *Authorizer, params map[string]string, path string, r *http.Request, rl *requestLog) (*callCtx, *authDenial) {

	arn := methodARN(api.ID, stage, r.Method, path)
	values, ok := identityValues(a, r, params, api.Stages[stage])
	if !ok {
		return nil, &authDenial{401, "Unauthorized"}
	}
	if a.Type == "TOKEN" && a.IdentityValidation != "" {
		re, err := regexp.Compile(a.IdentityValidation)
		if err != nil || !re.MatchString(values[0]) {
			return nil, &authDenial{401, "Unauthorized"}
		}
	}
	key := a.ID + "\x00" + strings.Join(values, "\x00")
	if a.ResultTTL > 0 {
		if cached, ok := s.authCache.get(key, s.now()); ok {
			rl.authCached = true
			return s.decide(cached, arn, rl)
		}
	}

	fn := lambdaFromURI(a.URI)
	var event any
	if a.Type == "TOKEN" {
		event = map[string]any{"type": "TOKEN", "authorizationToken": values[0], "methodArn": arn}
	} else {
		ev := s.buildProxyEvent(r, api, stage, res, params, path, nil, nil, rl.id)
		event = map[string]any{
			"type": "REQUEST", "methodArn": arn, "resource": ev.Resource, "path": ev.Path,
			"httpMethod": ev.HTTPMethod, "headers": ev.Headers, "multiValueHeaders": ev.MultiValueHeaders,
			"queryStringParameters": ev.QueryStringParameters, "multiValueQueryStringParameters": ev.MultiValueQueryStringParameters,
			"pathParameters": ev.PathParameters, "stageVariables": ev.StageVariables, "requestContext": ev.RequestContext,
		}
	}
	payload, _ := json.Marshal(event)
	rl.authorizer, rl.authStart = a.Name, s.now()
	out, err := peercall.LambdaInvoke(peers.WithPrincipal(ctx, "apigateway", APIARN(api.ID)), s.peers, fn, payload)
	rl.authEnd = s.now()
	if err != nil {
		return nil, &authDenial{500, "Authorizer error: invoking " + fn + ": " + err.Error()}
	}
	resp, perr := parseAuthorizerResponse(out)
	if perr != nil {
		return nil, &authDenial{500, "Authorizer error: " + perr.Error()}
	}
	if a.ResultTTL > 0 {
		s.authCache.put(key, resp, s.now().Add(time.Duration(a.ResultTTL)*time.Second))
	}
	return s.decide(resp, arn, rl)
}

// decide turns a policy into a verdict for one method ARN.
func (s *Server) decide(resp *authorizerResponse, arn string, rl *requestLog) (*callCtx, *authDenial) {
	rl.principal = resp.PrincipalID
	if !policyAllows(resp.Policy, arn) {
		return nil, &authDenial{403, "User is not authorized to access this resource with an explicit deny"}
	}
	return &callCtx{Principal: resp.PrincipalID, Context: resp.Context, UsageKey: resp.UsageIdentifierKey}, nil
}

// methodARN is the execute-api ARN a policy is evaluated against.
func methodARN(apiID, stage, method, path string) string {
	return awsident.ARN("execute-api", apiID+"/"+stage+"/"+strings.ToUpper(method)+"/"+strings.TrimPrefix(path, "/"))
}

// identityValues reads the authorizer's identity sources from the request.
// Every named source must be present, or the request is unauthorized before
// the function is called.
func identityValues(a *Authorizer, r *http.Request, params map[string]string, st *Stage) ([]string, bool) {
	src := a.IdentitySource
	if src == "" {
		src = "method.request.header.Authorization"
	}
	var out []string
	for _, one := range strings.Split(src, ",") {
		one = strings.TrimSpace(one)
		kind, name, _ := strings.Cut(strings.TrimPrefix(one, "method.request."), ".")
		var v string
		switch kind {
		case "header":
			v = r.Header.Get(name)
		case "querystring":
			v = r.URL.Query().Get(name)
		case "path":
			v = params[name]
		case "stageVariables":
			if st != nil {
				v = st.Variables[name]
			}
		case "context":
			// $context values an authorizer can key on locally.
			switch name {
			case "identity.sourceIp":
				v = sourceIP(r)
			case "identity.userAgent":
				v = r.UserAgent()
			}
		}
		if v == "" {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// authorizerResponse is the shape a Lambda authorizer returns.
type authorizerResponse struct {
	PrincipalID        string         `json:"principalId"`
	Policy             *policyDoc     `json:"policyDocument"`
	Context            map[string]any `json:"context"`
	UsageIdentifierKey string         `json:"usageIdentifierKey"`
}

type policyDoc struct {
	Version   string      `json:"Version"`
	Statement []statement `json:"Statement"`
}

type statement struct {
	Effect   string `json:"Effect"`
	Action   any    `json:"Action"`
	Resource any    `json:"Resource"`
}

func parseAuthorizerResponse(out []byte) (*authorizerResponse, error) {
	var resp authorizerResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("the authorizer did not return a JSON object: %s", truncate(string(out), 200))
	}
	if resp.PrincipalID == "" {
		return nil, fmt.Errorf("the authorizer's response has no principalId: %s", truncate(string(out), 200))
	}
	if resp.Policy == nil {
		return nil, fmt.Errorf("the authorizer's response has no policyDocument: %s", truncate(string(out), 200))
	}
	return &resp, nil
}

// policyAllows evaluates the returned policy for one method ARN: an explicit
// Deny that matches wins; otherwise an Allow must match; otherwise the
// request is denied.
func policyAllows(p *policyDoc, arn string) bool {
	allowed := false
	for _, st := range p.Statement {
		if !actionMatches(st.Action) || !anyGlob(st.Resource, arn) {
			continue
		}
		switch strings.ToLower(st.Effect) {
		case "deny":
			return false
		case "allow":
			allowed = true
		}
	}
	return allowed
}

func actionMatches(action any) bool {
	for _, a := range stringsOf(action) {
		if a == "*" || a == "execute-api:*" || strings.EqualFold(a, "execute-api:Invoke") {
			return true
		}
	}
	return false
}

func anyGlob(resource any, arn string) bool {
	for _, pat := range stringsOf(resource) {
		if globMatch(pat, arn) {
			return true
		}
	}
	return false
}

func stringsOf(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// globMatch is IAM's resource glob: `*` matches any run, `?` one character.
func globMatch(pattern, s string) bool {
	if pattern == "" {
		return s == ""
	}
	switch pattern[0] {
	case '*':
		for i := 0; i <= len(s); i++ {
			if globMatch(pattern[1:], s[i:]) {
				return true
			}
		}
		return false
	case '?':
		return s != "" && globMatch(pattern[1:], s[1:])
	}
	return s != "" && s[0] == pattern[0] && globMatch(pattern[1:], s[1:])
}

// authCache holds authorizer answers keyed by authorizer id and identity.
type authCache struct {
	mu      sync.Mutex
	entries map[string]cachedAuth
}

type cachedAuth struct {
	resp    *authorizerResponse
	expires time.Time
}

func newAuthCache() *authCache { return &authCache{entries: map[string]cachedAuth{}} }

func (c *authCache) get(key string, now time.Time) (*authorizerResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if now.After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.resp, true
}

func (c *authCache) put(key string, resp *authorizerResponse, expires time.Time) {
	c.mu.Lock()
	c.entries[key] = cachedAuth{resp: resp, expires: expires}
	c.mu.Unlock()
}

// forget drops every entry of one authorizer after it changed.
func (c *authCache) forget(authorizerID string) {
	c.mu.Lock()
	for k := range c.entries {
		if strings.HasPrefix(k, authorizerID+"\x00") {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}
