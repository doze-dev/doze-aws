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

	// The detail drawer surfaces object metadata (HeadObject).
	meta := req(t, h, "GET", "/_console/s3/webassets/meta?key=photos/logo.txt&prefix=photos/", nil)
	body := meta.Body.String()
	if meta.Code != 200 || !strings.Contains(body, "Storage class") || !strings.Contains(body, "s3://webassets/photos/logo.txt") {
		t.Fatalf("object meta drawer = %d\n%s", meta.Code, body)
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
		if !strings.Contains(m.Body.String(), `class="badge">1<`) {
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
	// The overview is a service directory: every service card + the caller identity.
	for _, want := range []string{"Overview", "DynamoDB", "EventBridge", "Secrets Manager", "Parameter Store", "000000000000"} {
		if !strings.Contains(body, want) {
			t.Fatalf("overview missing %q: %d\n%s", want, rec.Code, body)
		}
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

func TestConsoleDynamoDBFlow(t *testing.T) {
	h := newConsole(t)

	// Create a table with PK + SK via the dialog's form fields.
	rec := req(t, h, "POST", "/_console/ddb/create", url.Values{
		"name": {"users"}, "hash_key": {"pk"}, "hash_type": {"S"},
		"range_key": {"sk"}, "range_type": {"S"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "users") {
		t.Fatalf("create table: %d\n%s", rec.Code, rec.Body)
	}

	// Put an item as PLAIN JSON (the console converts to AttributeValue).
	put := req(t, h, "POST", "/_console/ddb/users/put", url.Values{
		"item": {`{"pk":"user-1","sk":"profile","name":"Ada","logins":42}`},
	})
	if put.Code != 200 || !strings.Contains(put.Body.String(), "user-1") {
		t.Fatalf("put item: %d\n%s", put.Code, put.Body)
	}

	// The table page shows the item and its schema.
	page := req(t, h, "GET", "/_console/ddb/users", nil)
	if !strings.Contains(page.Body.String(), "user-1") || !strings.Contains(page.Body.String(), "Ada") {
		t.Fatalf("table page missing item:\n%s", page.Body)
	}
	det := req(t, h, "GET", "/_console/ddb/users?tab=details", nil)
	if !strings.Contains(det.Body.String(), "Partition key") || !strings.Contains(det.Body.String(), "pk (S)") {
		t.Fatalf("details tab missing schema:\n%s", det.Body)
	}

	// Delete the item via its key JSON.
	del := req(t, h, "POST", "/_console/ddb/users/delete-item", url.Values{
		"key": {`{"pk":{"S":"user-1"},"sk":{"S":"profile"}}`},
	})
	if del.Code != 200 || strings.Contains(del.Body.String(), "Ada") {
		t.Fatalf("delete item: %d\n%s", del.Code, del.Body)
	}
}

func TestConsoleSNSFlow(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"sink"}})

	rec := req(t, h, "POST", "/_console/sns/create", url.Values{"name": {"events"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "events") {
		t.Fatalf("create topic: %d\n%s", rec.Code, rec.Body)
	}

	// Subscribe the queue, publish, and the message lands in the queue.
	sub := req(t, h, "POST", "/_console/sns/events/subscribe", url.Values{
		"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:sink"},
	})
	if sub.Code != 200 || !strings.Contains(sub.Body.String(), "sink") {
		t.Fatalf("subscribe: %d\n%s", sub.Code, sub.Body)
	}
	pub := req(t, h, "POST", "/_console/sns/events/publish", url.Values{"message": {"fan-out-me"}})
	if pub.Code != 200 {
		t.Fatalf("publish: %d\n%s", pub.Code, pub.Body)
	}
	msgs := req(t, h, "GET", "/_console/sqs/sink/messages", nil)
	if !strings.Contains(msgs.Body.String(), "fan-out-me") {
		t.Fatalf("published message never reached the queue:\n%s", msgs.Body)
	}
}

func TestConsoleEventBridgeFlow(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"evsink"}})

	rec := req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{
		"name": {"on-orders"}, "pattern": {`{"source":["orders"]}`},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "on-orders") {
		t.Fatalf("create rule: %d\n%s", rec.Code, rec.Body)
	}
	tgt := req(t, h, "POST", "/_console/eb/default/rule/on-orders/add-target", url.Values{
		"arn": {"arn:aws:sqs:us-east-1:000000000000:evsink"},
	})
	if tgt.Code != 200 || !strings.Contains(tgt.Body.String(), "evsink") {
		t.Fatalf("add target: %d\n%s", tgt.Code, tgt.Body)
	}
	// Test event matches the rule and lands in the queue.
	ev := req(t, h, "POST", "/_console/eb/default/test-event", url.Values{
		"source": {"orders"}, "detail_type": {"created"}, "detail": {`{"id":"A-1"}`},
	})
	if ev.Code != 200 {
		t.Fatalf("test event: %d\n%s", ev.Code, ev.Body)
	}
	msgs := req(t, h, "GET", "/_console/sqs/evsink/messages", nil)
	if !strings.Contains(msgs.Body.String(), "A-1") {
		t.Fatalf("test event never reached the queue:\n%s", msgs.Body)
	}
}

