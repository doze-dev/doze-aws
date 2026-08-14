package console

// STS, for one purpose: proving a connection works.
//
// GetCallerIdentity is the call every AWS user already reaches for to answer
// "am I talking to the right thing as the right principal", so Connect uses
// the same one rather than inventing a health check with its own semantics.

import (
	"context"
	"encoding/xml"
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
