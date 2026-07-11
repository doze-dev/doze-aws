package console

import (
	"io"
	"net/http"
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
	svc, action, resource := classify(r)
	sw := &statusWriter{ResponseWriter: w, code: 200}
	start := time.Now()
	rec.next.ServeHTTP(sw, r)
	rec.add(TrafficEntry{
		At: start, Service: svc, Action: action, Resource: resource,
		Status: sw.code, Millis: float64(time.Since(start).Microseconds()) / 1000.0,
		ReqBody: body,
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
	code int
	done bool
}

func (s *statusWriter) WriteHeader(c int) {
	if !s.done {
		s.code = c
		s.done = true
	}
	s.ResponseWriter.WriteHeader(c)
}
func (s *statusWriter) Write(b []byte) (int, error) {
	s.done = true
	return s.ResponseWriter.Write(b)
}

// targetService maps an X-Amz-Target prefix to a service label.
var targetService = map[string]string{
	"TrentService": "kms", "AmazonSSM": "ssm", "secretsmanager": "sm",
	"AWSEvents": "eb", "DynamoDB_20120810": "ddb", "AmazonSQS": "sqs",
}

// classify infers (service, action, resource) from a request, mirroring the
// gateway's own routing heuristics closely enough for a readable tail.
func classify(r *http.Request) (svc, action, resource string) {
	if t := r.Header.Get("X-Amz-Target"); t != "" {
		prefix, act, _ := strings.Cut(t, ".")
		svc = targetService[prefix]
		if svc == "" {
			svc = strings.ToLower(prefix)
		}
		return svc, act, ""
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
	// Query protocol (SNS / STS / legacy SQS): Action in form/query.
	if a := r.URL.Query().Get("Action"); a != "" {
		return querySvc(a), a, ""
	}
	if r.Method == "POST" && strings.Contains(r.Header.Get("Content-Type"), "x-www-form-urlencoded") {
		if err := r.ParseForm(); err == nil {
			if a := r.PostForm.Get("Action"); a != "" {
				return querySvc(a), a, r.PostForm.Get("QueueUrl") + r.PostForm.Get("TopicArn")
			}
		}
	}
	// S3 fallback: path-style /bucket/key.
	p := strings.TrimPrefix(r.URL.Path, "/")
	act := map[string]string{"GET": "GetObject", "PUT": "PutObject", "DELETE": "DeleteObject", "HEAD": "HeadObject", "POST": "PostObject"}[r.Method]
	if act == "" {
		act = r.Method
	}
	if p == "" {
		act = "ListBuckets"
	}
	return "s3", act, p
}

func querySvc(action string) string {
	switch {
	case strings.Contains(action, "Topic") || strings.Contains(action, "Subscri") || action == "Publish":
		return "sns"
	case strings.Contains(action, "Queue") || strings.Contains(action, "Message"):
		return "sqs"
	case strings.Contains(action, "Caller") || strings.Contains(action, "Role") || strings.Contains(action, "Session"):
		return "sts"
	}
	return "aws"
}

// redact blanks obvious secret-bearing fields in a captured body.
func redact(body string) string {
	for _, key := range []string{"SecretString", "Plaintext", "Value", "Password", "password"} {
		body = redactKey(body, key)
	}
	return body
}

func redactKey(body, key string) string {
	// crude but safe: "key":"…"  and  key=…
	for _, pat := range []string{`"` + key + `":"`, key + "="} {
		for {
			i := strings.Index(body, pat)
			if i < 0 {
				break
			}
			start := i + len(pat)
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
			body = body[:start] + "••••••" + body[end:]
		}
	}
	return body
}
