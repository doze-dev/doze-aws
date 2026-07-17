package console

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/peers"
)

// backend is the console's view of the stack: it is just another client of the
// same AWS gateway, dispatched in-process (no network, no signing — doze-aws
// parses signatures but never verifies them). This keeps the console honest —
// it exercises exactly the API real SDK users hit.
type backend struct {
	c    *http.Client
	base string

	// graphMu guards a short-lived cache of the full wiring graph. BuildGraph
	// fans out a crawl over every service; the flows poll and every resource
	// detail page's Neighbors() call would otherwise rebuild it on each
	// request. The TTL must outlive the fastest poller (flows polls at 4s) or
	// the cache never serves a poll and every tick pays the full crawl.
	// Console-driven mutations call bustGraph() so their next read is fresh;
	// changes made by the user's own app show up within a TTL.
	graphMu    sync.Mutex
	graphAt    time.Time
	graphSnap  *FlowGraph
	graphBuild chan struct{} // non-nil while one goroutine crawls (singleflight)
}

const graphTTL = 5 * time.Second

func newBackend(dir peers.Directory) *backend {
	return &backend{
		c:    &http.Client{Transport: fanoutTransport{dir}, Timeout: 30 * time.Second},
		base: "http://console.doze-aws.internal",
	}
}

// graphCached returns a recent wiring graph, rebuilding only when the cached
// one has aged past graphTTL. Concurrent callers during a rebuild wait for the
// in-flight crawl instead of piling on their own (singleflight).
func (b *backend) graphCached(ctx context.Context) FlowGraph {
	for {
		b.graphMu.Lock()
		if b.graphSnap != nil && time.Since(b.graphAt) < graphTTL {
			g := *b.graphSnap
			b.graphMu.Unlock()
			return g
		}
		if ch := b.graphBuild; ch != nil {
			b.graphMu.Unlock()
			<-ch // a crawl is in flight — take its result on the next loop
			continue
		}
		ch := make(chan struct{})
		b.graphBuild = ch
		b.graphMu.Unlock()

		g := b.BuildGraph(ctx)

		b.graphMu.Lock()
		b.graphSnap = &g
		b.graphAt = time.Now()
		b.graphBuild = nil
		b.graphMu.Unlock()
		close(ch)
		return g
	}
}

// bustGraph drops the cached wiring graph. Every console-driven mutation that
// can change topology or the node captions (queue depths, sub counts) calls
// this so the user's own action is visible on the very next poll.
func (b *backend) bustGraph() {
	b.graphMu.Lock()
	b.graphSnap = nil
	b.graphMu.Unlock()
}

// fanoutTransport routes each console request to the AWS service that owns it and
// dials that service's endpoint — an in-process handler when embedded, or a
// per-service unix socket in the module topology. Routing uses gateway.Route so
// the console picks exactly the service the real gateway would (one source of
// truth), which is what lets the same console front both topologies unchanged.
type fanoutTransport struct {
	dir peers.Directory
}

