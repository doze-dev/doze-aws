package iamguard

// The condition context of a request, built once.
//
// Two places used to build it and disagreed. The IAM service supplied seven
// keys when it evaluated an identity policy, and the guard that every other
// service calls for its resource policy supplied four, so a bucket policy that
// said
//
//	{"Effect": "Deny", "Condition": {"Bool": {"aws:SecureTransport": "false"}}}
//
// never saw the key at all: an absent key makes the condition false, the Deny
// did not apply, and the request went through. SUPPORT.md listed
// aws:SecureTransport, aws:SourceIp, aws:UserAgent, aws:RequestedRegion and
// aws:username as supplied, and they were, for identity policies only.
//
// One function now, used by both, so a key cannot be on one path and not the
// other.

import (
	"net"
	"net/http"
	"strings"
)

// RequestContext is the global condition keys for a request, from the request
// itself and the identity it was made as.
//
// principal is the caller's ARN; account and region are this instance's. A
// username is derived from a user ARN and is absent for a role or the root,
// as aws:username is on AWS.
//
// aws:SecureTransport is "true", and that is a decision rather than a
// measurement (plan decision D7). Every local request is plain HTTP, because
// the endpoint is a loopback address or a .doze name, so measuring the
// transport would make a "deny when SecureTransport is false" policy refuse
// every request — the policy CDK's enforceSSL and Serverless's deployment
// bucket both write. On AWS the same SDK uses the HTTPS endpoint by default
// and the policy never fires for it. The local endpoint stands in for that
// one, so the key says what the request would have said there.
func RequestContext(r *http.Request, principal, account, region string) map[string][]string {
	ctx := map[string][]string{
		"aws:PrincipalArn":     {principal},
		"aws:PrincipalAccount": {account},
		"aws:RequestedRegion":  {region},
		"aws:SecureTransport":  {"true"},
	}
	if name := userNameOf(principal); name != "" {
		ctx["aws:username"] = []string{name}
	}
	if r != nil {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
			ctx["aws:SourceIp"] = []string{host}
		} else if r.RemoteAddr != "" && !strings.Contains(r.RemoteAddr, ":") {
			ctx["aws:SourceIp"] = []string{r.RemoteAddr}
		}
		if ua := r.Header.Get("User-Agent"); ua != "" {
			ctx["aws:UserAgent"] = []string{ua}
		}
	}
	return ctx
}

// userNameOf returns the user name in an IAM user ARN, or "". The name is the
// last path segment: arn:aws:iam::123456789012:user/division/team/alice is alice.
func userNameOf(arn string) string {
	i := strings.Index(arn, ":user/")
	if i < 0 {
		return ""
	}
	name := arn[i+len(":user/"):]
	if j := strings.LastIndex(name, "/"); j >= 0 {
		name = name[j+1:]
	}
	return name
}
