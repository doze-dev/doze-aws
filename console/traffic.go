package console

import (
	"encoding/json"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Recorder is gateway middleware that keeps a ring buffer of the most recent
// AWS API calls an external SDK/CLI made against the endpoint. The console's own
// in-process client bypasses it (it talks to the raw gateway), so the tail only
// shows the app's traffic — the answer to "what did my app just do?".
type Recorder struct {
	next http.Handler
	mu   sync.Mutex
	buf  []TrafficEntry
	head int
	seq  int64
	full bool
}

// TrafficEntry is one recorded call.
type TrafficEntry struct {
	Seq      int64
	At       time.Time
	Service  string
	Action   string
	Resource string
	Status   int
	Millis   float64
	ReqBody  string // captured for JSON/query bodies (bounded); redacted
	RespBody string // first 8KB of a text response; redacted
	RespCT   string // response Content-Type
	Method   string
	Path     string // path + query, for replay
	Host     string
	CT       string // Content-Type
	Target   string // X-Amz-Target
}

// Curl renders the entry as a replayable curl command. The body is the
// recorder's redacted copy, so masked secrets stay masked in the repro.
func (e TrafficEntry) Curl() string {
	var b strings.Builder
	b.WriteString("curl -X " + e.Method + " 'http://" + e.Host + e.Path + "'")
	if e.CT != "" {
		b.WriteString(" \\\n  -H 'Content-Type: " + e.CT + "'")
	}
	if e.Target != "" {
		b.WriteString(" \\\n  -H 'X-Amz-Target: " + e.Target + "'")
	}
	if e.ReqBody != "" {
		b.WriteString(" \\\n  --data '" + strings.ReplaceAll(e.ReqBody, "'", `'\''`) + "'")
	}
	return b.String()
}

const trafficCap = 500

// NewRecorder wraps the AWS gateway handler with traffic capture.
func NewRecorder(next http.Handler) *Recorder {
	return &Recorder{next: next, buf: make([]TrafficEntry, trafficCap)}
}

func (rec *Recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Capture a bounded, redacted copy of small text bodies for the detail view.
	var body string
	if r.Body != nil && r.ContentLength > 0 && r.ContentLength < 8192 {
		if b, err := io.ReadAll(io.LimitReader(r.Body, 8192)); err == nil {
			body = redact(string(b))
			r.Body = io.NopCloser(strings.NewReader(string(b)))
		}
	}
	svc, action, resource := classify(r, body)
	sw := &statusWriter{ResponseWriter: w, code: 200}
	start := time.Now()
	rec.next.ServeHTTP(sw, r)
	rec.add(TrafficEntry{
		At: start, Service: svc, Action: action, Resource: resource,
		Status: sw.code, Millis: float64(time.Since(start).Microseconds()) / 1000.0,
		ReqBody:  body,
		RespBody: redact(sw.captured.String()), RespCT: sw.Header().Get("Content-Type"),
		Method: r.Method, Path: r.URL.RequestURI(), Host: r.Host,
		CT: r.Header.Get("Content-Type"), Target: r.Header.Get("X-Amz-Target"),
	})
}

func (rec *Recorder) add(e TrafficEntry) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seq++
	e.Seq = rec.seq
	rec.buf[rec.head] = e
	rec.head = (rec.head + 1) % trafficCap
	if rec.head == 0 {
		rec.full = true
	}
}

// LastSeq returns the sequence number of the newest recorded call — the cheap
// "anything new?" probe the live poll checks before rendering rows.
func (rec *Recorder) LastSeq() int64 {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.seq
}

// Get returns the entry with the given sequence number, if it is still in the
// ring (the inspector drawer loads entries by seq).
func (rec *Recorder) Get(seq int64) (TrafficEntry, bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := rec.head
	if rec.full {
		n = trafficCap
	}
	for i := 0; i < n; i++ {
		e := rec.buf[(rec.head-1-i+trafficCap)%trafficCap]
		if e.Seq == seq {
			return e, true
		}
		if e.Seq < seq {
			break
		}
	}
	return TrafficEntry{}, false
}

// Clear empties the ring. Seq keeps counting up so live-poll hashes stay
// monotonic across a clear.
func (rec *Recorder) Clear() {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.buf = make([]TrafficEntry, trafficCap)
	rec.head = 0
	rec.full = false
	rec.seq++ // the tail visibly changed; move the hash
}

// Entries returns recorded calls, newest first, with Seq > sinceSeq.
func (rec *Recorder) Entries(sinceSeq int64) []TrafficEntry {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := rec.head
	if rec.full {
		n = trafficCap
	}
	out := make([]TrafficEntry, 0, n)
	for i := 0; i < n; i++ {
		idx := (rec.head - 1 - i + trafficCap) % trafficCap
		e := rec.buf[idx]
		if e.Seq <= sinceSeq {
			break
		}
		out = append(out, e)
	}
	return out
}