func (t fanoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body once: routing may peek a Query-protocol Action, and the
	// outbound request needs the same bytes.
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	svc := gateway.Route(req)
	ep, ok := t.dir.Endpoint(svc)
	if !ok {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"__type":"ServiceUnavailable","message":"the ` + svc + ` service is not running"}`)),
			Request:    req,
		}, nil
	}
	out, err := http.NewRequestWithContext(req.Context(), req.Method, ep.URL(req.URL.RequestURI()), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	return ep.Client.Do(out)
}

// apiErr carries a non-2xx wire response back to the handlers.
type apiErr struct {
	status int
	body   string
}

func (e *apiErr) Error() string { return fmt.Sprintf("aws %d: %s", e.status, e.body) }

// ---- S3 ----

type Bucket struct {
	Name    string
	Created string
}

type Object struct {
	Key          string
	Size         int64
	LastModified string
	IsPrefix     bool // a "folder" (CommonPrefix) rather than a real object
}

func (b *backend) do(req *http.Request) ([]byte, error) {
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, &apiErr{status: resp.StatusCode, body: string(body)}
	}
	if graphMutation(req) {
		b.bustGraph()
	}
	return body, nil
}

// graphReadActions are wire-action prefixes that never change what the flows
// graph shows. Everything else on a graph-relevant service busts the cache.
var graphReadActions = []string{
	"List", "Get", "Describe", "Test", "Receive", "ChangeMessageVisibility", "DozePeek",
}

// graphMutation reports whether a successful console-client request could have
// changed the wiring graph (topology, queue depths, sub counts). Services the
// graph doesn't draw (DynamoDB, KMS, SSM, Secrets Manager) never count.
// Query-protocol requests (form-encoded Action bodies) are handled by
// queryXML, which sees the Action before it is encoded.
func graphMutation(req *http.Request) bool {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return false
	}
	if t := req.Header.Get("X-Amz-Target"); t != "" {
		prefix, action, _ := strings.Cut(t, ".")
		switch prefix {
		case "AmazonSQS", "AWSEvents":
			return !readAction(action)
		default: // ddb / kms / ssm / sm — not drawn in the graph
			return false
		}
	}
	if strings.Contains(req.Header.Get("Content-Type"), "x-www-form-urlencoded") {
		return false // query protocol: queryXML busts with the Action in hand
	}
	// Bare REST writes: S3 (PUT/DELETE/POST objects, buckets, notifications)
	// and Lambda (create/delete/config/mappings/invoke — invokes can land
	// messages on destination queues).
	return true
}

func readAction(action string) bool {
	for _, p := range graphReadActions {
		if strings.HasPrefix(action, p) {
			return true
		}
	}
	return false
}

func (b *backend) ListBuckets(ctx context.Context) ([]Bucket, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/", nil)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Buckets struct {
			Bucket []struct {
				Name         string `xml:"Name"`
				CreationDate string `xml:"CreationDate"`
			} `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	buckets := make([]Bucket, 0, len(out.Buckets.Bucket))
	for _, bk := range out.Buckets.Bucket {
		buckets = append(buckets, Bucket{Name: bk.Name, Created: shortTime(bk.CreationDate)})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	return buckets, nil
}

func (b *backend) CreateBucket(ctx context.Context, name string) error {
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+url.PathEscape(name), nil)
	_, err := b.do(req)
	return err
}

func (b *backend) DeleteBucket(ctx context.Context, name string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", b.base+"/"+url.PathEscape(name), nil)
	_, err := b.do(req)
	return err
}

