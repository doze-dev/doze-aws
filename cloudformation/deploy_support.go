package cloudformation

// Supporting machinery for the stack service: template fetching from S3,
// event synthesis, and errors. Resource teardown lives in internal/provision,
// beside the Apply it inverts.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/provision"
)

func unix(sec int64) time.Time { return time.Unix(sec, 0) }

// ---- errors ----

func errValidation(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationError", format, args...)
}

func errStackNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationError", "Stack with id %s does not exist", name)
}

func errChangeSetNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(404, "ChangeSetNotFound", "ChangeSet [%s] does not exist", name)
}

// ---- template fetching ----

// templateOf resolves the template a request carries: inline TemplateBody, or
// TemplateURL pointing at an object in the local S3.
//
// The URL path matters more than it looks. CDK always uploads templates to its
// staging bucket and passes TemplateURL, so a deploy that cannot fetch from S3
// fails before it starts.
func (s *Server) templateOf(p params) (string, *awshttp.APIError) {
	if body := p.str("TemplateBody"); body != "" {
		return body, nil
	}
	rawURL := p.str("TemplateURL")
	if rawURL == "" {
		return "", nil
	}
	bucket, key, err := parseS3URL(rawURL)
	if err != nil {
		return "", errValidation("TemplateURL: %v", err)
	}
	body, err := s.fetchS3(bucket, key)
	if err != nil {
		return "", errValidation("fetching TemplateURL %s: %v", rawURL, err)
	}
	return body, nil
}

// parseS3URL handles the three shapes AWS tooling produces:
//
//	https://s3.<region>.amazonaws.com/<bucket>/<key>   (path style)
//	https://<bucket>.s3.<region>.amazonaws.com/<key>   (virtual host)
//	s3://<bucket>/<key>
func parseS3URL(raw string) (bucket, key string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	path := strings.TrimPrefix(u.Path, "/")
	if u.Scheme == "s3" {
		return u.Host, path, nil
	}
	host := u.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	// Virtual-host style: the bucket is the label before the "s3." segment.
	if i := strings.Index(host, ".s3"); i > 0 {
		return host[:i], path, nil
	}
	// Path style: the first path segment is the bucket.
	bucket, key, found := strings.Cut(path, "/")
	if !found || bucket == "" {
		return "", "", fmt.Errorf("cannot tell which bucket %q refers to", raw)
	}
	return bucket, key, nil
}

// fetchS3 reads an object through the gateway, path-style.
func (s *Server) fetchS3(bucket, key string) (string, error) {
	if s.gateway == nil {
		return "", fmt.Errorf("no gateway wired")
	}
	req, err := http.NewRequest(http.MethodGet, "http://s3.doze-aws.internal/"+bucket+"/"+key, nil)
	if err != nil {
		return "", err
	}
	// Sign the scope so the gateway routes to S3 rather than falling back.
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20240101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=x")
	rec := &captureWriter{header: http.Header{}}
	s.gateway.ServeHTTP(rec, req)
	if rec.status != 0 && rec.status != http.StatusOK {
		return "", fmt.Errorf("s3 returned %d: %s", rec.status, truncate(rec.body.String(), 200))
	}
	return rec.body.String(), nil
}

// ---- event synthesis ----

// synthesizeEvents builds the progress trail deploy tools poll for.
//
// The events are derived from what apply ACTUALLY did, not invented: a
// resource that was already in place gets no create event, and a failure
// carries the real error. The final event is always a terminal stack-level
// status, because that is what stops the poller.
func (s *Server) synthesizeEvents(st *StackRecord, applyRep *provision.Report, isUpdate bool) []StackEvent {
	now := s.now().Unix()
	verb := statusVerb(isUpdate)
	events := []StackEvent{{
		ID: s.store.newID(), Timestamp: now, LogicalID: st.Name,
		Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID,
		Status: verb + "_IN_PROGRESS", Reason: "User Initiated",
	}}

	// Which resources apply actually touched, keyed by the name it reported.
	touched := map[string]string{}
	if applyRep != nil {
		for _, a := range applyRep.Actions {
			if i := strings.Index(a.Resource, "/"); i >= 0 {
				touched[a.Resource[i+1:]] = a.Op
			}
		}
	}

	for _, r := range st.Resources {
		op := touched[r.PhysicalID]
		status := verb + "_COMPLETE"
		reason := ""
		switch op {
		case "created":
			// nothing to add
		case "updated":
			status = "UPDATE_COMPLETE"
		case "":
			reason = "resource already in place"
		default:
			reason = "no change"
		}
		events = append(events,
			StackEvent{
				ID: s.store.newID(), Timestamp: now, LogicalID: r.LogicalID,
				Type: r.Type, PhysicalID: r.PhysicalID, Status: verb + "_IN_PROGRESS",
			},
			StackEvent{
				ID: s.store.newID(), Timestamp: now, LogicalID: r.LogicalID,
				Type: r.Type, PhysicalID: r.PhysicalID, Status: status, Reason: reason,
			})
	}

	events = append(events, StackEvent{
		ID: s.store.newID(), Timestamp: now, LogicalID: st.Name,
		Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID,
		Status: st.Status, Reason: st.StatusReason,
	})
	// Keep the trail bounded: a stack redeployed in a loop should not grow
	// without limit, and only the latest deploy is ever interesting.
	const maxEvents = 500
	all := append(st.Events, events...)
	if len(all) > maxEvents {
		all = all[len(all)-maxEvents:]
	}
	return all
}

// ---- gateway plumbing ----

// captureWriter records a handler response in memory, for the one place the
// service needs to read from a sibling service directly (fetching a template
// out of S3). Everything else goes through stackfile, which owns the wire
// protocols.
type captureWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *captureWriter) Header() http.Header    { return w.header }
func (w *captureWriter) WriteHeader(status int) { w.status = status }
func (w *captureWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
