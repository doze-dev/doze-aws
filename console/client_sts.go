package console

// STS, for one purpose: proving a connection works.
//
// GetCallerIdentity is the call every AWS user already reaches for to answer
// "am I talking to the right thing as the right principal", so Connect uses
// the same one rather than inventing a health check with its own semantics.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
)

// CallerIdentity returns the account and principal ARN this endpoint reports.
func (b *backend) CallerIdentity(ctx context.Context) (account, arn string, err error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"GetCallerIdentity"}})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Account string `xml:"GetCallerIdentityResult>Account"`
		ARN     string `xml:"GetCallerIdentityResult>Arn"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", "", err
	}
	return out.Account, out.ARN, nil
}

// ---- minting credentials: the rest of STS ----

// STSCreds is one minted credential set plus whatever identity the call
// echoed back. The env line is what someone actually pastes into a shell.
type STSCreds struct {
	AccessKeyID, SecretAccessKey, SessionToken, Expiration string
	Identity                                               string
}

// Env renders the three exports a shell needs to become this principal.
func (c STSCreds) Env() string {
	return "export AWS_ACCESS_KEY_ID=" + c.AccessKeyID +
		" AWS_SECRET_ACCESS_KEY=" + c.SecretAccessKey +
		" AWS_SESSION_TOKEN=" + c.SessionToken
}

// credWire is the shared <Credentials> element every mint returns.
type credWire struct {
	AccessKeyId     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

// MintCredentials runs one of STS's credential mints. mode picks the
// operation; v carries its already-named parameters. Each op wraps the same
// Credentials element in its own Result, so the decode covers every wrapper
// and takes whichever answered.
func (b *backend) MintCredentials(ctx context.Context, mode string, v url.Values) (*STSCreds, error) {
	action := map[string]string{
		"assume-role":  "AssumeRole",
		"session":      "GetSessionToken",
		"federation":   "GetFederationToken",
		"root":         "AssumeRoot",
		"web-identity": "AssumeRoleWithWebIdentity",
		"saml":         "AssumeRoleWithSAML",
	}[mode]
	if action == "" {
		return nil, fmt.Errorf("unknown credential mode %q", mode)
	}
	v.Set("Action", action)
	body, err := b.queryXML(ctx, v)
	if err != nil {
		return nil, err
	}
	var out struct {
		AR struct {
			C credWire `xml:"Credentials"`
			U struct {
				Arn string `xml:"Arn"`
			} `xml:"AssumedRoleUser"`
		} `xml:"AssumeRoleResult"`
		ST struct {
			C credWire `xml:"Credentials"`
		} `xml:"GetSessionTokenResult"`
		FT struct {
			C credWire `xml:"Credentials"`
			U struct {
				Arn string `xml:"Arn"`
			} `xml:"FederatedUser"`
		} `xml:"GetFederationTokenResult"`
		RT struct {
			C              credWire `xml:"Credentials"`
			SourceIdentity string   `xml:"SourceIdentity"`
		} `xml:"AssumeRootResult"`
		WI struct {
			C   credWire `xml:"Credentials"`
			Sub string   `xml:"SubjectFromWebIdentityToken"`
			U   struct {
				Arn string `xml:"Arn"`
			} `xml:"AssumedRoleUser"`
		} `xml:"AssumeRoleWithWebIdentityResult"`
		SA struct {
			C       credWire `xml:"Credentials"`
			Subject string   `xml:"Subject"`
			U       struct {
				Arn string `xml:"Arn"`
			} `xml:"AssumedRoleUser"`
		} `xml:"AssumeRoleWithSAMLResult"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	pick := func(c credWire, id string) *STSCreds {
		return &STSCreds{AccessKeyID: c.AccessKeyId, SecretAccessKey: c.SecretAccessKey,
			SessionToken: c.SessionToken, Expiration: shortTime(c.Expiration), Identity: id}
	}
	switch {
	case out.AR.C.AccessKeyId != "":
		return pick(out.AR.C, out.AR.U.Arn), nil
	case out.ST.C.AccessKeyId != "":
		return pick(out.ST.C, ""), nil
	case out.FT.C.AccessKeyId != "":
		return pick(out.FT.C, out.FT.U.Arn), nil
	case out.RT.C.AccessKeyId != "":
		return pick(out.RT.C, "source identity "+out.RT.SourceIdentity), nil
	case out.WI.C.AccessKeyId != "":
		return pick(out.WI.C, out.WI.U.Arn+" (subject "+out.WI.Sub+")"), nil
	case out.SA.C.AccessKeyId != "":
		return pick(out.SA.C, out.SA.U.Arn+" (subject "+out.SA.Subject+")"), nil
	}
	return nil, fmt.Errorf("no credentials in the %s response", action)
}

// AccessKeyAccount answers which account a key id belongs to
// (GetAccessKeyInfo) — locally, always the one account, which is the point:
// the answer is honest, not interesting.
func (b *backend) AccessKeyAccount(ctx context.Context, keyID string) (string, error) {
	body, err := b.queryXML(ctx, url.Values{"Action": {"GetAccessKeyInfo"}, "AccessKeyId": {keyID}})
	if err != nil {
		return "", err
	}
	var out struct {
		Account string `xml:"GetAccessKeyInfoResult>Account"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Account, nil
}
