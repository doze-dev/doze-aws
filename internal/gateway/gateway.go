// Package gateway routes requests arriving on doze-aws's single shared
// endpoint to the right service, the way AWS SDKs of every generation address
// a custom endpoint. A request identifies its service by (tried in order):
//
//  1. the X-Amz-Target header's prefix (all JSON-protocol services);
//  2. the SigV4 credential scope's service name (header or presigned query) —
//     SigV2 signatures carry no service name and fall through;
//  3. a Lambda REST-API path prefix (/2015-03-31/..., etc.);
//  4. the Query-protocol Action parameter, looked up in a static action table
//     (distinguishes STS from SNS for unsigned/SigV2 requests);
//  5. otherwise S3 — the host/path shapes S3 clients produce are too varied to
//     enumerate, so S3 is the fallback, matching LocalStack ergonomics.
//
// The gateway is routing only: it never interprets a request beyond picking a
// handler, and it restores any body bytes it had to peek at.
package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/sigparse"
)

// Services is the canonical set of doze-aws service names, in the order they
// appear in docs and config listings.
var Services = []string{
	"s3", "sqs", "sns", "sts", "dynamodb", "kms", "ssm", "secretsmanager", "eventbridge", "lambda",
}

// KnownService reports whether name is one of the canonical service names.
func KnownService(name string) bool {
	return slices.Contains(Services, name)
}

// targetPrefixes maps X-Amz-Target prefixes to services.
var targetPrefixes = map[string]string{
	"AmazonSQS":                "sqs",
	"DynamoDB_20120810":        "dynamodb",
	"DynamoDBStreams_20120810": "dynamodb",
	"TrentService":             "kms",
	"AmazonSSM":                "ssm",
	"secretsmanager":           "secretsmanager",
	"AWSEvents":                "eventbridge",
}

// scopeServices maps SigV4 signing names to services (they mostly coincide;
// EventBridge signs as "events").
var scopeServices = map[string]string{
	"s3":             "s3",
	"sqs":            "sqs",
	"sns":            "sns",
	"sts":            "sts",
	"dynamodb":       "dynamodb",
	"kms":            "kms",
	"ssm":            "ssm",
	"secretsmanager": "secretsmanager",
	"events":         "eventbridge",
	"lambda":         "lambda",
}

// lambdaPathPrefixes are the REST-API version prefixes the Lambda control
// plane uses (functions, tags, layers, event source mappings, function URLs).
var lambdaPathPrefixes = []string{
	"/2015-03-31/",
	"/2017-03-31/",
	"/2017-10-31/",
	"/2018-10-31/",
	"/2019-09-25/",
	"/2019-09-30/",
	"/2020-06-30/",
	"/2021-10-31/",
}

// Gateway is the shared-endpoint router. Register handlers for the services a
// deployment enables; requests for everything else get a clean error.
type Gateway struct {
	handlers map[string]http.Handler
	logf     func(format string, args ...any)
	now      func() time.Time
}

// Options configures a Gateway.
type Options struct {
	// Logf receives one line per routing failure; nil discards.
	Logf func(format string, args ...any)
	// Now is the clock used for presigned-URL expiry; nil means time.Now.
	Now func() time.Time
}

// New builds an empty gateway; add services with Register.
func New(opts Options) *Gateway {
	g := &Gateway{
		handlers: map[string]http.Handler{},
		logf:     opts.Logf,
		now:      opts.Now,
	}
	if g.logf == nil {
		g.logf = func(string, ...any) {}
	}
	if g.now == nil {
		g.now = time.Now
	}
	return g
}

// Register mounts h as the handler for the named service.
func (g *Gateway) Register(service string, h http.Handler) {
	g.handlers[service] = h
}

// Handler returns the registered handler for a service, or nil.
func (g *Gateway) Handler(service string) http.Handler { return g.handlers[service] }

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Presigned-URL expiry is enforced centrally: it applies to any service and
	// is the one signature check a local emulator genuinely benefits from.
	if present, expired := sigparse.PresignedExpiry(r.URL.Query(), g.now()); present && expired {
		writeRouteError(w, 403, "AccessDenied", "presigned URL has expired")
		return
	}

	service, why := g.route(r)
	h, ok := g.handlers[service]
	if !ok {
		g.logf("gateway: no %q service enabled (routed by %s, %s %s)", service, why, r.Method, r.URL.Path)
		writeRouteError(w, 501, "InvalidAction",
			"request was routed to the %q service (by %s), which is not enabled on this doze-aws endpoint", service, why)
		return
	}
	h.ServeHTTP(w, r)
}

// route picks the service for a request and names the rule that decided, for
// error messages and logs.
func (g *Gateway) route(r *http.Request) (service, why string) { return routeService(r) }

// Route picks the AWS service a request is destined for, using the same rules
// the gateway dispatches by. Exported so an out-of-process fanout (e.g. the
// console talking to per-service sockets) routes identically to the in-process
// gateway — one source of truth, no drift.
func Route(r *http.Request) string {
	svc, _ := routeService(r)
	return svc
}

// routeService is the pure routing logic shared by the gateway and Route.
func routeService(r *http.Request) (service, why string) {
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		prefix, _, _ := strings.Cut(target, ".")
		if svc, ok := targetPrefixes[prefix]; ok {
			return svc, "X-Amz-Target prefix"
		}
	}
	if scope, ok := sigparse.Parse(r); ok && scope.Service != "" {
		if svc, ok := scopeServices[scope.Service]; ok {
			return svc, "signature scope"
		}
	}
	for _, prefix := range lambdaPathPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return "lambda", "Lambda API path"
		}
	}
	if action := peekAction(r); action != "" {
		if svc, ok := queryActions[action]; ok {
			return svc, "Query action"
		}
	}
	return "s3", "fallback"
}

// peekAction extracts a Query-protocol Action parameter without consuming the
// request body: form bodies are read and then restored for the handler.
func peekAction(r *http.Request) string {
	if action := r.URL.Query().Get("Action"); action != "" {
		return action
	}
	if r.Method != http.MethodPost {
		return ""
	}
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return ""
	}
	return vals.Get("Action")
}

// writeRouteError emits a routing-level error. The requester's protocol isn't
// reliably known at this point, so the body is the JSON error shape (both SDK
// generations surface the code and message from it, even for XML services).
func writeRouteError(w http.ResponseWriter, status int, code, format string, args ...any) {
	e := awshttp.Errf(status, code, format, args...)
	body, _ := json.Marshal(map[string]string{"__type": e.Code, "message": e.Message})
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", e.Code)
	w.WriteHeader(e.Status)
	w.Write(body)
}
