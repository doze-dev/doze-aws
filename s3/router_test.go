package s3

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Every operation in the model's table has a handler, and every handler an
// operation: a route with none would answer 405 for something the model says
// exists, and a handler with no route is dead code that reads like a feature.
func TestEveryRouteHasAHandlerAndEveryHandlerARoute(t *testing.T) {
	handlers := (&Server{}).handlers()
	routed := map[string]bool{}
	for _, rt := range routes {
		routed[rt.Op] = true
		if handlers[rt.Op] == nil {
			t.Errorf("%s has no handler", rt.Op)
		}
	}
	for op := range handlers {
		if !routed[op] {
			t.Errorf("handler %s has no route", op)
		}
	}
}

func opOf(method, target string, headers ...string) string {
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	return OperationFor(r)
}

// Operations that share a method and a path are told apart by a marker, a
// header or a query parameter, and "/bucket/" is a bucket, not an object with
// a blank key.
func TestOperationsAreToldApartByTheirMarkers(t *testing.T) {
	for _, c := range []struct{ method, target, want string }{
		{"GET", "/", "ListBuckets"},
		{"GET", "/b", "ListObjects"},
		{"GET", "/b/", "ListObjects"},
		{"GET", "/b?list-type=2", "ListObjectsV2"},
		{"GET", "/b?tagging", "GetBucketTagging"},
		{"PUT", "/b?tagging", "PutBucketTagging"},
		{"DELETE", "/b?tagging", "DeleteBucketTagging"},
		{"GET", "/b?versioning", "GetBucketVersioning"},
		{"GET", "/b?versions", "ListObjectVersions"},
		{"GET", "/b?uploads", "ListMultipartUploads"},
		{"PUT", "/b", "CreateBucket"},
		{"DELETE", "/b", "DeleteBucket"},
		{"POST", "/b?delete", "DeleteObjects"},
		{"GET", "/b/k", "GetObject"},
		{"HEAD", "/b/k", "HeadObject"},
		{"PUT", "/b/k", "PutObject"},
		{"DELETE", "/b/k", "DeleteObject"},
		{"GET", "/b/k?tagging", "GetObjectTagging"},
		{"GET", "/b/k?acl", "GetObjectAcl"},
		{"POST", "/b/k?uploads", "CreateMultipartUpload"},
		{"POST", "/b/k?uploadId=u", "CompleteMultipartUpload"},
		{"DELETE", "/b/k?uploadId=u", "AbortMultipartUpload"},
		{"GET", "/b/k?uploadId=u", "ListParts"},
		{"PUT", "/b/k?partNumber=1&uploadId=u", "UploadPart"},
		{"GET", "/b/a/b/c", "GetObject"},
		{"GET", "/b/dir/", "GetObject"},
	} {
		if got := opOf(c.method, c.target); got != c.want {
			t.Errorf("%s %s = %q, want %s", c.method, c.target, got, c.want)
		}
	}
	// The same method and path, told apart by a header.
	if got := opOf("PUT", "/b/k", "x-amz-copy-source", "/src/key"); got != "CopyObject" {
		t.Errorf("PUT with x-amz-copy-source = %q, want CopyObject", got)
	}
	if got := opOf("PUT", "/b/k?partNumber=1&uploadId=u", "x-amz-copy-source", "/src/key"); got != "UploadPartCopy" {
		t.Errorf("part with x-amz-copy-source = %q, want UploadPartCopy", got)
	}
}

// A sub-resource marker the request carries and the operation does not declare
// belongs to a different operation. ListObjects must not claim ?legal-hold.
func TestAMarkerNoRouteDeclaresMatchesNothing(t *testing.T) {
	if got := opOf("GET", "/b?legal-hold"); got == "ListObjects" {
		t.Errorf("GET /b?legal-hold = ListObjects: a bucket listing is not what that asks for")
	}
}

// Keys are what a client says they are: an escaped slash stays inside the key,
// two slashes in a row are two slashes, a trailing slash is part of the name,
// and none of it is read as structure.
func TestKeysKeepTheirShape(t *testing.T) {
	ts := s3Server(t)
	do := func(method, target, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+target, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := do("PUT", "/shape", ""); code != 200 {
		t.Fatalf("create bucket = %d", code)
	}
	for _, key := range []string{
		"plain.txt", "dir/file.txt", "a%2Fb", "double//slash", "trailing/", "50%2541", "sp%20ace", "unicode-%C3%A9",
	} {
		if code, body := do("PUT", "/shape/"+key, "body-of-"+key); code != 200 {
			t.Errorf("PUT %s = %d %s", key, code, body)
			continue
		}
		if code, got := do("GET", "/shape/"+key, ""); code != 200 || got != "body-of-"+key {
			t.Errorf("GET %s = %d %q, want what was put", key, code, got)
		}
	}
	// An escaped slash and a real one are the same key.
	if code, got := do("GET", "/shape/dir%2Ffile.txt", ""); code != 200 || got != "body-of-dir/file.txt" {
		t.Errorf("GET dir%%2Ffile.txt = %d %q, want the object at dir/file.txt", code, got)
	}
	// A literal %41 in a key is not an A.
	if code, _ := do("GET", "/shape/50%2541", ""); code != 200 {
		t.Errorf("the key 50%%41 is not reachable")
	}
	if code, _ := do("GET", "/shape/50A", ""); code != 404 {
		t.Errorf("GET 50A = %d, want 404: %%41 must not be decoded twice", code)
	}
	// "/bucket/" lists the bucket.
	if code, body := do("GET", "/shape/", ""); code != 200 || !strings.Contains(body, "<ListBucketResult") {
		t.Errorf("GET /shape/ = %d, want a bucket listing", code)
	}
}

// A virtual-hosted request addresses the same bucket, through the Host.
func TestVirtualHostedRequestsRouteLikePathStyle(t *testing.T) {
	srv, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Suffix: "doze.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	serve := func(method, host, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = host
		srv.ServeHTTP(rec, r)
		return rec
	}
	if rec := serve("PUT", "doze.test", "/vhost", ""); rec.Code != 200 {
		t.Fatalf("create bucket = %d", rec.Code)
	}
	vhost := "vhost.s3.us-east-1.doze.test"
	if rec := serve("PUT", vhost, "/dir/key.txt", "hello"); rec.Code != 200 {
		t.Fatalf("vhost PUT = %d %s", rec.Code, rec.Body)
	}
	if rec := serve("GET", "doze.test", "/vhost/dir/key.txt", ""); rec.Body.String() != "hello" {
		t.Errorf("path-style read of the vhost write = %q", rec.Body)
	}
	if rec := serve("GET", vhost, "/", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "dir/key.txt") {
		t.Errorf("vhost bucket listing = %d %s", rec.Code, rec.Body)
	}
}

func TestWhatNoRouteServesIsA405ForItsLevel(t *testing.T) {
	ts := s3Server(t)
	for _, c := range []struct{ method, path, want string }{
		{"PATCH", "/", "unsupported service-level method PATCH"},
		{"PATCH", "/b", "unsupported bucket-level request"},
		{"PATCH", "/b/k", "unsupported object-level request"},
		{"POST", "/b", "unsupported bucket-level request"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 405 || !strings.Contains(string(body), c.want) {
			t.Errorf("%s %s = %d %s, want 405 %q", c.method, c.path, resp.StatusCode, body, c.want)
		}
	}
}
