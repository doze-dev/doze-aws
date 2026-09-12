package peers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoneNeverResolves(t *testing.T) {
	if _, ok := None().Endpoint("sqs"); ok {
		t.Fatal("None resolved a service")
	}
}

func TestInProcessDispatchesToHandler(t *testing.T) {
	hit := ""
	dir := InProcess(func(service string) http.Handler {
		if service != "sqs" {
			return nil
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = r.URL.Path
			io.WriteString(w, "ok")
		})
	})
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		t.Fatal("sqs not resolved")
	}
	resp, err := ep.Client.Post(ep.URL("/create"), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if hit != "/create" || string(b) != "ok" {
		t.Fatalf("hit=%q body=%q", hit, b)
	}
	if _, ok := dir.Endpoint("s3"); ok {
		t.Fatal("unmapped service resolved")
	}
}

func TestUnixSockets(t *testing.T) {
	dir := UnixSockets(map[string]string{"sqs": "/run/sqs.sock"})
	ep, ok := dir.Endpoint("sqs")
	if !ok || ep.Client == nil {
		t.Fatalf("ep = %+v ok=%v", ep, ok)
	}
	// This used to assert the BaseURL contained "sqs", which held only because
	// the host embedded the service name — a cosmetic property of a string
	// nothing dials. The transport routes to the socket regardless.
	//
	// What matters now is the opposite: the host must be one nobody could
	// mistake for an address. peer.invalid is reserved by RFC 2606 and is
	// guaranteed never to resolve, so a URL that leaks it is obviously wrong
	// rather than plausibly right.
	if ep.BaseURL != "http://peer.invalid" {
		t.Errorf("BaseURL = %q, want the unresolvable placeholder", ep.BaseURL)
	}
	if _, ok := dir.Endpoint("s3"); ok {
		t.Fatal("unmapped resolved")
	}
}

// The peer marker rides the same X-Doze-* channel as the principal, and is set
// on every peer call — including the ones with no principal on the context,
// which is most of them. Stamping it conditionally would mark almost nothing.
func TestEveryPeerCallIsMarked(t *testing.T) {
	for _, tc := range []struct {
		what string
		ctx  context.Context
	}{
		{"no principal", context.Background()},
		{"with a principal", WithPrincipal(context.Background(), "s3", "arn:aws:s3:::b")},
	} {
		t.Run(tc.what, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://peer.invalid/", nil).WithContext(tc.ctx)
			stampPrincipal(req)
			if !IsPeer(req) {
				t.Error("a peer call was not marked")
			}
		})
	}

	// And a client's own claim never survives: the strip runs first, so a
	// forged marker on an inbound request cannot be inherited by a peer call
	// made while handling it.
	forged := httptest.NewRequest("POST", "http://peer.invalid/", nil)
	forged.Header.Set("X-Doze-Principal", "root")
	forged.Header.Set("X-Doze-Source-Arn", "arn:aws:iam::999999999999:root")
	stampPrincipal(forged)
	if got := forged.Header.Get("X-Doze-Source-Arn"); got != "" {
		t.Errorf("a client-supplied source ARN survived: %q", got)
	}
	if got := forged.Header.Get("X-Doze-Principal"); got != "" {
		t.Errorf("a client-supplied principal survived: %q", got)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("DOZE_SQS_SOCKET", "/run/x.sock")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://s3.local:9000")
	dir := FromEnv()
	if ep, ok := dir.Endpoint("sqs"); !ok || ep.Client == nil {
		t.Fatalf("sqs socket not resolved: %+v", ep)
	}
	if ep, ok := dir.Endpoint("s3"); !ok || ep.BaseURL != "http://s3.local:9000" {
		t.Fatalf("s3 url = %+v", ep)
	}
	if _, ok := dir.Endpoint("kms"); ok {
		t.Fatal("unset service resolved")
	}
}

func TestStaticAndURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	dir := Static{"sqs": {Client: srv.Client(), BaseURL: srv.URL}}
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		t.Fatal("static not resolved")
	}
	if got := ep.URL("/foo"); got != srv.URL+"/foo" {
		t.Fatalf("URL = %q", got)
	}
	if _, ok := dir.Endpoint("nope"); ok {
		t.Fatal("missing static resolved")
	}
}