type statusWriter struct {
	http.ResponseWriter
	code     int
	done     bool
	captured strings.Builder // first respCap bytes of a text response, for the inspector
}

const respCap = 8192

func (s *statusWriter) WriteHeader(c int) {
	if !s.done {
		s.code = c
		s.done = true
	}
	s.ResponseWriter.WriteHeader(c)
}
func (s *statusWriter) Write(b []byte) (int, error) {
	s.done = true
	if s.captured.Len() < respCap && textualCT(s.Header().Get("Content-Type")) {
		room := respCap - s.captured.Len()
		if len(b) <= room {
			s.captured.Write(b)
		} else {
			s.captured.Write(b[:room])
			s.captured.WriteString("…")
		}
	}
	return s.ResponseWriter.Write(b)
}

// textualCT reports whether a response body is worth capturing for display —
// object downloads and other binary payloads are not.
func textualCT(ct string) bool {
	return strings.Contains(ct, "json") || strings.Contains(ct, "xml") ||
		strings.Contains(ct, "text") || strings.Contains(ct, "x-www-form-urlencoded")
}

// consoleLabel maps a gateway service name onto the short label the console
// uses in its nav and traffic tail.
var consoleLabel = map[string]string{
	"secretsmanager": "sm",
	"eventbridge":    "eb",
	"dynamodb":       "ddb",
	"cloudformation": "cfn",
	"apigateway":     "apigw",
}

// labelFor resolves a request to the console's service label using the
// GATEWAY's own routing, not a private copy of it.
//
// This used to be a separate set of heuristics, and it drifted: IAM traffic was
// reported as STS (because the sts rule matched "Role" in CreateRole), and API
// Gateway traffic as S3 (because /restapis fell through to the path-style
// catch-all). A debugging tool that misattributes requests is worse than one
// that shows nothing, so the classification now has a single source of truth.
func labelFor(r *http.Request) string {
	svc := gateway.Route(r)
	if short, ok := consoleLabel[svc]; ok {
		return short
	}
	return svc
}

// classify infers (service, action, resource) from a request. The SERVICE comes
// from the gateway; only the action and resource are console-specific.
func classify(r *http.Request, capturedBody string) (svc, action, resource string) {
	svc = labelFor(r)

	if t := r.Header.Get("X-Amz-Target"); t != "" {
		_, act, _ := strings.Cut(t, ".")
		return svc, act, jsonResource(svc, capturedBody)
	}
	// API Gateway: the control plane is path-routed, and a call to a DEPLOYED
	// api is the one shape worth naming precisely, since that is what a
	// developer is usually watching for.
	if svc == "apigw" {
		return svc, apigwAction(r), apigwResource(r)
	}
	// Lambda REST paths.
	if strings.HasPrefix(r.URL.Path, "/2015-03-31/") {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		act := "Invoke"
		res := ""
		if len(parts) >= 3 && parts[1] == "functions" {
			res = parts[2]
			if r.Method == "DELETE" {
				act = "DeleteFunction"
			} else if r.Method == "GET" {
				act = "GetFunction"
			} else if len(parts) >= 4 && parts[3] == "invocations" {
				act = "Invoke"
			} else if r.Method == "POST" {
				act = "CreateFunction"
			}
		} else if len(parts) >= 2 && parts[1] == "functions" {
			act = "ListFunctions"
		}
		return "lambda", act, res
	}
	// Query protocol (SNS / STS / legacy SQS): Action in query or form body.
	// The form body is parsed from the recorder's captured copy — NEVER via
	// r.ParseForm, which would consume r.Body and starve the gateway's own
	// body-Action routing (SigV2-era clients put Action in the body).
	if a := r.URL.Query().Get("Action"); a != "" {
		return svc, a, ""
	}
	if r.Method == "POST" && strings.Contains(r.Header.Get("Content-Type"), "x-www-form-urlencoded") && capturedBody != "" {
		if vals, err := url.ParseQuery(capturedBody); err == nil {
			if a := vals.Get("Action"); a != "" {
				return svc, a, queryResource(svc, vals)
			}
		}
	}
	// S3: path-style /bucket/key. Only reached when the gateway itself routed
	// here, so an unrecognised path can no longer masquerade as an object GET.
	p := strings.TrimPrefix(r.URL.Path, "/")
	act := map[string]string{"GET": "GetObject", "PUT": "PutObject", "DELETE": "DeleteObject", "HEAD": "HeadObject", "POST": "PostObject"}[r.Method]
	if act == "" {
		act = r.Method
	}
	if p == "" {
		act = "ListBuckets"
	}
	return svc, act, p
}

