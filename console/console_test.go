package console_test

import (
	"bytes"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/console"
)

func newConsole(t *testing.T) http.Handler {
	c, _ := newConsoleStack(t)
	return c
}

// newConsoleStack returns the console AND the raw gateway handler — needed by
// tests that follow a link back to the AWS endpoint (e.g. presigned URLs).
func newConsoleStack(t *testing.T) (http.Handler, http.Handler) {
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
	return c, stack.Handler()
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

// create POSTs a create form and asserts the 303 redirect, returning the
// Location the console sent the browser to.
func create(t *testing.T, h http.Handler, target string, form url.Values) string {
	t.Helper()
	rec := req(t, h, "POST", target, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create %s: status %d (want 303)\n%s", target, rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatalf("create %s: no Location header", target)
	}
	return loc
}

func TestConsoleS3Flow(t *testing.T) {
	h := newConsole(t)

	// Create a bucket; the console lands on the new bucket with a flash banner.
	loc := create(t, h, "/_console/s3/create", url.Values{"name": {"webassets"}})
	if !strings.Contains(loc, "/s3/webassets") || !strings.Contains(loc, "flash=") {
		t.Fatalf("create bucket location = %q", loc)
	}
	page0 := req(t, h, "GET", loc, nil)
	if page0.Code != 200 || !strings.Contains(page0.Body.String(), "created") {
		t.Fatalf("bucket page after create missing flash: %d", page0.Code)
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

	if loc := create(t, h, "/_console/sqs/create", url.Values{"name": {"jobs"}}); !strings.Contains(loc, "/sqs/jobs") {
		t.Fatalf("create queue location = %q", loc)
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

func TestConsoleFlowsHome(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/s3/create", url.Values{"name": {"bkt"}})
	create(t, h, "/_console/sqs/create", url.Values{"name": {"que"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"topic"}})
	req(t, h, "POST", "/_console/sns/topic/subscribe", url.Values{"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:que"}})

	rec := req(t, h, "GET", "/_console/", nil)
	body := rec.Body.String()
	// Home is the wiring map; the subscribed topic → queue is a real edge, and
	// the unwired bucket is flagged.
	for _, want := range []string{"Flows", "flow-canvas", "topic", "que", "unwired"} {
		if !strings.Contains(body, want) {
			t.Fatalf("flows home missing %q: %d\n%s", want, rec.Code, body)
		}
	}
	// htmx must be served locally (embedded), not from a CDN.
	js := req(t, h, "GET", "/_console/static/htmx.min.js", nil)
	if js.Code != 200 || !strings.Contains(js.Body.String(), "htmx") {
		t.Fatalf("htmx not served: %d", js.Code)
	}
	// The filter store must be registered: table rows gate on
	// $store.filter.q, and without the store every row hides (falsy x-show).
	if !strings.Contains(body, `Alpine.store("filter"`) {
		t.Fatal("layout missing the Alpine filter store registration")
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
	if loc := create(t, h, "/_console/ddb/create", url.Values{
		"name": {"users"}, "hash_key": {"pk"}, "hash_type": {"S"},
		"range_key": {"sk"}, "range_type": {"S"},
	}); !strings.Contains(loc, "/ddb/users") {
		t.Fatalf("create table location = %q", loc)
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
	create(t, h, "/_console/sqs/create", url.Values{"name": {"sink"}})
	if loc := create(t, h, "/_console/sns/create", url.Values{"name": {"events"}}); !strings.Contains(loc, "/sns/events") {
		t.Fatalf("create topic location = %q", loc)
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
	create(t, h, "/_console/sqs/create", url.Values{"name": {"evsink"}})
	if loc := create(t, h, "/_console/eb/default/create-rule", url.Values{
		"name": {"on-orders"}, "pattern": {`{"source":["orders"]}`},
	}); !strings.Contains(loc, "/eb/default/rule/on-orders") {
		t.Fatalf("create rule location = %q", loc)
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

	loc := create(t, h, "/_console/kms/create", url.Values{
		"spec": {"SYMMETRIC_DEFAULT"}, "alias": {"app-data"},
	})
	// The redirect goes straight to the new key page: /_console/kms/<id>?flash=…
	id := strings.TrimPrefix(loc, "/_console/kms/")
	if i := strings.IndexAny(id, "?"); i >= 0 {
		id = id[:i]
	}
	if id == "" {
		t.Fatalf("no key id in location %q", loc)
	}

	// Encrypt/decrypt round-trip through the playground.
	enc := req(t, h, "POST", "/_console/kms/"+id+"/encrypt", url.Values{"plaintext": {"round-trip-me"}})
	if enc.Code != 200 {
		t.Fatalf("encrypt: %d\n%s", enc.Code, enc.Body)
	}
	eb := enc.Body.String()
	// html/template entity-escapes '+' etc. in text nodes; browsers decode them.
	ct := html.UnescapeString(eb[strings.Index(eb, "<pre>")+5 : strings.Index(eb, "</pre>")])
	dec := req(t, h, "POST", "/_console/kms/"+id+"/decrypt", url.Values{"ciphertext": {ct}})
	if dec.Code != 200 || !strings.Contains(dec.Body.String(), "round-trip-me") {
		t.Fatalf("decrypt: %d\n%s", dec.Code, dec.Body)
	}
}

func TestConsoleSSMFlow(t *testing.T) {
	h := newConsole(t)

	if loc := create(t, h, "/_console/ssm/create", url.Values{
		"name": {"/app/db/host"}, "value": {"localhost:5432"}, "type": {"String"},
	}); !strings.Contains(loc, "/ssm/param?name=") {
		t.Fatalf("create param location = %q", loc)
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

	if loc := create(t, h, "/_console/sm/create", url.Values{
		"name": {"prod/db"}, "value": {`{"user":"admin"}`},
	}); !strings.Contains(loc, "/sm/secret?name=") {
		t.Fatalf("create secret location = %q", loc)
	}
	page := req(t, h, "GET", "/_console/sm/secret?name=prod/db", nil)
	if !strings.Contains(page.Body.String(), "AWSCURRENT") || !strings.Contains(page.Body.String(), "admin") {
		t.Fatalf("secret page: %d\n%s", page.Code, page.Body)
	}
}

func TestConsoleS3Properties(t *testing.T) {
	h := newConsole(t)
	// Create with versioning on via the create-page options.
	create(t, h, "/_console/s3/create", url.Values{"name": {"propbkt"}, "versioning": {"on"}})
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
	if page.Code != 200 || !strings.Contains(page.Body.String(), "New function") {
		t.Fatalf("lambda list: %d\n%s", page.Code, page.Body)
	}
}

// TestConsoleCreatePages: every create form renders as a full page with its
// sectioned panels, and the palette API returns the live resource index.
func TestConsoleCreatePagesAndPalette(t *testing.T) {
	h := newConsole(t)

	for _, p := range []string{
		"/_console/s3/create", "/_console/sqs/create", "/_console/ddb/create",
		"/_console/sns/create", "/_console/eb/create-bus", "/_console/eb/default/create-rule",
		"/_console/kms/create", "/_console/ssm/create", "/_console/sm/create",
	} {
		rec := req(t, h, "GET", p, nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "form-page") {
			t.Fatalf("create page %s: %d", p, rec.Code)
		}
	}

	// Seed one resource, then the palette index includes it plus the actions.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"palq"}})
	api := req(t, h, "GET", "/_console/api/resources", nil)
	body := api.Body.String()
	if api.Code != 200 || !strings.Contains(body, `"palq"`) || !strings.Contains(body, `"u":"/_console/sqs/palq"`) {
		t.Fatalf("palette api: %d\n%s", api.Code, body)
	}
}

// TestConsoleEditing covers the new edit surfaces: bucket tags, queue
// attributes, and the prefilled secret/param editors.
func TestConsoleEditing(t *testing.T) {
	h := newConsole(t)

	// Bucket tags: add appears in the props partial, remove clears it.
	create(t, h, "/_console/s3/create", url.Values{"name": {"tagbkt"}})
	add := req(t, h, "POST", "/_console/s3/tagbkt/add-tag", url.Values{"key": {"env"}, "value": {"dev"}})
	if add.Code != 200 || !strings.Contains(add.Body.String(), "env") || !strings.Contains(add.Body.String(), "dev") {
		t.Fatalf("add tag: %d\n%s", add.Code, add.Body)
	}
	rm := req(t, h, "POST", "/_console/s3/tagbkt/remove-tag", url.Values{"key": {"env"}})
	if rm.Code != 200 || strings.Contains(rm.Body.String(), ">dev<") {
		t.Fatalf("remove tag: %d\n%s", rm.Code, rm.Body)
	}

	// SQS attributes: edit visibility, the config partial reflects it.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"editq"}})
	upd := req(t, h, "POST", "/_console/sqs/editq/attributes", url.Values{"visibility": {"120"}})
	if upd.Code != 200 || !strings.Contains(upd.Body.String(), "120 s") {
		t.Fatalf("set attributes: %d\n%s", upd.Code, upd.Body)
	}

	// The secret View masks values in place (keys stay visible).
	create(t, h, "/_console/sm/create", url.Values{"name": {"editsec"}, "value": {`{"pw":"old-value"}`}})
	view := req(t, h, "GET", "/_console/sm/secret?name=editsec", nil)
	// Keys stay visible; the value is masked with dots (reveal is client-side).
	if !strings.Contains(view.Body.String(), "ws-view") || !strings.Contains(view.Body.String(), "pw") || !strings.Contains(view.Body.String(), "\u2022\u2022") {
		t.Fatalf("secret view should mask the value:\n%s", view.Body)
	}
	// Edit mode prefills the editor with the real value.
	edit := req(t, h, "GET", "/_console/sm/secret?name=editsec&tab=edit", nil)
	if !strings.Contains(edit.Body.String(), `data-editor`) || !strings.Contains(edit.Body.String(), "old-value") {
		t.Fatalf("secret edit not prefilled:\n%s", edit.Body)
	}
	// Versions mode + diff against current.
	req(t, h, "POST", "/_console/sm/put", url.Values{"name": {"editsec"}, "value": {`{"pw":"new-value"}`}})
	diff := req(t, h, "GET", "/_console/sm/diff?name=editsec&v=", nil)
	if !strings.Contains(diff.Body.String(), "diffbox") {
		t.Fatalf("secret diff missing:\n%s", diff.Body)
	}

	// Param edit mode prefills too.
	create(t, h, "/_console/ssm/create", url.Values{"name": {"/edit/me"}, "value": {"v1-value"}, "type": {"String"}})
	ppage := req(t, h, "GET", "/_console/ssm/param?name=/edit/me&tab=edit", nil)
	if !strings.Contains(ppage.Body.String(), `data-editor`) || !strings.Contains(ppage.Body.String(), "v1-value") {
		t.Fatalf("param edit not prefilled:\n%s", ppage.Body)
	}

	// CodeMirror ships embedded.
	cm := req(t, h, "GET", "/_console/static/cm/codemirror.min.js", nil)
	if cm.Code != 200 || cm.Body.Len() < 100000 {
		t.Fatalf("codemirror not served: %d (%d bytes)", cm.Code, cm.Body.Len())
	}
}

// TestFlowsGraph: the wiring graph reflects real edges built from the services.
func TestFlowsGraph(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"jobs"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"events"}})
	req(t, h, "POST", "/_console/sns/events/subscribe", url.Values{"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:jobs"}})
	create(t, h, "/_console/s3/create", url.Values{"name": {"loosebkt"}})

	// The polled data endpoint returns the canvas with the sub edge + unwired bucket.
	rec := req(t, h, "GET", "/_console/flows.json", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "events") || !strings.Contains(body, "jobs") || !strings.Contains(body, "sub") {
		t.Fatalf("flows graph missing the SNS→SQS edge:\n%s", body)
	}
	if !strings.Contains(body, "unwired") {
		t.Fatalf("flows graph should flag the unwired bucket:\n%s", body)
	}
	// Unchanged poll (matching hash) returns 204.
	// (extract hash from data-hash attribute)
	i := strings.Index(body, `data-hash="`)
	if i >= 0 {
		hash := body[i+len(`data-hash="`):]
		hash = hash[:strings.IndexByte(hash, '"')]
		again := req(t, h, "GET", "/_console/flows.json?h="+hash, nil)
		if again.Code != 204 {
			t.Fatalf("unchanged flows poll should 204, got %d", again.Code)
		}
	}
}

// TestConnectionsView: a resource's detail page shows its 1-hop wiring — a
// queue with a redrive policy drains to its DLQ, which in turn is fed by it.
func TestConnectionsView(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"dead-letter"}})
	create(t, h, "/_console/sqs/create", url.Values{
		"name": {"orders"}, "dlq": {"dead-letter"}, "max_receive": {"5"},
	})

	// The orders page drains to dead-letter.
	orders := req(t, h, "GET", "/_console/sqs/orders", nil).Body.String()
	if !strings.Contains(orders, "Drains to") || !strings.Contains(orders, "dead-letter") {
		t.Fatalf("orders page missing its DLQ connection:\n%s", orders)
	}
	if !strings.Contains(orders, "redrive") {
		t.Fatalf("orders connection should be labelled by edge kind (redrive):\n%s", orders)
	}
	// The dead-letter page is fed by orders (the reverse edge).
	dl := req(t, h, "GET", "/_console/sqs/dead-letter", nil).Body.String()
	if !strings.Contains(dl, "Fed by") || !strings.Contains(dl, "orders") {
		t.Fatalf("dead-letter page missing its upstream connection:\n%s", dl)
	}
}

// TestSQSMessagingThoroughness: the full publish/create surface — FIFO queue
// with an auto-created dead-letter queue and content-based dedup, FIFO + DLQ
// badges, and sends carrying group/dedup/message attributes that the peek
// then displays.
func TestSQSMessagingThoroughness(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{
		"name": {"orders"}, "fifo": {"on"}, "content_dedup": {"on"},
		"dlq_mode": {"new"}, "max_receive": {"4"},
	})

	// The queue page wears the FIFO badge and the composer offers FIFO fields.
	page := req(t, h, "GET", "/_console/sqs/orders.fifo", nil).Body.String()
	for _, want := range []string{">FIFO<", "Deduplication ID", "Message group ID", "orders-dlq.fifo"} {
		if !strings.Contains(page, want) {
			t.Fatalf("queue page missing %q", want)
		}
	}
	// Config tab reports content-based dedup; the auto-created DLQ is wired.
	cfg := req(t, h, "GET", "/_console/sqs/orders.fifo?tab=config", nil).Body.String()
	if !strings.Contains(cfg, "Content-based deduplication") || !strings.Contains(cfg, "orders-dlq.fifo") {
		t.Fatalf("config tab missing dedup row or DLQ wiring:\n%s", cfg)
	}
	// The DLQ itself exists (FIFO, matching the main queue) and wears DLQ badge.
	dlq := req(t, h, "GET", "/_console/sqs/orders-dlq.fifo", nil).Body.String()
	if !strings.Contains(dlq, ">DLQ<") {
		t.Fatalf("auto-created dead-letter queue should wear the DLQ badge:\n%s", dlq)
	}

	// Send with the full metadata set; the peek shows all of it.
	req(t, h, "POST", "/_console/sqs/orders.fifo/send", url.Values{
		"body": {`{"n":1}`}, "group": {"batch-7"}, "dedup": {"dedup-42"},
		"attrs": {`[{"n":"traceId","t":"String","v":"abc-123"},{"n":"retries","t":"Number","v":"2"}]`},
	})
	msgs := req(t, h, "GET", "/_console/sqs/orders.fifo/messages", nil).Body.String()
	for _, want := range []string{"batch-7", "dedup-42", "traceId", "abc-123", "retries"} {
		if !strings.Contains(msgs, want) {
			t.Fatalf("peek missing message metadata %q:\n%s", want, msgs)
		}
	}
}

