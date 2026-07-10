// Package peers lets one doze-aws service reach its siblings — SNS delivering
// to SQS queues, EventBridge dispatching to targets, Lambda destinations —
// without caring how the deployment is wired. A Directory answers "how do I
// speak HTTP to service X?" identically across the three real topologies:
//
//   - one process serving everything (the doze-aws binary, or an embedded
//     Stack): InProcess dispatches straight into the sibling handler, no
//     sockets involved;
//   - one child process per service on unix sockets (how doze runs the
//     services): UnixSockets / FromEnv;
//   - services on plain TCP endpoints: FromEnv via AWS_ENDPOINT_URL_*.
//
// Cross-service delivery is always best-effort and late-bound: a service with
// no directory entry for a peer logs and drops, it never fails to start.
package peers

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Endpoint is how to reach one service: an HTTP client and the base URL to
// address it with. The service behind it speaks its normal AWS wire protocol.
type Endpoint struct {
	Client  *http.Client
	BaseURL string
}

// Directory resolves sibling services. ok is false when the deployment has no
// wiring for that service (callers degrade gracefully).
type Directory interface {
	Endpoint(service string) (Endpoint, bool)
}

// None is an empty directory: every lookup misses. It is what a service sees
// when cross-service delivery is deliberately disabled.
func None() Directory { return noneDir{} }

type noneDir struct{}

func (noneDir) Endpoint(string) (Endpoint, bool) { return Endpoint{}, false }

// InProcess wires peers within a single process: requests round-trip straight
// into the sibling's http.Handler with no network. resolve returns nil for
// services that aren't running.
func InProcess(resolve func(service string) http.Handler) Directory {
	return inProcessDir{resolve: resolve}
}

type inProcessDir struct {
	resolve func(string) http.Handler
}

func (d inProcessDir) Endpoint(service string) (Endpoint, bool) {
	h := d.resolve(service)
	if h == nil {
		return Endpoint{}, false
	}
	return Endpoint{
		Client:  &http.Client{Transport: handlerTransport{h}, Timeout: 30 * time.Second},
		BaseURL: "http://" + service + ".doze-aws.internal",
	}, true
}

// handlerTransport serves each request by invoking the handler directly.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := newRecorder()
	t.h.ServeHTTP(rec, req)
	return rec.result(req), nil
}

// UnixSockets wires peers as HTTP-over-unix-socket endpoints, one socket per
// service — the shape doze's per-instance child processes use.
func UnixSockets(sockets map[string]string) Directory {
	return socketDir{sockets: sockets}
}

type socketDir struct {
	sockets map[string]string
}

func (d socketDir) Endpoint(service string) (Endpoint, bool) {
	sock, ok := d.sockets[service]
	if !ok || sock == "" {
		return Endpoint{}, false
	}
	return unixEndpoint(service, sock), true
}

func unixEndpoint(service, socket string) Endpoint {
	return Endpoint{
		Client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
			Timeout: 30 * time.Second,
		},
		// The host is decorative — the transport dials the socket regardless —
		// but it keeps URLs readable in logs.
		BaseURL: "http://" + service + ".doze-aws.internal",
	}
}

// FromEnv resolves peers from the environment at call time (late-bound, so a
// child process started before its peers still finds them):
//
//	DOZE_<SERVICE>_SOCKET      unix socket path (doze child-process wiring)
//	AWS_ENDPOINT_URL_<SERVICE> per-service TCP endpoint (SDK-standard variable)
//	AWS_ENDPOINT_URL           shared endpoint, e.g. a doze-aws gateway
func FromEnv() Directory { return envDir{} }

type envDir struct{}

func (envDir) Endpoint(service string) (Endpoint, bool) {
	envSvc := strings.ToUpper(strings.ReplaceAll(service, "-", "_"))
	if sock := os.Getenv("DOZE_" + envSvc + "_SOCKET"); sock != "" {
		return unixEndpoint(service, sock), true
	}
	for _, key := range []string{"AWS_ENDPOINT_URL_" + envSvc, "AWS_ENDPOINT_URL"} {
		if u := os.Getenv(key); u != "" {
			return Endpoint{
				Client:  &http.Client{Timeout: 30 * time.Second},
				BaseURL: strings.TrimSuffix(u, "/"),
			}, true
		}
	}
	return Endpoint{}, false
}

// Static wires an explicit service→Endpoint map (tests, custom embeddings).
type Static map[string]Endpoint

func (s Static) Endpoint(service string) (Endpoint, bool) {
	ep, ok := s[service]
	return ep, ok
}

// URL joins an endpoint's base URL with a request path.
func (e Endpoint) URL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return e.BaseURL + path
}
