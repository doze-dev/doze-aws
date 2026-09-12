package peers

// A service calling a sibling on behalf of a resource — S3 delivering a
// notification to Lambda, SNS fanning out to SQS — acts as a service
// principal, and the sibling's resource policy decides whether to admit it.
// WithPrincipal names that principal on the context; the transports stamp it
// onto every peer request so the sibling's guard (internal/iamguard) sees
// who is calling and for which resource.

import (
	"context"
	"net/http"
	"strings"
)

type principalKey struct{}

type principal struct {
	service   string // "s3", "sns", ...
	sourceARN string
}

// WithPrincipal marks ctx as a call made by service on behalf of sourceARN.
func WithPrincipal(ctx context.Context, service, sourceARN string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal{service: service, sourceARN: sourceARN})
}

// HeaderPeer marks a request as one service calling another rather than a
// client calling in.
//
// It exists because SOME answers depend on knowing that. A service that mints
// a user-facing URL builds it from the host the request arrived on — which is
// right for a client and wrong for a peer, whose "host" is a placeholder that
// resolves to nothing. Reported back, that placeholder is a URL that looks
// plausible and reaches nowhere; a Lambda function URL came out as
// http://lambda.doze-aws.internal/_aws/lambda-url/… exactly this way, and was
// caught only by an e2e test asserting the shape the console shows.
//
// The first fix was to recognise the placeholder hostname. This is the second
// and better one: a request states what it is, rather than being identified by
// a string that looks like a name. Peer base URLs are now peer.invalid
// (RFC 2606 — guaranteed never to resolve, and obviously not something to
// copy), which is a placeholder that cannot be mistaken for an address.
//
// A client cannot forge it. Every request arriving over the network passes
// iamguard.Strip, which deletes every X-Doze-* header before anything reads
// one (dozeaws.go, both IAM modes; iam/spoofing_test.go asserts it end to end
// against a running stack), and stampPrincipal below strips the same set on
// the way out so a nested peer call cannot inherit one either.
const HeaderPeer = "X-Doze-Peer"

// IsPeer reports whether a request came from a sibling service in this process
// rather than from a client. A URL minted for a peer must not be built from
// the request's host.
func IsPeer(r *http.Request) bool { return r.Header.Get(HeaderPeer) != "" }

// stampPrincipal writes the service principal onto a peer request. A request
// with no principal on its context is stamped as one anyway, from nothing:
// a peer call never carries a client's identity.
func stampPrincipal(req *http.Request) {
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-doze-") {
			req.Header.Del(name)
		}
	}
	// After the strip, and before the early return below: this marks the CALL,
	// not the caller, and most peer calls carry no principal at all — stamping
	// it conditionally would mark almost nothing.
	req.Header.Set(HeaderPeer, "1")

	p, ok := req.Context().Value(principalKey{}).(principal)
	if !ok || p.service == "" {
		return
	}
	req.Header.Set("X-Doze-Principal", p.service+".amazonaws.com")
	req.Header.Set("X-Doze-Identity", "service")
	if p.sourceARN != "" {
		req.Header.Set("X-Doze-Source-Arn", p.sourceARN)
	}
}

// principalTransport stamps the principal before the next transport sends.
type principalTransport struct{ next http.RoundTripper }

func (t principalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	stampPrincipal(req)
	return t.next.RoundTrip(req)
}