// ListObjects browses one "directory" level using the delimiter, so the UI can
// present folders like a filesystem instead of a flat key dump.
func (b *backend) ListObjects(ctx context.Context, bucket, prefix string) ([]Object, error) {
	q := url.Values{"list-type": {"2"}, "delimiter": {"/"}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/"+bucket+"?"+q.Encode(), nil)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			LastModified string `xml:"LastModified"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	objs := make([]Object, 0, len(out.Contents)+len(out.CommonPrefixes))
	for _, p := range out.CommonPrefixes {
		objs = append(objs, Object{Key: p.Prefix, IsPrefix: true})
	}
	for _, o := range out.Contents {
		if o.Key == prefix { // the folder marker itself
			continue
		}
		objs = append(objs, Object{Key: o.Key, Size: o.Size, LastModified: shortTime(o.LastModified)})
	}
	return objs, nil
}

// bucketTreeTotals counts every object under prefix (no delimiter — the whole
// subtree), for the footer's corrective totals when folders hide the count.
func (b *backend) bucketTreeTotals(ctx context.Context, bucket, prefix string) (int, int64) {
	q := url.Values{"list-type": {"2"}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/"+bucket+"?"+q.Encode(), nil)
	body, err := b.do(req)
	if err != nil {
		return 0, 0
	}
	var out struct {
		Contents []struct {
			Key  string `xml:"Key"`
			Size int64  `xml:"Size"`
		} `xml:"Contents"`
	}
	if xml.Unmarshal(body, &out) != nil {
		return 0, 0
	}
	count, total := 0, int64(0)
	for _, o := range out.Contents {
		if strings.HasSuffix(o.Key, "/") && o.Size == 0 {
			continue // folder markers
		}
		count++
		total += o.Size
	}
	return count, total
}

// GetObject returns the object body and its content type, for preview/download.
func (b *backend) GetObject(ctx context.Context, bucket, key string) ([]byte, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/"+bucket+"/"+escapeKey(key), nil)
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, "", &apiErr{status: resp.StatusCode, body: string(body)}
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// ObjectMeta is the metadata surfaced in the object detail drawer.
type ObjectMeta struct {
	Key          string
	Size         string
	SizeBytes    int64
	ContentType  string
	ETag         string
	LastModified string
	StorageClass string
	IsImage      bool
	IsText       bool
}

// HeadObject fetches an object's metadata without its body (S3 HeadObject).
func (b *backend) HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error) {
	req, _ := http.NewRequestWithContext(ctx, "HEAD", b.base+"/"+bucket+"/"+escapeKey(key), nil)
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &apiErr{status: resp.StatusCode, body: "object not found"}
	}
	ct := resp.Header.Get("Content-Type")
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	sc := resp.Header.Get("x-amz-storage-class")
	if sc == "" {
		sc = "STANDARD"
	}
	return &ObjectMeta{
		Key:          key,
		SizeBytes:    size,
		ContentType:  ct,
		ETag:         strings.Trim(resp.Header.Get("ETag"), `"`),
		LastModified: httpDate(resp.Header.Get("Last-Modified")),
		StorageClass: sc,
		IsImage:      strings.HasPrefix(ct, "image/"),
		IsText:       strings.HasPrefix(ct, "text/") || ct == "application/json",
	}, nil
}

func (b *backend) PutObject(ctx context.Context, bucket, key string, body []byte, contentType string) error {
	req, _ := http.NewRequestWithContext(ctx, "PUT", b.base+"/"+bucket+"/"+escapeKey(key), bytes.NewReader(body))
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	_, err := b.do(req)
	return err
}

func (b *backend) DeleteObject(ctx context.Context, bucket, key string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", b.base+"/"+bucket+"/"+escapeKey(key), nil)
	_, err := b.do(req)
	return err
}

// ---- SQS (JSON 1.0 protocol) ----

type Queue struct {
	Name      string
	Available int    // ApproximateNumberOfMessages
	InFlight  int    // ApproximateNumberOfMessagesNotVisible
	DLQ       string // redrive target queue name, "" when none (from the same attrs fetch)
}

type SQSMessage struct {
	MessageID     string
	ReceiptHandle string
	Body          string
	SentAt        string
	GroupID       string // FIFO: MessageGroupId
	DedupID       string // FIFO: MessageDeduplicationId
	SeqNo         string // FIFO: SequenceNumber
	Receives      string // ApproximateReceiveCount (real receives; peeks don't count)
	Attrs         []MsgAttr
	Sum           MsgSummary // display form, filled by the handler before render
}

// MsgAttr is one user message attribute (metadata alongside the body).
type MsgAttr struct{ Name, Type, Value string }

func (b *backend) sqs(ctx context.Context, action string, in any) ([]byte, error) {
	buf, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	return b.do(req)
}

func (b *backend) queueURL(name string) string {
	return b.base + "/" + awsident.AccountID + "/" + name
}

func (b *backend) ListQueues(ctx context.Context) ([]Queue, error) {
	body, err := b.sqs(ctx, "ListQueues", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	json.Unmarshal(body, &out)
	queues := make([]Queue, 0, len(out.QueueUrls))
	for _, u := range out.QueueUrls {
		name := u[strings.LastIndex(u, "/")+1:]
		q := Queue{Name: name}
		if attrs, err := b.queueAttrs(ctx, name); err == nil {
			q.Available = atoi(attrs["ApproximateNumberOfMessages"])
			q.InFlight = atoi(attrs["ApproximateNumberOfMessagesNotVisible"])
			q.DLQ = sqsConfigOf(attrs).DLQ // BuildGraph reads redrive edges from here
		}
		queues = append(queues, q)
	}
	sort.Slice(queues, func(i, j int) bool { return queues[i].Name < queues[j].Name })
	return queues, nil
}

// CountQueues is the cheap cardinality probe: one ListQueues call, no per-queue
// attribute fetches. The rail's counts poll must not pay the N+1 price.
func (b *backend) CountQueues(ctx context.Context) (int, error) {
	body, err := b.sqs(ctx, "ListQueues", map[string]any{})
	if err != nil {
		return 0, err
	}
	var out struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	json.Unmarshal(body, &out)
	return len(out.QueueUrls), nil
}

func (b *backend) queueAttrs(ctx context.Context, name string) (map[string]string, error) {
	body, err := b.sqs(ctx, "GetQueueAttributes", map[string]any{
		"QueueUrl": b.queueURL(name), "AttributeNames": []string{"All"},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Attributes map[string]string `json:"Attributes"`
	}
	json.Unmarshal(body, &out)
	return out.Attributes, nil
}

// QueueDetail returns attributes plus a non-destructive peek at the visible
// messages — this is the console's edge over the real AWS console, whose
// "poll for messages" actually receives them (bumping the receive count and
// hiding them). DozePeek reads without consuming.
func (b *backend) QueueDetail(ctx context.Context, name string) (map[string]string, []SQSMessage, error) {
	attrs, err := b.queueAttrs(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	body, err := b.sqs(ctx, "DozePeek", map[string]any{
		"QueueUrl": b.queueURL(name), "MaxNumberOfMessages": 10,
		"AttributeNames": []string{"All"}, "MessageAttributeNames": []string{"All"},
	})
	if err != nil {
		return attrs, nil, err
	}
	var out struct {
		Messages []struct {
			MessageID     string            `json:"MessageId"`
			ReceiptHandle string            `json:"ReceiptHandle"`
			Body          string            `json:"Body"`
			Attributes    map[string]string `json:"Attributes"`
			MessageAttrs  map[string]struct {
				DataType    string `json:"DataType"`
				StringValue string `json:"StringValue"`
			} `json:"MessageAttributes"`
		} `json:"Messages"`
	}
	json.Unmarshal(body, &out)
	msgs := make([]SQSMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		msg := SQSMessage{
			MessageID: m.MessageID, ReceiptHandle: m.ReceiptHandle, Body: m.Body,
			SentAt:   epochMillisToTime(m.Attributes["SentTimestamp"]),
			GroupID:  m.Attributes["MessageGroupId"],
			DedupID:  m.Attributes["MessageDeduplicationId"],
			SeqNo:    m.Attributes["SequenceNumber"],
			Receives: m.Attributes["ApproximateReceiveCount"],
		}
		names := make([]string, 0, len(m.MessageAttrs))
		for n := range m.MessageAttrs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			a := m.MessageAttrs[n]
			msg.Attrs = append(msg.Attrs, MsgAttr{Name: n, Type: a.DataType, Value: a.StringValue})
		}
		msgs = append(msgs, msg)
	}
	return attrs, msgs, nil
}

func (b *backend) CreateQueue(ctx context.Context, name string) error {
	_, err := b.sqs(ctx, "CreateQueue", map[string]any{"QueueName": name})
	return err
}

func (b *backend) DeleteQueue(ctx context.Context, name string) error {
	_, err := b.sqs(ctx, "DeleteQueue", map[string]any{"QueueUrl": b.queueURL(name)})
	return err
}

// SendOpts carries everything a message can be published with.
type SendOpts struct {
	GroupID string    // FIFO: required group
	DedupID string    // FIFO: explicit dedup ID; blank auto-generates one
	Delay   string    // standard queues: per-message DelaySeconds
	Attrs   []MsgAttr // message attributes (metadata)
}

func (b *backend) SendMessage(ctx context.Context, name, body string, o SendOpts) error {
	in := map[string]any{"QueueUrl": b.queueURL(name), "MessageBody": body}
	if o.GroupID != "" {
		in["MessageGroupId"] = o.GroupID
		dedup := o.DedupID
		if dedup == "" { // keep repeat sends distinct even without content-based dedup
			dedup = fmt.Sprintf("console-%d", time.Now().UnixNano())
		}
		in["MessageDeduplicationId"] = dedup
	}
	if o.Delay != "" && o.Delay != "0" {
		in["DelaySeconds"] = atoi(o.Delay)
	}
	if len(o.Attrs) > 0 {
		mattrs := map[string]any{}
		for _, a := range o.Attrs {
			if a.Name == "" {
				continue
			}
			t := a.Type
			if t == "" {
				t = "String"
			}
			if t == "Binary" { // value is base64 on the wire
				mattrs[a.Name] = map[string]string{"DataType": t, "BinaryValue": a.Value}
			} else {
				mattrs[a.Name] = map[string]string{"DataType": t, "StringValue": a.Value}
			}
		}
		in["MessageAttributes"] = mattrs
	}
	_, err := b.sqs(ctx, "SendMessage", in)
	return err
}

// DeleteMessage removes one message by receipt handle (peek handles are
// valid — the handle encodes the message's sequence key directly).
func (b *backend) DeleteMessage(ctx context.Context, name, handle string) error {
	_, err := b.sqs(ctx, "DeleteMessage", map[string]any{
		"QueueUrl": b.queueURL(name), "ReceiptHandle": handle,
	})
	return err
}

// DLQSources names the queues whose redrive policies point at this queue —
// the redrive destinations offered when moving messages back.
func (b *backend) DLQSources(ctx context.Context, name string) []string {
	body, err := b.sqs(ctx, "ListDeadLetterSourceQueues", map[string]any{"QueueUrl": b.queueURL(name)})
	if err != nil {
		return nil
	}
	var out struct {
		QueueUrls []string `json:"queueUrls"`
	}
	json.Unmarshal(body, &out)
	if len(out.QueueUrls) == 0 { // some shapes capitalize differently
		var alt struct {
			QueueUrls []string `json:"QueueUrls"`
		}
		json.Unmarshal(body, &alt)
		out.QueueUrls = alt.QueueUrls
	}
	names := make([]string, 0, len(out.QueueUrls))
	for _, u := range out.QueueUrls {
		names = append(names, u[strings.LastIndex(u, "/")+1:])
	}
	sort.Strings(names)
	return names
}

// StartRedrive moves every message from a DLQ back to dest.
func (b *backend) StartRedrive(ctx context.Context, from, dest string) error {
	_, err := b.sqs(ctx, "StartMessageMoveTask", map[string]any{
		"SourceArn":      awsident.ARN("sqs", from),
		"DestinationArn": awsident.ARN("sqs", dest),
	})
	return err
}

// MoveTask is one redrive task's progress.
type MoveTask struct {
	Status  string // RUNNING | COMPLETED | FAILED | CANCELLED
	Dest    string
	Moved   int64
	Failure string
}

func (b *backend) MoveTasks(ctx context.Context, name string) []MoveTask {
	body, err := b.sqs(ctx, "ListMessageMoveTasks", map[string]any{
		"SourceArn": awsident.ARN("sqs", name), "MaxResults": 5,
	})
	if err != nil {
		return nil
	}
	var out struct {
		Results []struct {
			Status                           string `json:"Status"`
			DestinationArn                   string `json:"DestinationArn"`
			ApproximateNumberOfMessagesMoved int64  `json:"ApproximateNumberOfMessagesMoved"`
			FailureReason                    string `json:"FailureReason"`
		} `json:"Results"`
	}
	json.Unmarshal(body, &out)
	tasks := make([]MoveTask, 0, len(out.Results))
	for _, r := range out.Results {
		tasks = append(tasks, MoveTask{
			Status: r.Status, Dest: arnLeaf(r.DestinationArn),
			Moved: r.ApproximateNumberOfMessagesMoved, Failure: r.FailureReason,
		})
	}
	return tasks
}

func (b *backend) PurgeQueue(ctx context.Context, name string) error {
	_, err := b.sqs(ctx, "PurgeQueue", map[string]any{"QueueUrl": b.queueURL(name)})
	return err
}

// ---- generic wire-protocol helpers ----

// json11 posts an AWS JSON 1.1 request (X-Amz-Target routed). prefix is the
// service's target prefix (TrentService, AmazonSSM, secretsmanager, AWSEvents).
func (b *backend) json11(ctx context.Context, prefix, action string, in any) ([]byte, error) {
	buf, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", prefix+"."+action)
	return b.do(req)
}

// ddbCall posts a DynamoDB JSON 1.0 request.
func (b *backend) ddbCall(ctx context.Context, action string, in any) ([]byte, error) {
	buf, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+action)
	return b.do(req)
}

// queryXML posts a legacy Query-protocol request (SNS/STS) and returns the XML.
func (b *backend) queryXML(ctx context.Context, v url.Values) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := b.do(req)
	if err == nil && !readAction(v.Get("Action")) {
		b.bustGraph() // SNS topology/publishes change the graph
	}
	return body, err
}

// ---- helpers ----

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func shortTime(s string) string {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
	}
	return s
}

func httpDate(s string) string {
	if t, err := time.Parse(http.TimeFormat, s); err == nil {
		return t.Local().Format("2006-01-02 15:04:05")
	}
	return s
}

func epochMillisToTime(s string) string {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}