// TestDynamoDBExplorer exercises the three read modes — Scan, Query (base key),
// and PartiQL — each returning the right subset of a seeded table.
func TestDynamoDBExplorer(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/ddb/create", url.Values{
		"name": {"events"}, "hash_key": {"userId"}, "hash_type": {"S"},
		"range_key": {"ts"}, "range_type": {"N"},
	})
	put := func(uid, ts, kind string) {
		req(t, h, "POST", "/_console/ddb/events/put", url.Values{
			"item": {`{"userId":"` + uid + `","ts":` + ts + `,"kind":"` + kind + `"}`},
		})
	}
	put("user-1", "1", "click")
	put("user-1", "2", "click")
	put("user-2", "9", "view")

	rows := func(body string) int { return strings.Count(body, `<tr `) }

	// Scan returns everything.
	scan := req(t, h, "POST", "/_console/ddb/events/explore", url.Values{"mode": {"scan"}}).Body.String()
	if n := rows(scan); n != 3 {
		t.Fatalf("scan: got %d rows, want 3", n)
	}
	// Query on the partition key returns just that partition.
	q := req(t, h, "POST", "/_console/ddb/events/explore", url.Values{
		"mode": {"query"}, "pk": {"user-1"},
	}).Body.String()
	if n := rows(q); n != 2 {
		t.Fatalf("query user-1: got %d rows, want 2", n)
	}
	if !strings.Contains(q, "matched") {
		t.Fatalf("query footer should say 'matched':\n%s", q)
	}
	// Query with a sort-key condition narrows further.
	q2 := req(t, h, "POST", "/_console/ddb/events/explore", url.Values{
		"mode": {"query"}, "pk": {"user-1"}, "sk_op": {"="}, "sk": {"2"},
	}).Body.String()
	if n := rows(q2); n != 1 {
		t.Fatalf("query user-1 ts=2: got %d rows, want 1", n)
	}
	// PartiQL SELECT.
	pq := req(t, h, "POST", "/_console/ddb/events/explore", url.Values{
		"mode": {"partiql"}, "statement": {`SELECT * FROM "events" WHERE kind = 'view'`},
	}).Body.String()
	if n := rows(pq); n != 1 {
		t.Fatalf("partiql kind=view: got %d rows, want 1", n)
	}
}

