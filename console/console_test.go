package console_test

import (
	"bytes"
	"html"
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
	if page.Code != 200 || !strings.Contains(page.Body.String(), "_local_") {
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
