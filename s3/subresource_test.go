package s3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUnknownSubresourceDoesNotFallThrough is the regression for the worst bug
// the S3 audit found.
//
// S3 routes by method + path + query sub-resource, and every method's switch
// ends in a default arm that is a DIFFERENT operation. Before the guard,
// `GET /bucket?ownershipControls` returned an object listing, and
// `DELETE /object?annotation` deleted the object — destroying data the caller
// never asked to touch.
func TestUnknownSubresourceDoesNotFallThrough(t *testing.T) {
	srv, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	do := func(method, path string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(http.MethodPut, "/audit", ""); rec.Code != 200 {
		t.Fatalf("CreateBucket = %d", rec.Code)
	}
	if rec := do(http.MethodPut, "/audit/important.txt", "hello"); rec.Code != 200 {
		t.Fatalf("PutObject = %d: %s", rec.Code, rec.Body)
	}

	// Every unimplemented sub-resource must be refused, not re-interpreted.
	cases := []struct{ method, path string }{
		{http.MethodGet, "/audit?ownershipControls"},
		{http.MethodGet, "/audit?publicAccessBlock"},
		{http.MethodGet, "/audit?policyStatus"},
		{http.MethodGet, "/audit?analytics"},
		{http.MethodGet, "/audit?inventory"},
		{http.MethodGet, "/audit?metrics"},
		{http.MethodGet, "/audit?intelligent-tiering"},
		{http.MethodPut, "/audit?ownershipControls"},
		{http.MethodDelete, "/audit?publicAccessBlock"},
		{http.MethodGet, "/audit/important.txt?torrent"},
		{http.MethodPost, "/audit/important.txt?restore"},
		{http.MethodPut, "/audit/important.txt?renameObject"},
		{http.MethodDelete, "/audit/important.txt?annotation"},
		{http.MethodGet, "/audit/important.txt?annotation"},
	}
	for _, c := range cases {
		rec := do(c.method, c.path, "")
		if rec.Code != 501 {
			t.Errorf("%s %s = %d, want 501 NotImplemented (body: %s)",
				c.method, c.path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if !strings.Contains(rec.Body.String(), "NotImplemented") {
			t.Errorf("%s %s: body should carry NotImplemented, got %s", c.method, c.path, rec.Body)
		}
	}

	// The object must still be there — the destructive fall-through is the
	// specific thing this guards.
	if rec := do(http.MethodHead, "/audit/important.txt", ""); rec.Code != 200 {
		t.Fatalf("the object was destroyed by an unknown sub-resource: HEAD = %d", rec.Code)
	}
}

// TestImplementedSubresourcesStillRoute guards the other direction: the guard
// must not swallow anything doze-aws does implement.
func TestImplementedSubresourcesStillRoute(t *testing.T) {
	srv, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	do(http.MethodPut, "/b", "")
	do(http.MethodPut, "/b/k", "hi")

	// A representative marker from every implemented family, including the
	// bucket-level `encryption` that the object-level table deliberately lists.
	for _, path := range []string{
		"/b?versioning", "/b?tagging", "/b?cors", "/b?lifecycle", "/b?website",
		"/b?policy", "/b?acl", "/b?encryption", "/b?notification", "/b?location",
		"/b?versions", "/b?uploads", "/b?object-lock", "/b?list-type=2",
		"/b/k?tagging", "/b/k?acl", "/b/k?attributes",
	} {
		rec := do(http.MethodGet, path, "")
		if rec.Code == 501 {
			t.Errorf("GET %s was refused as unimplemented, but doze-aws implements it", path)
		}
	}
}
