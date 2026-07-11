package console_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/console"
)

func newConsole(t *testing.T) http.Handler {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	c, err := console.New(console.Options{Gateway: stack.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func req(t *testing.T, h http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestConsoleS3Flow(t *testing.T) {
	h := newConsole(t)

	// Create a bucket.
	rec := req(t, h, "POST", "/_console/s3/create", url.Values{"name": {"webassets"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "webassets") {
		t.Fatalf("create bucket: %d\n%s", rec.Code, rec.Body)
	}

	// Upload a file into a "folder" (the multipart layer basenames the filename,
	// so it lands at <prefix>/<basename>).
	up := multipartUpload(t, h, "/_console/s3/webassets/upload", "logo.txt", "hello-console", "photos/")
	if up.Code != 200 || !strings.Contains(up.Body.String(), "logo.txt") {
		t.Fatalf("upload: %d\n%s", up.Code, up.Body)
	}

	// The bucket page lists the folder (delimiter-based browsing).
	page := req(t, h, "GET", "/_console/s3/webassets", nil)
	if !strings.Contains(page.Body.String(), "photos/") {
		t.Fatalf("bucket page missing folder:\n%s", page.Body)
	}

	// Object bytes are served back.
	obj := req(t, h, "GET", "/_console/s3/webassets/object?key=photos/logo.txt", nil)
	if obj.Code != 200 || obj.Body.String() != "hello-console" {
		t.Fatalf("get object = %d %q", obj.Code, obj.Body.String())
	}

	// Delete it via the console.
	del := req(t, h, "POST", "/_console/s3/webassets/delete", url.Values{"key": {"photos/logo.txt"}, "prefix": {"photos/"}})
	if del.Code != 200 {
		t.Fatalf("delete: %d\n%s", del.Code, del.Body)
	}
}

func TestConsoleSQSFlow(t *testing.T) {
	h := newConsole(t)

	rec := req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"jobs"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "jobs") {
		t.Fatalf("create queue: %d\n%s", rec.Code, rec.Body)
	}

	// Send a message; the returned panel shows it.
	send := req(t, h, "POST", "/_console/sqs/jobs/send", url.Values{"body": {"UNIQUE-PAYLOAD-42"}})
	if send.Code != 200 || !strings.Contains(send.Body.String(), "UNIQUE-PAYLOAD-42") {
		t.Fatalf("send: %d\n%s", send.Code, send.Body)
	}

	// The peek is NON-DESTRUCTIVE: two refreshes both still show the message,
	// and the depth stays at 1 (the console's edge over the AWS console).
	for i := 0; i < 2; i++ {
		m := req(t, h, "GET", "/_console/sqs/jobs/messages", nil)
		if !strings.Contains(m.Body.String(), "UNIQUE-PAYLOAD-42") {
			t.Fatalf("peek %d lost the message (consumed?):\n%s", i, m.Body)
		}
		if !strings.Contains(m.Body.String(), `class="pill">1<`) {
			t.Fatalf("peek %d depth not 1:\n%s", i, m.Body)
		}
	}

	// Purge empties it.
	purge := req(t, h, "POST", "/_console/sqs/jobs/purge", nil)
	if purge.Code != 200 || strings.Contains(purge.Body.String(), "UNIQUE-PAYLOAD-42") {
		t.Fatalf("purge did not clear:\n%s", purge.Body)
	}
}

func TestConsoleOverview(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/s3/create", url.Values{"name": {"bkt"}})
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"que"}})

	rec := req(t, h, "GET", "/_console/", nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Overview") || !strings.Contains(body, "bkt") || !strings.Contains(body, "que") {
		t.Fatalf("overview missing content: %d\n%s", rec.Code, body)
	}
	// htmx must be served locally (embedded), not from a CDN.
	js := req(t, h, "GET", "/_console/static/htmx.min.js", nil)
	if js.Code != 200 || !strings.Contains(js.Body.String(), "htmx") {
		t.Fatalf("htmx not served: %d", js.Code)
	}
}

func multipartUpload(t *testing.T, h http.Handler, target, filename, content, prefix string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("prefix", prefix)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write([]byte(content))
	mw.Close()
	r := httptest.NewRequest("POST", target, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