// TestDynamoDBCreateWithGSIAndTTL: a GSI declared at create time is queryable
// by its own key, and TTL is enabled on the named attribute.
func TestDynamoDBCreateWithGSIAndTTL(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/ddb/create", url.Values{
		"name": {"logs"}, "hash_key": {"id"}, "hash_type": {"S"},
		"gsi_name": {"by-level"}, "gsi_hash": {"level"}, "gsi_hash_type": {"S"},
		"gsi_range": {"ts"}, "gsi_range_type": {"N"},
		"ttl_attr": {"expiresAt"},
	})
	// The GSI shows up in the Details tab with its full key schema.
	details := req(t, h, "GET", "/_console/ddb/logs?tab=details", nil).Body.String()
	for _, want := range []string{"by-level", "level (S)", "ts (N)"} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing GSI schema %q:\n%s", want, details)
		}
	}
	// Items land, and the GSI is queryable by its own partition key.
	req(t, h, "POST", "/_console/ddb/logs/put", url.Values{
		"item": {`{"id":"a","level":"warn","ts":1}`},
	})
	req(t, h, "POST", "/_console/ddb/logs/put", url.Values{
		"item": {`{"id":"b","level":"warn","ts":2}`},
	})
	req(t, h, "POST", "/_console/ddb/logs/put", url.Values{
		"item": {`{"id":"c","level":"info","ts":3}`},
	})
	q := req(t, h, "POST", "/_console/ddb/logs/explore", url.Values{
		"mode": {"query"}, "index": {"by-level"}, "pk": {"warn"},
	}).Body.String()
	if n := strings.Count(q, `<tr `); n != 2 {
		t.Fatalf("GSI query level=warn: got %d rows, want 2:\n%s", n, q)
	}
}

