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

// stampPrincipal writes the service principal onto a peer request. A request
// with no principal on its context is stamped as one anyway, from nothing:
// a peer call never carries a client's identity.
func stampPrincipal(req *http.Request) {
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-doze-") {
			req.Header.Del(name)
		}
	}
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
