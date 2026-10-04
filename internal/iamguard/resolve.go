package iamguard

// Resolving an HTTP request into the (action, resource) pair IAM evaluates.
//
// This is the part that decides how precise enforcement can be, and where the
// honest boundary sits. Three shapes of request exist across doze-aws:
//
//   - JSON services carry the operation in X-Amz-Target: exact, free.
//   - Query services carry it in the Action parameter: exact, free.
//   - REST services (S3, Lambda) encode it in method + path. Their router
//     matches it against AWS's own table, and the permission it is authorized
//     as comes from AWS's own list — so they resolve it themselves, and the
//     stack hands each one's resolver (Resolver) to the middleware. This
//     package never guesses it from a path: it used to, and got twenty of
//     Lambda's and about as many of S3's wrong.
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
	"stepfunctions":  "states",
	"logs":           "logs",
	"cloudwatch":     "cloudwatch",
}

// resourceField names the request parameter holding the resource identifier
// for each service, and how to turn it into an ARN.
type resourceRule struct {
	fields []string // parameter names to try, in order
	// toARN converts the raw value into an ARN. It takes an identity rather
	// than reading a package constant: these closures live in a package-level
	// map, so they cannot capture one, and the ARN belongs to the instance.
	toARN func(id awsident.Identity, v string) string
}

var resourceRules = map[string]resourceRule{
	"sqs": {fields: []string{"QueueUrl", "QueueName"}, toARN: func(id awsident.Identity, v string) string {
		// A queue URL ends in /<account>/<name>; the name is what matters.
		if i := strings.LastIndex(v, "/"); i >= 0 {
			v = v[i+1:]
		}
		return id.ARN("sqs", v)
	}},
	"sns": {fields: []string{"TopicArn", "ResourceArn", "SubscriptionArn"}, toARN: identity},
	"dynamodb": {fields: []string{"TableName"}, toARN: func(id awsident.Identity, v string) string {
		return id.ARN("dynamodb", "table/"+v)
	}},
	"kinesis": {fields: []string{"StreamARN", "StreamName"}, toARN: func(id awsident.Identity, v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return id.ARN("kinesis", "stream/"+v)
	}},
	"kms": {fields: []string{"KeyId"}, toARN: func(id awsident.Identity, v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		if strings.HasPrefix(v, "alias/") {
			return id.ARN("kms", v)
		}
		return id.ARN("kms", "key/"+v)
	}},
	"secretsmanager": {fields: []string{"SecretId", "Name"}, toARN: func(id awsident.Identity, v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return id.ARN("secretsmanager", "secret:"+v)
	}},
	"ssm": {fields: []string{"Name"}, toARN: func(id awsident.Identity, v string) string {
		return id.ARN("ssm", "parameter"+ensureLeadingSlash(v))
	}},
	"eventbridge": {fields: []string{"Name", "EventBusName"}, toARN: func(id awsident.Identity, v string) string {
		return id.ARN("events", "rule/"+v)
	}},
	// CloudWatch names an alarm two ways: AlarmName on the single-alarm
	// operations, and AlarmNames — a LIST — on DeleteAlarms and the
	// enable/disable pair. A list field is a shape no other rule here has, so
	// resolveField below takes the first element: an IAM decision needs one
	// resource, and refusing a batch because it names several would deny work
	// AWS allows. The batch is authorised as its first alarm, which is
	// documented in iam.md rather than left to be discovered.
	//
	// The metric operations resolve to no resource at all: PutMetricData and
	// GetMetricStatistics act on a namespace, not on an ARN, and AWS
	// authorises them against "*" with a cloudwatch:namespace condition. An
	// invented ARN would be worse than none.
	"cloudwatch": {fields: []string{"AlarmName", "AlarmNames"}, toARN: func(id awsident.Identity, v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return id.ARN("cloudwatch", "alarm:"+v)
	}},
	// Step Functions spells its members lowercase-initial, unlike every other
	// service here. Matching is exact, so "StateMachineArn" would resolve to an
	// empty resource and only ever match a policy saying "Resource": "*".
	"stepfunctions": {fields: []string{"stateMachineArn", "activityArn", "executionArn", "resourceArn", "name"}, toARN: func(id awsident.Identity, v string) string {
		if strings.HasPrefix(v, "arn:") {
			return v
		}
		return id.ARN("states", "stateMachine:"+v)
	}},
}

func identity(_ awsident.Identity, v string) string { return v }

func ensureLeadingSlash(v string) string {
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "/" + v
}

// Resolver is how a REST service names the permission a request is authorized
// as and the resource it names: its router says which operation the request is,
// and AWS's own list says what that operation is authorized as. An empty action
// means the request is not to be evaluated.
type Resolver func(r *http.Request) (action, resource string)

// ResolveAction maps a request onto the IAM action it exercises and, where it
// can be determined, the resource ARN. An empty action means doze-aws cannot
// classify the request and it should not be evaluated.
func ResolveAction(id awsident.Identity, r *http.Request, service string) (action, resource string) {
	prefix, known := actionPrefixes[service]
	if !known {
		return "", ""
	}

	// 1. JSON protocol: X-Amz-Target is "Prefix.Operation".
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		if _, op, ok := strings.Cut(target, "."); ok && op != "" {
			return iamAction(prefix, op), resourceFromBody(id, r, service)
		}
	}

	// 2. REST services are resolved by their own resolver (Resolver), which the
	// middleware is handed; with none, they are not evaluated here.
	if service == "s3" || service == "lambda" {
		if resolve := resolverOf(r); resolve != nil {
			return resolve(r)
		}
		return "", ""
	}

	// 3. Query protocol: the Action parameter, in the query string or the form.
	if op := r.URL.Query().Get("Action"); op != "" {
		return iamAction(prefix, op), resourceFromForm(id, r, service, r.URL.Query())
	}
	if form, ok := peekForm(r); ok {
		if op := form.Get("Action"); op != "" {
			return iamAction(prefix, op), resourceFromForm(id, r, service, form)
		}
	}
	return "", ""
}

