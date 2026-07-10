package lambda_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer starts an httptest server for h and returns its URL.
func newTestServer(t *testing.T, h http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}
