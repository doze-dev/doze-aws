package apigateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The invoke URL a deployed stage reports has to be an address that actually
// reaches it. It used to be the literal "http://127.0.0.1:4566", so anyone who
// had moved the endpoint — --listen, a container, a .doze name, a proxy in
// front — was handed a URL that went nowhere, and the console disagreed with
// the SDK because the console re-minted it from the live Host.
//
// The assertion is on the HOST rather than the whole string: the point is that
// it follows the request, not that it has a particular shape.
func TestInvokeURLFollowsTheRequestHost(t *testing.T) {
	for _, tc := range []struct {
		what string
		host string
		want string
	}{
		{"the default loopback", "127.0.0.1:4566", "http://127.0.0.1:4566"},
		{"a different port", "127.0.0.1:9999", "http://127.0.0.1:9999"},
		{"a wildcard bind reached by LAN address", "192.168.1.20:4566", "http://192.168.1.20:4566"},
		{"a .doze name, port-less", "aws.harbour.doze", "http://aws.harbour.doze"},
		{"a compose service name", "doze-aws:4566", "http://doze-aws:4566"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := &Server{}
			r := httptest.NewRequest("GET", "/restapis/abc/stages/prod", nil)
			r.Host = tc.host
			if got := s.invokeBase(r); got != tc.want {
				t.Errorf("invokeBase = %q, want %q", got, tc.want)
			}
			url := InvokeURL(s.invokeBase(r), "abc", "prod")
			if !strings.HasPrefix(url, tc.want+ExecutePrefix) {
				t.Errorf("InvokeURL = %q, want it under %q", url, tc.want)
			}
		})
	}
}

// A proxy terminating TLS tells us so, and a URL that says http:// through an
// https front door is one that redirects or fails.
func TestInvokeURLHonoursForwardedProto(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/restapis", nil)
	r.Host = "aws.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got, want := s.invokeBase(r), "https://aws.example.com"; got != want {
		t.Errorf("invokeBase = %q, want %q", got, want)
	}
}

// A CloudFormation apply reaches this service in-process, with no request to
// read a Host from. The configured endpoint is what the stack was told it is
// reachable at, so that is the fallback — not a literal loopback address.
func TestInvokeURLFallsBackToTheConfiguredEndpoint(t *testing.T) {
	s := &Server{endpoint: "http://aws.harbour.doze/"}
	if got, want := s.invokeBase(nil), "http://aws.harbour.doze"; got != want {
		t.Errorf("invokeBase(nil) = %q, want %q (trailing slash trimmed)", got, want)
	}

	// And with nothing configured at all, the historical default — which is
	// right for an embedder that never told us anything.
	bare := &Server{}
	if got, want := bare.invokeBase(nil), "http://127.0.0.1:4566"; got != want {
		t.Errorf("invokeBase(nil) with no endpoint = %q, want %q", got, want)
	}
}