func TestConsoleKMSFlow(t *testing.T) {
	h := newConsole(t)

	rec := req(t, h, "POST", "/_console/kms/create", url.Values{
		"spec": {"SYMMETRIC_DEFAULT"}, "alias": {"app-data"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app-data") {
		t.Fatalf("create key: %d\n%s", rec.Code, rec.Body)
	}
	// Pull the key id out of the key list link (/_console/kms/<id>).
	body := rec.Body.String()
	i := strings.Index(body, "/_console/kms/")
	if i < 0 {
		t.Fatal("no key link in list")
	}
	id := body[i+len("/_console/kms/"):]
	id = id[:strings.IndexAny(id, `"`)]

	// Encrypt/decrypt round-trip through the playground.
	enc := req(t, h, "POST", "/_console/kms/"+id+"/encrypt", url.Values{"plaintext": {"round-trip-me"}})
	if enc.Code != 200 {
		t.Fatalf("encrypt: %d\n%s", enc.Code, enc.Body)
	}
	eb := enc.Body.String()
	ct := eb[strings.Index(eb, "<pre>")+5 : strings.Index(eb, "</pre>")]
	dec := req(t, h, "POST", "/_console/kms/"+id+"/decrypt", url.Values{"ciphertext": {ct}})
	if dec.Code != 200 || !strings.Contains(dec.Body.String(), "round-trip-me") {
		t.Fatalf("decrypt: %d\n%s", dec.Code, dec.Body)
	}
}

func TestConsoleSSMFlow(t *testing.T) {
	h := newConsole(t)

	rec := req(t, h, "POST", "/_console/ssm/create", url.Values{
		"name": {"/app/db/host"}, "value": {"localhost:5432"}, "type": {"String"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/app/db/host") {
		t.Fatalf("create param: %d\n%s", rec.Code, rec.Body)
	}
	// New version, then the page shows v2 + history.
	req(t, h, "POST", "/_console/ssm/put", url.Values{"name": {"/app/db/host"}, "value": {"db:5432"}})
	page := req(t, h, "GET", "/_console/ssm/param?name=/app/db/host", nil)
	if !strings.Contains(page.Body.String(), "db:5432") || !strings.Contains(page.Body.String(), "v2") {
		t.Fatalf("param page missing v2:\n%s", page.Body)
	}
}

func TestConsoleSecretsFlow(t *testing.T) {
	h := newConsole(t)

	rec := req(t, h, "POST", "/_console/sm/create", url.Values{
		"name": {"prod/db"}, "value": {`{"user":"admin"}`},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "prod/db") {
		t.Fatalf("create secret: %d\n%s", rec.Code, rec.Body)
	}
	page := req(t, h, "GET", "/_console/sm/secret?name=prod/db", nil)
	if !strings.Contains(page.Body.String(), "AWSCURRENT") || !strings.Contains(page.Body.String(), "admin") {
		t.Fatalf("secret page: %d\n%s", page.Code, page.Body)
	}
}

func TestConsoleS3Properties(t *testing.T) {
	h := newConsole(t)
	// Create with versioning on via the dialog options.
	req(t, h, "POST", "/_console/s3/create", url.Values{"name": {"propbkt"}, "versioning": {"on"}})
	page := req(t, h, "GET", "/_console/s3/propbkt?tab=properties", nil)
	body := page.Body.String()
	if !strings.Contains(body, "Versioning") || !strings.Contains(body, "state-on") ||
		!strings.Contains(body, "arn:aws:s3:::propbkt") {
		t.Fatalf("properties tab: %d\n%s", page.Code, body)
	}
}

func TestConsoleLambdaPage(t *testing.T) {
	h := newConsole(t)
	// Without functions the list shows the create hint.
	page := req(t, h, "GET", "/_console/lambda", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "_local_") {
		t.Fatalf("lambda list: %d\n%s", page.Code, page.Body)
	}
}
