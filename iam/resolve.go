package iam

// Resolving an HTTP request into the (action, resource) pair IAM evaluates.
//
// This is the part that decides how precise enforcement can be, and where the
// honest boundary sits. Three shapes of request exist across doze-aws:
//
//   - JSON services carry the operation in X-Amz-Target: exact, free.
//   - Query services carry it in the Action parameter: exact, free.
//   - REST services (S3, Lambda) encode it in method + path, which has to be
//     mapped by hand.
//
// The action is always resolved. The resource is resolved when it can be read
// cheaply and unambiguously, and left EMPTY otherwise — never guessed. An
// empty resource matches only `"Resource": "*"` statements, and the recorder
// marks the event so a generated policy admits it could not be scoped.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// maxPeek bounds how much of a request body is read to find a resource name.
// Anything larger is a data-plane payload whose resource, if any, is in the
// first few hundred bytes anyway.
const maxPeek = 1 << 20

// actionPrefixes maps doze-aws service names to IAM action prefixes. Most
// coincide; EventBridge signs and authorizes as "events".
var actionPrefixes = map[string]string{
	"s3":             "s3",
	"sqs":            "sqs",
	"sns":            "sns",
	"sts":            "sts",
	"dynamodb":       "dynamodb",
	"kms":            "kms",
	"ssm":            "ssm",
	"secretsmanager": "secretsmanager",
	"eventbridge":    "events",
	"lambda":         "lambda",
	"kinesis":        "kinesis",
	"iam":            "iam",
}

// resourceField names the request parameter holding the resource identifier
// for each service, and how to turn it into an ARN.
type resourceRule struct {
	fields []string              // parameter names to try, in order
	toARN  func(v string) string // converts the raw value into an ARN
}

var resourceRules = map[string]resourceRule{
	"sqs": {fields: []string{"QueueUrl", "QueueName"}, toARN: func(v string) string {
		// A queue URL ends in /<account>/<name>; the name is what matters.
		if i := strings.LastIndex(v, "/"); i >= 0 {
			v = v[i+1:]
		}
		return awsident.ARN("sqs", v)
	}},
	"sns": {fields: []string{"TopicArn", "ResourceArn", "SubscriptionArn"}, toARN: identity},
	"dynamodb": {fields: []string{"TableName"}, toARN: func(v string) string {
		return awsident.ARN("dynamodb", "table/"+v)
	}},
	"kinesis": {fields: []string{"StreamARN", "StreamName"}, toARN: func(v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return awsident.ARN("kinesis", "stream/"+v)
	}},
	"kms": {fields: []string{"KeyId"}, toARN: func(v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		if strings.HasPrefix(v, "alias/") {
			return awsident.ARN("kms", v)
		}
		return awsident.ARN("kms", "key/"+v)
	}},
	"secretsmanager": {fields: []string{"SecretId", "Name"}, toARN: func(v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return awsident.ARN("secretsmanager", "secret:"+v)
	}},
	"ssm": {fields: []string{"Name"}, toARN: func(v string) string {
		return awsident.ARN("ssm", "parameter"+ensureLeadingSlash(v))
	}},
	"eventbridge": {fields: []string{"Name", "EventBusName"}, toARN: func(v string) string {
		return awsident.ARN("events", "rule/"+v)
	}},
}

func identity(v string) string { return v }

func ensureLeadingSlash(v string) string {
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "/" + v
}

// ResolveAction maps a request onto the IAM action it exercises and, where it
// can be determined, the resource ARN. An empty action means doze-aws cannot
// classify the request and it should not be evaluated.
func ResolveAction(r *http.Request, service string) (action, resource string) {
	prefix, known := actionPrefixes[service]
	if !known {
		return "", ""
	}

	// 1. JSON protocol: X-Amz-Target is "Prefix.Operation".
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		if _, op, ok := strings.Cut(target, "."); ok && op != "" {
			return prefix + ":" + op, resourceFromBody(r, service)
		}
	}

	// 2. REST services encode the operation in method + path.
	switch service {
	case "s3":
		return resolveS3(r)
	case "lambda":
		return resolveLambda(r, prefix)
	}

	// 3. Query protocol: the Action parameter, in the query string or the form.
	if op := r.URL.Query().Get("Action"); op != "" {
		return prefix + ":" + op, resourceFromForm(r, service, r.URL.Query())
	}
	if form, ok := peekForm(r); ok {
		if op := form.Get("Action"); op != "" {
			return prefix + ":" + op, resourceFromForm(r, service, form)
		}
	}
	return "", ""
}