// apigwAction names an API Gateway request: the control-plane operation, or an
// invocation of a deployed API.
func apigwAction(r *http.Request) string {
	if apiID, stage, _, ok := executePath(r); ok {
		_, _ = apiID, stage
		return "Invoke " + r.Method
	}
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return r.Method
	}
	// /restapis/{id}/resources/{id}/methods/{verb}/integration → "integration"
	noun := segs[len(segs)-1]
	verb := map[string]string{
		"POST": "Create", "PUT": "Put", "GET": "Get", "DELETE": "Delete", "PATCH": "Update",
	}[r.Method]
	if verb == "" {
		verb = r.Method
	}
	return verb + " " + noun
}

// apigwResource names what an API Gateway request addressed: the api id, or
// the invoked path on a deployed api.
func apigwResource(r *http.Request) string {
	if apiID, stage, path, ok := executePath(r); ok {
		return apiID + "/" + stage + path
	}
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segs) >= 2 && segs[0] == "restapis" {
		return segs[1]
	}
	return ""
}

// executePath splits a call to a deployed API, in either addressing form.
func executePath(r *http.Request) (apiID, stage, path string, ok bool) {
	if rest, found := strings.CutPrefix(r.URL.Path, "/_aws/execute-api/"); found {
		apiID, remainder, _ := strings.Cut(rest, "/")
		stage, tail, _ := strings.Cut(remainder, "/")
		return apiID, stage, "/" + tail, apiID != ""
	}
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if label, _, found := strings.Cut(host, ".execute-api."); found && label != "" {
		stage, tail, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		return label, stage, "/" + tail, true
	}
	return "", "", "", false
}

// queryResource pulls the resource name out of a Query-protocol form.
func queryResource(svc string, vals url.Values) string {
	switch svc {
	case "iam":
		for _, k := range []string{"UserName", "RoleName", "GroupName", "PolicyName", "PolicyArn"} {
			if v := vals.Get(k); v != "" {
				return leafName(v)
			}
		}
	case "cfn":
		if v := vals.Get("StackName"); v != "" {
			return leafName(v)
		}
		return vals.Get("ChangeSetName")
	}
	return leafName(vals.Get("QueueUrl") + vals.Get("TopicArn"))
}

// jsonResource pulls the resource name out of a JSON-protocol request body —
// without this, every SendMessage/PutItem row shows an empty resource column.
func jsonResource(svc, body string) string {
	if body == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return ""
	}
	str := func(k string) string {
		s, _ := m[k].(string)
		return s
	}
	switch svc {
	case "sqs":
		if u := str("QueueUrl"); u != "" {
			return leafName(u)
		}
		return str("QueueName") // JSON-protocol clients may address by name
	case "ddb":
		return str("TableName")
	case "kms":
		return leafName(str("KeyId"))
	case "ssm":
		return str("Name")
	case "sm":
		return leafName(str("SecretId"))
	case "kinesis":
		if n := str("StreamName"); n != "" {
			return n
		}
		return leafName(str("StreamARN"))
	case "eb":
		if n := str("Name"); n != "" {
			return n
		}
		if n := str("EventBusName"); n != "" {
			return n
		}
		if n := str("Rule"); n != "" {
			return n
		}
	}
	return ""
}

// leafName trims a URL or ARN down to its final path/name segment.
func leafName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && strings.HasPrefix(s, "arn:") {
		s = s[i+1:]
	}
	return s
}

// redact blanks obvious secret-bearing fields in a captured body.
func redact(body string) string {
	for _, key := range []string{"SecretString", "Plaintext", "Value", "Password", "password"} {
		body = redactKey(body, key)
	}
	return body
}

func redactKey(body, key string) string {
	const mask = "••••••"
	// crude but safe: "key":"…"  and  key=…
	for _, pat := range []string{`"` + key + `":"`, key + "="} {
		// Resume each search after the previous replacement — the pattern
		// itself survives the replacement, so restarting from 0 would refind
		// it forever (a masked value re-masks to itself: infinite loop).
		from := 0
		for {
			i := strings.Index(body[from:], pat)
			if i < 0 {
				break
			}
			start := from + i + len(pat)
			end := start
			if strings.HasSuffix(pat, `"`) {
				for end < len(body) && body[end] != '"' {
					end++
				}
			} else {
				for end < len(body) && body[end] != '&' {
					end++
				}
			}
			body = body[:start] + mask + body[end:]
			from = start + len(mask)
		}
	}
	return body
}