// TestDynamoDBTableEditing covers wave E: TTL and GSIs can be changed after a
// table exists, and the item browser pages through with a real cursor.
func TestDynamoDBTableEditing(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/ddb/create", url.Values{"name": {"sessions"}, "hash_key": {"id"}, "hash_type": {"S"}})

	// Enable TTL after creation.
	det := req(t, h, "POST", "/_console/ddb/sessions/ttl", url.Values{"attr": {"expiresAt"}}).Body.String()
	if !strings.Contains(det, "expiresAt") || !strings.Contains(det, "ENABLED") {
		t.Fatalf("TTL not shown enabled after set:\n%s", det)
	}

	// Add a GSI after creation, then it is queryable.
	det = req(t, h, "POST", "/_console/ddb/sessions/add-gsi", url.Values{
		"gsi_name": {"by-user"}, "gsi_hash": {"userId"}, "gsi_hash_type": {"S"},
	}).Body.String()
	if !strings.Contains(det, "by-user") {
		t.Fatalf("added GSI not shown:\n%s", det)
	}

	// Delete the GSI.
	det = req(t, h, "POST", "/_console/ddb/sessions/delete-gsi", url.Values{"index": {"by-user"}}).Body.String()
	if strings.Contains(det, "by-user") {
		t.Fatalf("GSI still present after delete:\n%s", det)
	}

	// Pagination: three items, page size 1 → first page has a cursor.
	for _, id := range []string{"s1", "s2", "s3"} {
		req(t, h, "POST", "/_console/ddb/sessions/put", url.Values{"item": {`{"id":"` + id + `","userId":"u"}`}})
	}
	page1 := req(t, h, "POST", "/_console/ddb/sessions/explore", url.Values{
		"mode": {"scan"}, "limit": {"1"},
	}).Body.String()
	if strings.Count(page1, "<tr x-data") != 1 || !strings.Contains(page1, "Load 50 more") {
		t.Fatalf("first page should have 1 row + a load-more trigger:\n%s", page1)
	}
	// Pull the cursor out of the load-more button's hx-vals and load the next page.
	cur := between(html.UnescapeString(page1), `"cursor":"`, `"`)
	if cur == "" {
		t.Fatalf("no pagination cursor in first page:\n%s", page1)
	}
	page2 := req(t, h, "POST", "/_console/ddb/sessions/explore", url.Values{
		"mode": {"scan"}, "limit": {"1"}, "cursor": {cur},
	}).Body.String()
	if !strings.Contains(page2, "<tr x-data") {
		t.Fatalf("second page returned no rows:\n%s", page2)
	}
	// The append fragment must NOT re-wrap the whole #ddb-items container.
	if strings.Contains(page2, `id="ddb-items"`) {
		t.Fatalf("load-more should return a row fragment, not the full table:\n%s", page2)
	}
}

// TestFailureLoopRecovery covers wave A: messages land in a DLQ and come BACK
// (redrive), a single message can be deleted from the peek, the redrive policy
// is editable post-create, a deleted secret is restorable, and an EventBridge
// rule can be disabled and re-enabled.
func TestFailureLoopRecovery(t *testing.T) {
	h := newConsole(t)

	// --- SQS: build main + dlq, park messages in the dlq, redrive them back.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	req(t, h, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})
	req(t, h, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":2}`}})

	// The DLQ page offers redrive toward its source.
	page := req(t, h, "GET", "/_console/sqs/work-dlq", nil).Body.String()
	if !strings.Contains(page, "Redrive") || !strings.Contains(page, `value="work"`) {
		t.Fatalf("DLQ page missing redrive toward source:\n%s", page)
	}
	req(t, h, "POST", "/_console/sqs/work-dlq/redrive", url.Values{"dest": {"work"}})
	// The move task drains the DLQ into the source (poll briefly — it's async).
	var moved bool
	for range 50 {
		main := html.UnescapeString(req(t, h, "GET", "/_console/sqs/work/messages", nil).Body.String())
		if strings.Contains(main, `{"n":1}`) && strings.Contains(main, `{"n":2}`) {
			moved = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !moved {
		t.Fatalf("redriven messages never arrived on the source queue")
	}

	// --- Single-message delete from the peek.
	msgs := req(t, h, "GET", "/_console/sqs/work/messages", nil).Body.String()
	i := strings.Index(msgs, `"handle":"`)
	if i < 0 {
		t.Fatalf("peek rows missing delete handles:\n%s", msgs)
	}
	handle := msgs[i+len(`"handle":"`):]
	handle = handle[:strings.IndexByte(handle, '"')]
	req(t, h, "POST", "/_console/sqs/work/delete-message", url.Values{"handle": {handle}})
	after := req(t, h, "GET", "/_console/sqs/work/messages", nil).Body.String()
	if strings.Count(after, "msg-del") != 1 {
		t.Fatalf("single delete should leave exactly one message:\n%s", after)
	}

	// --- Redrive policy edit post-create: retarget + then remove.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"other-dlq"}, "dlq_mode": {"none"}})
	req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"other-dlq"}, "max_receive": {"7"}})
	cfg := req(t, h, "GET", "/_console/sqs/work?tab=config", nil).Body.String()
	if !strings.Contains(cfg, "other-dlq") || !strings.Contains(cfg, "7") {
		t.Fatalf("redrive edit did not land:\n%s", cfg)
	}
	req(t, h, "POST", "/_console/sqs/work/attributes", url.Values{"dlq": {"none"}})
	cfg = req(t, h, "GET", "/_console/sqs/work?tab=config", nil).Body.String()
	if strings.Contains(cfg, "Dead-letter queue</td>") {
		t.Fatalf("redrive removal did not land:\n%s", cfg)
	}

	// --- Secrets: delete → still listed + restorable → restored.
	create(t, h, "/_console/sm/create", url.Values{"name": {"app/tok"}, "value": {"v1"}})
	req(t, h, "POST", "/_console/sm/delete", url.Values{"name": {"app/tok"}})
	detail := req(t, h, "GET", "/_console/sm/secret?name=app/tok", nil).Body.String()
	if !strings.Contains(detail, "pending deletion") || !strings.Contains(detail, "Restore secret") {
		t.Fatalf("deleted secret page missing restore affordance:\n%s", detail)
	}
	req(t, h, "POST", "/_console/sm/restore", url.Values{"name": {"app/tok"}})
	detail = req(t, h, "GET", "/_console/sm/secret?name=app/tok", nil).Body.String()
	if strings.Contains(detail, "pending deletion") {
		t.Fatalf("restore did not clear the pending state:\n%s", detail)
	}

	// --- EventBridge: disable then re-enable a rule.
	req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{
		"name": {"r1"}, "pattern": {`{"source":["x"]}`},
	})
	req(t, h, "POST", "/_console/eb/default/rule/r1/toggle", nil)
	rp := req(t, h, "GET", "/_console/eb/default/rule/r1", nil).Body.String()
	if !strings.Contains(rp, "DISABLED") || !strings.Contains(rp, "Enable") {
		t.Fatalf("rule should be disabled with an Enable button:\n%s", rp)
	}
	req(t, h, "POST", "/_console/eb/default/rule/r1/toggle", nil)
	rp = req(t, h, "GET", "/_console/eb/default/rule/r1", nil).Body.String()
	if !strings.Contains(rp, "ENABLED") {
		t.Fatalf("rule should be re-enabled:\n%s", rp)
	}
}