// iamAction is the IAM action an operation is authorized as. Most are the
// operation's own name; the batch operations are authorized as the action
// they batch, since sqs:SendMessageBatch and sns:PublishBatch are not IAM
// actions and a policy on sqs:SendMessage covers both.
// Action is exported for the services, whose guards name their own actions.
func Action(prefix, op string) string { return iamAction(prefix, op) }

func iamAction(prefix, op string) string {
	switch prefix + ":" + op {
	case "sqs:SendMessageBatch":
		return "sqs:SendMessage"
	case "sqs:DeleteMessageBatch":
		return "sqs:DeleteMessage"
	case "sqs:ChangeMessageVisibilityBatch":
		return "sqs:ChangeMessageVisibility"
	case "sns:PublishBatch":
		return "sns:Publish"
	}
	return prefix + ":" + op
}

// ---- resource extraction ----

// resourceFromBody peeks a JSON request body for the service's resource field.
// The body is fully restored for the handler.
func resourceFromBody(id awsident.Identity, r *http.Request, service string) string {
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
		switch v := doc[field].(type) {
		case string:
			if v != "" {
				return rule.toARN(id, v)
			}
		case []any:
			// A list-valued name — CloudWatch's AlarmNames on DeleteAlarms
			// and the enable/disable pair. An IAM decision needs one
			// resource, so the batch is authorised as its first member.
			if len(v) > 0 {
				if first, ok := v[0].(string); ok && first != "" {
					return rule.toARN(id, first)
				}
			}
		}
	}
	return ""
}

// resourceFromForm reads the resource from an already-parsed Query form.
func resourceFromForm(id awsident.Identity, _ *http.Request, service string, form url.Values) string {
	rule, ok := resourceRules[service]
	if !ok {
		return ""
	}
	for _, field := range rule.fields {
		if v := form.Get(field); v != "" {
			return rule.toARN(id, v)
		}
		// The Query spelling of a list: the first member is the one an IAM
		// decision is made against, matching the JSON path above.
		if v := form.Get(field + ".member.1"); v != "" {
			return rule.toARN(id, v)
		}
	}
	return ""
}

// bodyWithRest restores a partly-read body: the bytes already taken, followed
// by whatever is still unread, closing over the original.
type bodyWithRest struct {
	io.Reader
	io.Closer
}

// peekBody reads a request body and puts it back, so the service handler sees
// an untouched request. Bodies over maxPeek are left alone entirely.
//
// "Left alone" has to mean it, and it did not. The guard is on ContentLength,
// which is **-1 for a chunked request** — so chunked bodies passed it, were
// read to maxPeek, and then r.Body was REPLACED by just those bytes. The rest
// was discarded. IAM mode is soft by default, so this middleware is always on:
// a perfectly well-formed 4 MB BatchWriteItem sent with
// Transfer-Encoding: chunked reached its handler as 1 MiB of truncated JSON
// and came back as SerializationException.
//
// So read one byte PAST the limit. Getting it means the body is too big to
// peek at, and everything read goes back in front of the unread remainder
// rather than in place of it.
func peekBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil || r.ContentLength > maxPeek {
		return nil, false
	}
	orig := r.Body
	body, err := io.ReadAll(io.LimitReader(orig, maxPeek+1))
	if err != nil {
		return nil, false
	}
	if len(body) > maxPeek {
		r.Body = bodyWithRest{Reader: io.MultiReader(bytes.NewReader(body), orig), Closer: orig}
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

// LambdaFunctionARN is the function ARN a request's function reference
// names. The reference may be a name, a name with a qualifier (name:alias
// or name:version), a partial ARN (account:function:name) or a full one; the
// ARN keeps the qualifier, as a function policy written for an alias does.
//
// The cut is on ":function:" without requiring an "arn:" prefix, because that
// is what the service does (lambda/versions.go, lambda/extras.go): it accepts
// the partial ARN AWS documents. Requiring the prefix here resolved
// "000000000000:function:worker" to a function literally named
// "000000000000:function:worker", so a policy scoped to the real function
// matched neither way round — an explicit Deny did not bite.
func LambdaFunctionARN(id awsident.Identity, ref string) string {
	if i := strings.Index(ref, ":function:"); i >= 0 {
		ref = ref[i+len(":function:"):]
	}
	return id.ARN("lambda", "function:"+ref)
}

// LambdaFunctionName is the bare function name of a reference, without a
// qualifier or the ARN around it.
// It needs no identity: it used to build a full ARN and immediately strip the
// prefix back off, so the region and account were computed and discarded.
func LambdaFunctionName(ref string) string {
	if i := strings.Index(ref, ":function:"); i >= 0 {
		ref = ref[i+len(":function:"):]
	}
	name, _, _ := strings.Cut(ref, ":")
	return name
}
