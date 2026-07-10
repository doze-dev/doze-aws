// Package sigparse extracts identity and routing information from AWS request
// signatures without ever verifying them. doze-aws is a local emulator with a
// fixed identity, so a signature's only value is what it *names*: the access
// key, and — for SigV4 — the region and service the request was signed for
// (which the gateway uses to route a shared endpoint).
//
// Both signature generations are understood, in both placements:
//
//	SigV4 header:    Authorization: AWS4-HMAC-SHA256 Credential=AKID/20130524/us-east-1/s3/aws4_request, SignedHeaders=..., Signature=...
//	SigV4 presigned: ?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKID%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=...&X-Amz-Expires=...
//	SigV2 header:    Authorization: AWS AKID:Base64Signature
//	SigV2 presigned: ?AWSAccessKeyId=AKID&Signature=...&Expires=1141889120
//
// The one check that IS enforced is presigned-URL expiry (both forms): stale
// presigned URLs are a real, cheap-to-catch application bug.
package sigparse

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Scope identifies who a request claims to be from and, for SigV4, what it was
// signed for.
type Scope struct {
	AccessKeyID string
	Date        string // SigV4 credential-scope date (YYYYMMDD); empty for SigV2
	Region      string // SigV4 only
	Service     string // SigV4 signing name, e.g. "s3", "events"; empty for SigV2
	Version     int    // 2 or 4
	Presigned   bool
}

// Parse extracts the signature scope from r, trying the SigV4 header, the
// SigV2 header, then the two presigned query forms. ok is false when the
// request carries no recognizable AWS signature (anonymous requests are fine —
// callers treat them like any other).
func Parse(r *http.Request) (Scope, bool) {
	if s, ok := ParseAuthorization(r.Header.Get("Authorization")); ok {
		return s, true
	}
	return ParsePresigned(r.URL.Query())
}

// ParseAuthorization parses an Authorization header in either generation.
func ParseAuthorization(header string) (Scope, bool) {
	header = strings.TrimSpace(header)
	switch {
	case strings.HasPrefix(header, "AWS4-HMAC-SHA256 "), strings.HasPrefix(header, "AWS4-HMAC-SHA256,"):
		return parseV4Header(header)
	case strings.HasPrefix(header, "AWS "):
		return parseV2Header(header)
	}
	return Scope{}, false
}

// parseV4Header pulls the credential scope out of a SigV4 Authorization value.
func parseV4Header(header string) (Scope, bool) {
	rest := strings.TrimPrefix(header, "AWS4-HMAC-SHA256")
	for part := range strings.SplitSeq(rest, ",") {
		part = strings.TrimSpace(part)
		if cred, ok := strings.CutPrefix(part, "Credential="); ok {
			s, ok := parseCredential(cred)
			return s, ok
		}
	}
	return Scope{}, false
}

// parseV2Header parses the legacy "AWS AccessKeyId:Signature" form.
func parseV2Header(header string) (Scope, bool) {
	rest := strings.TrimPrefix(header, "AWS ")
	akid, sig, ok := strings.Cut(rest, ":")
	akid = strings.TrimSpace(akid)
	if !ok || akid == "" || strings.TrimSpace(sig) == "" {
		return Scope{}, false
	}
	return Scope{AccessKeyID: akid, Version: 2}, true
}

// parseCredential splits a SigV4 credential string:
// AKID/YYYYMMDD/region/service/aws4_request.
func parseCredential(cred string) (Scope, bool) {
	parts := strings.Split(cred, "/")
	if len(parts) != 5 || parts[0] == "" || parts[4] != "aws4_request" {
		return Scope{}, false
	}
	return Scope{
		AccessKeyID: parts[0],
		Date:        parts[1],
		Region:      parts[2],
		Service:     parts[3],
		Version:     4,
	}, true
}

// ParsePresigned recognizes both presigned query forms.
func ParsePresigned(q url.Values) (Scope, bool) {
	if q.Get("X-Amz-Algorithm") == "AWS4-HMAC-SHA256" {
		if s, ok := parseCredential(q.Get("X-Amz-Credential")); ok {
			s.Presigned = true
			return s, true
		}
		return Scope{}, false
	}
	if akid := q.Get("AWSAccessKeyId"); akid != "" && q.Get("Signature") != "" {
		return Scope{AccessKeyID: akid, Version: 2, Presigned: true}, true
	}
	return Scope{}, false
}

// PresignedExpiry reports whether q carries a presigned-URL expiry and, if so,
// whether it has passed at time now. Requests without presigned parameters
// return (false, false).
func PresignedExpiry(q url.Values, now time.Time) (present, expired bool) {
	// SigV4: X-Amz-Date (ISO8601 basic) + X-Amz-Expires (seconds, 1..604800).
	if amzDate := q.Get("X-Amz-Date"); amzDate != "" && q.Get("X-Amz-Expires") != "" {
		t, err := time.Parse("20060102T150405Z", amzDate)
		if err != nil {
			return true, true // unparseable date on a presigned URL: treat as expired, not as anonymous
		}
		secs, err := strconv.ParseInt(q.Get("X-Amz-Expires"), 10, 64)
		if err != nil || secs < 0 {
			return true, true
		}
		return true, now.After(t.Add(time.Duration(secs) * time.Second))
	}
	// SigV2: Expires is an absolute unix timestamp.
	if exp := q.Get("Expires"); exp != "" && q.Get("AWSAccessKeyId") != "" && q.Get("Signature") != "" {
		secs, err := strconv.ParseInt(exp, 10, 64)
		if err != nil {
			return true, true
		}
		return true, now.After(time.Unix(secs, 0))
	}
	return false, false
}