// TestS3EditingDepth covers wave B: version history (list, restore, delete),
// share links that really expire, copy/rename, the notification editor, and
// the CORS/lifecycle JSON editors round-tripping to the XML wire configs.
func TestS3EditingDepth(t *testing.T) {
	h, gw := newConsoleStack(t)
	create(t, h, "/_console/s3/create", url.Values{"name": {"docs"}, "versioning": {"on"}})
	create(t, h, "/_console/sqs/create", url.Values{"name": {"events"}, "dlq_mode": {"none"}})

	// Two versions of the same key.
	multipartUpload(t, h, "/_console/s3/docs/upload", "a.txt", "v1-content", "")
	multipartUpload(t, h, "/_console/s3/docs/upload", "a.txt", "v2-content", "")

	// Version history shows both, newest marked current.
	vs := req(t, h, "GET", "/_console/s3/docs/versions?key=a.txt", nil).Body.String()
	if strings.Count(vs, `ver-id mono`) != 2 || !strings.Contains(vs, "current") {
		t.Fatalf("expected 2 versions with a current marker:\n%s", vs)
	}
	// Grab the OLDEST version id (rows are newest-first; take the last one).
	oldID := ""
	for _, chunk := range strings.Split(vs, `"versionId":"`)[1:] {
		oldID = chunk[:strings.IndexByte(chunk, '"')]
	}
	req(t, h, "POST", "/_console/s3/docs/restore-version", url.Values{"key": {"a.txt"}, "versionId": {oldID}})
	obj := req(t, h, "GET", "/_console/s3/docs/object?key=a.txt", nil)
	if obj.Body.String() != "v1-content" {
		t.Fatalf("restore should make v1 current, got %q", obj.Body.String())
	}
	// A specific version is fetchable by id.
	verObj := req(t, h, "GET", "/_console/s3/docs/object?key=a.txt&versionId="+url.QueryEscape(oldID), nil)
	if verObj.Body.String() != "v1-content" {
		t.Fatalf("version fetch = %q", verObj.Body.String())
	}

	// Share link: works now, expires for real.
	share := req(t, h, "POST", "/_console/s3/docs/presign", url.Values{"key": {"a.txt"}, "ttl": {"15m"}}).Body.String()
	i := strings.Index(share, "http://")
	if i < 0 {
		t.Fatalf("no share link generated:\n%s", share)
	}
	end := strings.IndexAny(share[i:], `"<`)
	link := html.UnescapeString(share[i : i+end])
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("share link unparseable: %v", err)
	}
	direct := req(t, gw, "GET", u.Path+"?"+u.RawQuery, nil)
	if direct.Code != 200 || direct.Body.String() != "v1-content" {
		t.Fatalf("share link should serve the object: %d %q", direct.Code, direct.Body.String())
	}
	// Tamper the date to the past — the gateway must refuse.
	q := u.Query()
	q.Set("X-Amz-Date", "20200101T000000Z")
	expired := req(t, gw, "GET", u.Path+"?"+q.Encode(), nil)
	if expired.Code == 200 {
		t.Fatalf("expired share link still served (code %d)", expired.Code)
	}

	// Copy then move.
	req(t, h, "POST", "/_console/s3/docs/copy", url.Values{"src": {"a.txt"}, "dst": {"b.txt"}})
	if got := req(t, h, "GET", "/_console/s3/docs/object?key=b.txt", nil).Body.String(); got != "v1-content" {
		t.Fatalf("copy content = %q", got)
	}
	req(t, h, "POST", "/_console/s3/docs/copy", url.Values{"src": {"b.txt"}, "dst": {"c/renamed.txt"}, "move": {"true"}})
	if code := req(t, h, "GET", "/_console/s3/docs/object?key=b.txt", nil).Code; code == 200 {
		t.Fatalf("move should delete the source")
	}

	// Notification editor: wire → visible on properties → remove.
	req(t, h, "POST", "/_console/s3/docs/notify-add", url.Values{
		"dest": {"sqs:events"}, "event": {"s3:ObjectCreated:*"}, "prefix": {"in/"},
	})
	props := req(t, h, "GET", "/_console/s3/docs?tab=properties", nil).Body.String()
	if !strings.Contains(props, "Stop notifying") || !strings.Contains(props, "in/") {
		t.Fatalf("notification not shown on properties:\n%s", props)
	}
	req(t, h, "POST", "/_console/s3/docs/notify-remove", url.Values{"index": {"0"}})
	props = req(t, h, "GET", "/_console/s3/docs?tab=properties", nil).Body.String()
	if strings.Contains(props, "Stop notifying") {
		t.Fatalf("notification should be gone:\n%s", props)
	}

	// CORS + lifecycle JSON round-trips.
	req(t, h, "POST", "/_console/s3/docs/cors", url.Values{
		"rules": {`[{"AllowedOrigins":["http://localhost:3000"],"AllowedMethods":["GET","PUT"],"MaxAgeSeconds":300}]`},
	})
	props = req(t, h, "GET", "/_console/s3/docs?tab=properties", nil).Body.String()
	if !strings.Contains(html.UnescapeString(props), "http://localhost:3000") {
		t.Fatalf("CORS JSON not round-tripped into the editor:\n%s", props)
	}
	req(t, h, "POST", "/_console/s3/docs/lifecycle", url.Values{
		"rules": {`[{"Prefix":"tmp/","ExpireDays":7}]`},
	})
	props = req(t, h, "GET", "/_console/s3/docs?tab=properties", nil).Body.String()
	if !strings.Contains(html.UnescapeString(props), `"ExpireDays": 7`) {
		t.Fatalf("lifecycle JSON not round-tripped:\n%s", props)
	}
}