// resolveS3 maps an S3 REST request onto an action. S3's mapping is
// well-defined by method and path shape, which is why it can be done at all —
// bucket operations are distinguished from object ones by whether a key is
// present, and sub-resources by the query string.
func resolveS3(r *http.Request) (string, string) {
	bucket, key := s3Target(r)
	arn := ""
	switch {
	case bucket != "" && key != "":
		arn = "arn:aws:s3:::" + bucket + "/" + key
	case bucket != "":
		arn = "arn:aws:s3:::" + bucket
	}

	q := r.URL.Query()
	// Sub-resource operations are named by the query parameter present.
	for param, suffix := range map[string]string{
		"acl": "Acl", "policy": "BucketPolicy", "versioning": "BucketVersioning",
		"tagging": "BucketTagging", "lifecycle": "LifecycleConfiguration",
		"cors": "BucketCORS", "notification": "BucketNotification",
		"website": "BucketWebsite", "encryption": "EncryptionConfiguration",
		"replication": "ReplicationConfiguration", "uploads": "MultipartUpload",
	} {
		if _, ok := q[param]; ok {
			verb := "Get"
			switch r.Method {
			case http.MethodPut, http.MethodPost:
				verb = "Put"
			case http.MethodDelete:
				verb = "Delete"
			}
			if param == "tagging" && key != "" {
				return "s3:" + verb + "ObjectTagging", arn
			}
			return "s3:" + verb + suffix, arn
		}
	}

	if key == "" {
		switch r.Method {
		case http.MethodGet:
			if bucket == "" {
				return "s3:ListAllMyBuckets", ""
			}
			return "s3:ListBucket", arn
		case http.MethodHead:
			return "s3:ListBucket", arn
		case http.MethodPut:
			return "s3:CreateBucket", arn
		case http.MethodDelete:
			return "s3:DeleteBucket", arn
		case http.MethodPost:
			return "s3:DeleteObject", arn // POST ?delete= is the batch delete
		}
		return "", arn
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return "s3:GetObject", arn
	case http.MethodPut:
		// A copy carries the source header; AWS authorizes it as PutObject on
		// the destination.
		return "s3:PutObject", arn
	case http.MethodPost:
		return "s3:PutObject", arn
	case http.MethodDelete:
		return "s3:DeleteObject", arn
	}
	return "", arn
}

// s3Target extracts (bucket, key) from a path-style S3 request. Virtual-host
// style is not decoded here: the Host-based form needs the configured S3 host
// to split correctly, and guessing would produce a wrong ARN.
func s3Target(r *http.Request) (bucket, key string) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		return "", ""
	}
	bucket, key, _ = strings.Cut(p, "/")
	return bucket, key
}

// resolveLambda maps the Lambda REST API onto actions by path family.
func resolveLambda(r *http.Request, prefix string) (string, string) {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segs) < 2 {
		return "", ""
	}
	arn := ""
	if len(segs) >= 3 && segs[1] == "functions" {
		arn = awsident.ARN("lambda", "function:"+segs[2])
	}
	switch segs[1] {
	case "functions":
		if len(segs) >= 4 {
			switch segs[3] {
			case "invocations":
				return prefix + ":InvokeFunction", arn
			case "configuration":
				if r.Method == http.MethodGet {
					return prefix + ":GetFunctionConfiguration", arn
				}
				return prefix + ":UpdateFunctionConfiguration", arn
			case "code":
				return prefix + ":UpdateFunctionCode", arn
			case "versions":
				return prefix + ":PublishVersion", arn
			case "aliases":
				return prefix + ":" + methodVerb(r, "Alias"), arn
			case "concurrency":
				return prefix + ":PutFunctionConcurrency", arn
			case "policy":
				return prefix + ":" + methodVerb(r, "Permission"), arn
			case "url", "urls":
				return prefix + ":" + methodVerb(r, "FunctionUrlConfig"), arn
			case "event-invoke-config":
				return prefix + ":" + methodVerb(r, "FunctionEventInvokeConfig"), arn
			}
		}
		switch r.Method {
		case http.MethodGet:
			if len(segs) == 2 {
				return prefix + ":ListFunctions", ""
			}
			return prefix + ":GetFunction", arn
		case http.MethodPost:
			return prefix + ":CreateFunction", arn
		case http.MethodDelete:
			return prefix + ":DeleteFunction", arn
		}
	case "event-source-mappings":
		switch r.Method {
		case http.MethodGet:
			return prefix + ":ListEventSourceMappings", ""
		case http.MethodPost:
			return prefix + ":CreateEventSourceMapping", ""
		case http.MethodPut:
			return prefix + ":UpdateEventSourceMapping", ""
		case http.MethodDelete:
			return prefix + ":DeleteEventSourceMapping", ""
		}
	case "tags":
		if r.Method == http.MethodPost {
			return prefix + ":TagResource", ""
		}
		if r.Method == http.MethodDelete {
			return prefix + ":UntagResource", ""
		}
		return prefix + ":ListTags", ""
	case "layers":
		return prefix + ":" + methodVerb(r, "LayerVersion"), ""
	}
	return "", arn
}

func methodVerb(r *http.Request, noun string) string {
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		return "Create" + noun
	case http.MethodDelete:
		return "Delete" + noun
	default:
		return "Get" + noun
	}
}

// ---- resource extraction ----

// resourceFromBody peeks a JSON request body for the service's resource field.
// The body is fully restored for the handler.
func resourceFromBody(r *http.Request, service string) string {
	rule, ok := resourceRules[service]
	if !ok {
		return ""
	}
	body, ok := peekBody(r)
	if !ok {
		return ""
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	for _, field := range rule.fields {
		if v, ok := doc[field].(string); ok && v != "" {
			return rule.toARN(v)
		}
	}
	return ""
}

// resourceFromForm reads the resource from an already-parsed Query form.
func resourceFromForm(_ *http.Request, service string, form url.Values) string {
	rule, ok := resourceRules[service]
	if !ok {
		return ""
	}
	for _, field := range rule.fields {
		if v := form.Get(field); v != "" {
			return rule.toARN(v)
		}
	}
	return ""
}

// peekBody reads a request body and puts it back, so the service handler sees
// an untouched request. Bodies over maxPeek are left alone entirely.
func peekBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil || r.ContentLength > maxPeek {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPeek))
	if err != nil {
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, len(body) > 0
}

// peekForm parses a urlencoded body and restores it.
func peekForm(r *http.Request) (url.Values, bool) {
	if r.Method != http.MethodPost {
		return nil, false
	}
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return nil, false
	}
	body, ok := peekBody(r)
	if !ok {
		return nil, false
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, false
	}
	return vals, true
}
