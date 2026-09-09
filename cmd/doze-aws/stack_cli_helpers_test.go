package main

// Helpers for stack_cli_test.go.

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/config"
)

func configFor(dataDir, listen string) config.Config {
	c := config.Default()
	c.DataDir = dataDir
	c.ListenAddr = listen
	return c
}

// newHTTPTest serves a stack and returns its URL.
func newHTTPTest(t *testing.T, s *dozeaws.Stack) string {
	t.Helper()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// newListener opens a real TCP listener so gatewayFor's dial probe succeeds.
func newListener(t *testing.T) (stop func(), addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go srv.Serve(ln) //nolint:errcheck
	return func() { srv.Close() }, ln.Addr().String()
}

// newEchoServer answers with the method, target, body and one request header,
// plus a header of its own, so the proxy can be checked in both directions.
func newEchoServer(t *testing.T) (stop func(), base string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Came-Back", "yes")
		w.WriteHeader(207)
		io.WriteString(w, r.Method+" "+r.URL.RequestURI()+" "+string(body)+" "+r.Header.Get("X-Sent")) //nolint:errcheck
	}))
	return ts.Close, ts.URL
}

func doThroughProxy(t *testing.T, p proxyHandler, method, target, body string, headers map[string]string) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}