// TestLambdaLifecycle covers wave C: create a function from a local build dir,
// edit env/timeout/memory, provision + drop a function URL, add an event
// source mapping, and invoke both sync and async.
func TestLambdaLifecycle(t *testing.T) {
	h := newConsole(t)

	// A real build directory the _local_ extension can run.
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/bootstrap", []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	create(t, h, "/_console/sqs/create", url.Values{"name": {"jobs"}, "dlq_mode": {"none"}})

	// Create the function from the UI form.
	loc := create(t, h, "/_console/lambda/create", url.Values{
		"name": {"worker"}, "runtime": {"provided.al2"}, "handler": {"bootstrap"},
		"code": {dir}, "timeout": {"15"}, "memory": {"256"},
		"env_key": {"LOG_LEVEL"}, "env_val": {"debug"},
	})
	if !strings.Contains(loc, "/lambda/worker") {
		t.Fatalf("create function location = %q", loc)
	}
	page := req(t, h, "GET", "/_console/lambda/worker?tab=config", nil).Body.String()
	for _, want := range []string{`value="15"`, `value="256"`, "LOG_LEVEL", "debug"} {
		if !strings.Contains(page, want) {
			t.Fatalf("config tab missing %q:\n%s", want, page)
		}
	}

	// Edit config: bump memory, add a variable.
	req(t, h, "POST", "/_console/lambda/worker/config", url.Values{
		"timeout": {"30"}, "memory": {"512"},
		"env_key": {"LOG_LEVEL", "REGION"}, "env_val": {"info", "us-east-1"},
	})
	page = req(t, h, "GET", "/_console/lambda/worker?tab=config", nil).Body.String()
	if !strings.Contains(page, `value="512"`) || !strings.Contains(page, "REGION") {
		t.Fatalf("config edit did not land:\n%s", page)
	}

	// Function URL: create then remove.
	cfg := req(t, h, "POST", "/_console/lambda/worker/create-url", nil).Body.String()
	if !strings.Contains(cfg, "lambda-url") {
		t.Fatalf("function URL not shown after create:\n%s", cfg)
	}
	cfg = req(t, h, "POST", "/_console/lambda/worker/delete-url", nil).Body.String()
	if strings.Contains(cfg, "Remove URL") {
		t.Fatalf("function URL should be gone:\n%s", cfg)
	}

	// Event source mapping.
	trig := req(t, h, "POST", "/_console/lambda/worker/add-mapping", url.Values{
		"queue": {"jobs"}, "batch": {"5"},
	}).Body.String()
	if !strings.Contains(trig, "jobs") || !strings.Contains(trig, ">5<") {
		t.Fatalf("event source mapping not shown:\n%s", trig)
	}

	// Async invoke returns the accepted receipt (no payload).
	as := req(t, h, "POST", "/_console/lambda/worker/invoke", url.Values{
		"payload": {"{}"}, "async": {"true"},
	}).Body.String()
	if !strings.Contains(as, "Event accepted") {
		t.Fatalf("async invoke should return an accepted receipt:\n%s", as)
	}
}

// TestCryptoConfigDepth exercises wave D: usage-aware KMS playground
// (sign/verify + HMAC round-trips), alias management, SSM version labels,
// and the Secrets Manager password generator.
func TestCryptoConfigDepth(t *testing.T) {
	h := newConsole(t)

	// --- KMS asymmetric: sign then verify the same message round-trips. ---
	loc := create(t, h, "/_console/kms/create", url.Values{
		"spec": {"RSA_2048"}, "usage": {"SIGN_VERIFY"}, "alias": {"signer"},
	})
	signKey := strings.TrimPrefix(loc, "/_console/kms/")
	if i := strings.IndexByte(signKey, '?'); i >= 0 {
		signKey = signKey[:i]
	}
	// The detail page offers a Sign/verify panel (not encrypt/decrypt).
	page := req(t, h, "GET", "/_console/kms/"+signKey, nil).Body.String()
	if !strings.Contains(page, "Sign / verify") || strings.Contains(page, "Encrypt / decrypt") {
		t.Fatalf("SIGN_VERIFY key should show the sign panel only:\n%s", page)
	}
	signed := req(t, h, "POST", "/_console/kms/"+signKey+"/sign", url.Values{
		"algo": {"RSASSA_PKCS1_V1_5_SHA_256"}, "message": {"attest this"},
	}).Body.String()
	sig := html.UnescapeString(between(signed, "<pre>", "</pre>"))
	if sig == "" {
		t.Fatalf("sign produced no signature:\n%s", signed)
	}
	good := req(t, h, "POST", "/_console/kms/"+signKey+"/verify", url.Values{
		"algo": {"RSASSA_PKCS1_V1_5_SHA_256"}, "message": {"attest this"}, "signature": {sig},
	}).Body.String()
	if !strings.Contains(good, "valid") || strings.Contains(good, "does NOT") {
		t.Fatalf("signature over the same message should verify:\n%s", good)
	}
	bad := req(t, h, "POST", "/_console/kms/"+signKey+"/verify", url.Values{
		"algo": {"RSASSA_PKCS1_V1_5_SHA_256"}, "message": {"tampered"}, "signature": {sig},
	}).Body.String()
	if !strings.Contains(bad, "does NOT") {
		t.Fatalf("signature over a different message must not verify:\n%s", bad)
	}

	// Alias management: add a second alias, then it shows on the key.
	added := req(t, h, "POST", "/_console/kms/"+signKey+"/add-alias", url.Values{"alias": {"jwt-signer"}}).Body.String()
	if !strings.Contains(added, "jwt-signer") {
		t.Fatalf("added alias not listed:\n%s", added)
	}

	// --- KMS HMAC: generate then verify a MAC round-trips. ---
	loc = create(t, h, "/_console/kms/create", url.Values{"spec": {"HMAC_256"}, "usage": {"GENERATE_VERIFY_MAC"}})
	macKey := strings.TrimPrefix(loc, "/_console/kms/")
	if i := strings.IndexByte(macKey, '?'); i >= 0 {
		macKey = macKey[:i]
	}
	macOut := req(t, h, "POST", "/_console/kms/"+macKey+"/mac", url.Values{
		"algo": {"HMAC_SHA_256"}, "message": {"authenticate me"},
	}).Body.String()
	mac := html.UnescapeString(between(macOut, "<pre>", "</pre>"))
	if mac == "" {
		t.Fatalf("HMAC generation produced nothing:\n%s", macOut)
	}
	macVerdict := req(t, h, "POST", "/_console/kms/"+macKey+"/verify-mac", url.Values{
		"algo": {"HMAC_SHA_256"}, "message": {"authenticate me"}, "mac": {mac},
	}).Body.String()
	if !strings.Contains(macVerdict, "valid") || strings.Contains(macVerdict, "does NOT") {
		t.Fatalf("MAC over the same message should verify:\n%s", macVerdict)
	}

	// --- SSM: attach a label to a version, then it renders on the versions tab. ---
	create(t, h, "/_console/ssm/create", url.Values{"name": {"/app/db/host"}, "type": {"String"}, "value": {"localhost"}})
	req(t, h, "POST", "/_console/ssm/put", url.Values{"name": {"/app/db/host"}, "value": {"db.internal"}})
	req(t, h, "POST", "/_console/ssm/label", url.Values{"name": {"/app/db/host"}, "label": {"prod"}, "version": {"2"}})
	vers := req(t, h, "GET", "/_console/ssm/param?name=/app/db/host&tab=versions", nil).Body.String()
	if !strings.Contains(vers, "prod") {
		t.Fatalf("SSM version label not shown on the versions tab:\n%s", vers)
	}

	// --- Secrets Manager: the password generator returns a strong string. ---
	pw := req(t, h, "GET", "/_console/sm/password", nil).Body.String()
	if len(pw) < 16 {
		t.Fatalf("generated password too short: %q", pw)
	}
	// The create form exposes the generator button.
	form := req(t, h, "GET", "/_console/sm/create", nil).Body.String()
	if !strings.Contains(form, "Generate password") {
		t.Fatalf("SM create form missing the password generator")
	}
}

// TestSNSSubscriptionAttributes exercises wave E's SNS surface: a subscription's
// filter policy and raw-delivery flag are set through the console and reflected
// back on the subscribers panel.
func TestSNSSubscriptionAttributes(t *testing.T) {
	h := newConsole(t)

	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"events"}})

	// Subscribe WITH a filter policy + raw delivery applied at creation time.
	sub0 := req(t, h, "POST", "/_console/sns/events/subscribe", url.Values{
		"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:orders"},
		"policy": {`{"tier":["gold"]}`}, "raw": {"on"},
	}).Body.String()
	if !strings.Contains(sub0, "filtered") || !strings.Contains(sub0, ">raw<") {
		t.Fatalf("subscribe-time filter/raw not applied:\n%s", sub0)
	}

	// Set a filter policy.
	pol := `{"eventType":["OrderPlaced"]}`
	panel := req(t, h, "POST", "/_console/sns/events/sub-filter", url.Values{
		"arn": {subARN(t, h, "events")}, "policy": {pol},
	}).Body.String()
	if !strings.Contains(panel, "filtered") {
		t.Fatalf("filter policy not reflected on the panel:\n%s", panel)
	}
	if !strings.Contains(html.UnescapeString(panel), "OrderPlaced") {
		t.Fatalf("filter policy JSON not shown in the editor:\n%s", panel)
	}

	// Turn on raw delivery.
	panel = req(t, h, "POST", "/_console/sns/events/sub-raw", url.Values{
		"arn": {subARN(t, h, "events")}, "raw": {"on"},
	}).Body.String()
	if !strings.Contains(panel, ">raw<") {
		t.Fatalf("raw delivery badge not shown after enabling:\n%s", panel)
	}
}

// subARN fetches the (single) subscription ARN of a topic from its detail page.
func subARN(t *testing.T, h http.Handler, topic string) string {
	t.Helper()
	body := req(t, h, "GET", "/_console/sns/"+topic, nil).Body.String()
	const marker = `/unsubscribe" hx-vals='{"arn":"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no subscription found on %s page", topic)
	}
	rest := body[i+len(marker):]
	return rest[:strings.IndexByte(rest, '"')]
}

// TestTagsEverywhere covers wave E's cross-service tag surface: the same editor
// tags an SQS queue, a DynamoDB table, and a KMS key through the generic routes.
func TestTagsEverywhere(t *testing.T) {
	h := newConsole(t)

	create(t, h, "/_console/sqs/create", url.Values{"name": {"jobs"}, "dlq_mode": {"none"}})
	create(t, h, "/_console/ddb/create", url.Values{"name": {"users"}, "hash_key": {"id"}, "hash_type": {"S"}})
	kmsLoc := create(t, h, "/_console/kms/create", url.Values{"spec": {"SYMMETRIC_DEFAULT"}})
	keyID := strings.TrimPrefix(kmsLoc, "/_console/kms/")
	if i := strings.IndexByte(keyID, '?'); i >= 0 {
		keyID = keyID[:i]
	}

	// An EventBridge rule on the default bus (id = rule name).
	req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{
		"name": {"tagged-rule"}, "pattern": {`{"source":["x"]}`},
	})

	cases := []struct{ svc, id string }{
		{"sqs", "jobs"}, {"ddb", "users"}, {"kms", keyID}, {"eb", "tagged-rule"},
	}
	for _, c := range cases {
		// Set a tag, then it round-trips back through the editor.
		set := req(t, h, "POST", "/_console/tags/set", url.Values{
			"svc": {c.svc}, "id": {c.id}, "key": {"env"}, "value": {"staging"},
		}).Body.String()
		if !strings.Contains(set, "env") || !strings.Contains(set, "staging") {
			t.Fatalf("%s tag not reflected after set:\n%s", c.svc, set)
		}
		// The lazy-load view shows it too.
		view := req(t, h, "GET", "/_console/tags/view?svc="+c.svc+"&id="+url.QueryEscape(c.id), nil).Body.String()
		if !strings.Contains(view, "staging") {
			t.Fatalf("%s tag not shown on reload:\n%s", c.svc, view)
		}
		// Remove it.
		rm := req(t, h, "POST", "/_console/tags/remove", url.Values{
			"svc": {c.svc}, "id": {c.id}, "key": {"env"},
		}).Body.String()
		if strings.Contains(rm, "staging") {
			t.Fatalf("%s tag still present after remove:\n%s", c.svc, rm)
		}
	}
}

// TestSSMPathTree checks that path-addressed parameters group by their parent
// path in the list pane, with leaves labelled by their last segment.
func TestSSMPathTree(t *testing.T) {
	h := newConsole(t)
	for _, name := range []string{"/app/db/host", "/app/db/port", "/app/cache/ttl"} {
		create(t, h, "/_console/ssm/create", url.Values{"name": {name}, "type": {"String"}, "value": {"v"}})
	}
	body := req(t, h, "GET", "/_console/ssm", nil).Body.String()
	// Folder headers for each parent path.
	for _, dir := range []string{"/app/db", "/app/cache"} {
		if !strings.Contains(body, ">"+dir+"</div>") {
			t.Fatalf("list pane missing folder header %q:\n%s", dir, body)
		}
	}
	// Leaves show the last segment, not the full path.
	for _, leaf := range []string{">host<", ">port<", ">ttl<"} {
		if !strings.Contains(body, leaf) {
			t.Fatalf("list pane missing leaf %q", leaf)
		}
	}
}

// TestEBArchivesReplay covers the console archives/replay surface: an archive
// captures a bus's events, and a replay runs to COMPLETED.
func TestEBArchivesReplay(t *testing.T) {
	h := newConsole(t)

	// Archive first, so the subsequent event is captured.
	arc := req(t, h, "POST", "/_console/eb/default/create-archive", url.Values{"name": {"audit"}}).Body.String()
	if !strings.Contains(arc, "audit") {
		t.Fatalf("archive not shown after create:\n%s", arc)
	}
	// Publish a test event onto the bus.
	req(t, h, "POST", "/_console/eb/default/test-event", url.Values{
		"source": {"billing"}, "detail_type": {"Invoiced"}, "detail": {`{"id":"1"}`},
	})
	// The bus page shows the archive with its captured event count.
	bus := req(t, h, "GET", "/_console/eb/default", nil).Body.String()
	if !strings.Contains(bus, "audit") {
		t.Fatalf("archive missing from bus page:\n%s", bus)
	}
	// Replay it — the replay completes synchronously.
	rep := req(t, h, "POST", "/_console/eb/default/replay", url.Values{"name": {"audit"}}).Body.String()
	if !strings.Contains(rep, "COMPLETED") {
		t.Fatalf("replay did not complete:\n%s", rep)
	}
}

// between returns the text between the first open..close pair, or "".
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(s[i : i+j])
}

// TestTrafficRecorder: external calls are captured; console calls are not.
func TestTrafficRecorder(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a Stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	rec := console.NewRecorder(stack.Handler())
	c, err := console.New(console.Options{Gateway: stack.Handler(), Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}

	// An external SDK-style call flows through the recorder.
	r := httptest.NewRequest("POST", "/", strings.NewReader("{}"))
	r.Header.Set("X-Amz-Target", "AmazonSQS.ListQueues")
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	rec.ServeHTTP(httptest.NewRecorder(), r)

	feed := req(t, c, "GET", "/_console/traffic/feed", nil)
	if !strings.Contains(feed.Body.String(), "ListQueues") || !strings.Contains(feed.Body.String(), "sqs") {
		t.Fatalf("traffic feed missing the external call:\n%s", feed.Body)
	}
	// The console's own list call (via its in-process backend, bypassing rec)
	// must NOT appear. Count rendered rows (the action cell), not raw substring
	// hits — each row also embeds the action inside its copy-as-curl payload.
	req(t, c, "GET", "/_console/sqs", nil)
	feed2 := req(t, c, "GET", "/_console/traffic/feed", nil)
	if n := strings.Count(feed2.Body.String(), `<span class="act">ListQueues</span>`); n > 1 {
		t.Fatalf("console's own calls leaked into the traffic tail (%d rows)", n)
	}
}

// TestConsoleCSRFAndObjectHeaders covers the console hardening: cross-origin
// state-changing requests are refused, and S3 objects are served with anti-XSS
// headers (nosniff + forced download for active content types).
func TestConsoleCSRFAndObjectHeaders(t *testing.T) {
	h := newConsole(t)

	// Cross-origin POST is refused.
	r := httptest.NewRequest("POST", "/_console/s3/create", strings.NewReader(url.Values{"name": {"x"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", rec.Code)
	}

	// Same-origin POST (Origin matching Host) is allowed.
	r2 := httptest.NewRequest("POST", "/_console/s3/create", strings.NewReader(url.Values{"name": {"safebucket"}}.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.Header.Set("Origin", "http://"+r2.Host)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("same-origin create = %d, want 303", rec2.Code)
	}

	// Upload an HTML file, then confirm it's served nosniff + attachment.
	multipartUpload(t, h, "/_console/s3/safebucket/upload", "evil.html", "<script>alert(1)</script>", "")
	obj := req(t, h, "GET", "/_console/s3/safebucket/object?key=evil.html", nil)
	if got := obj.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if disp := obj.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment") {
		t.Fatalf("html object served as %q, want attachment", disp)
	}
}
