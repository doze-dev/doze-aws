// Package provision is doze-aws's resource-graph engine: an intermediate
// representation of a desired stack, plus the convergent Apply and Destroy
// that make the live services match it.
//
// It is deliberately NOT a file format. doze-aws once had its own stack.yaml
// dialect; that has been removed, because a format only doze-aws speaks is a
// format nobody wants to learn. CloudFormation, SAM, CDK and Serverless all
// converge on CloudFormation templates, so that is the front end, and this
// package is what a template compiles down to.
//
// Design choices, deliberately:
//   - Resources are named and wired by NAME, not ARN — inside one local
//     account and region names are unambiguous.
//   - Apply is CONVERGENT: create what is missing, update what is cheap to
//     update, and never touch a value a human may have changed. Destroy is the
//     explicit inverse.
//   - Dependency order is fixed by phase (queues before the topics that
//     subscribe to them, functions before the rules that target them, bucket
//     notifications last) so references always resolve.
package provision

import (
	"fmt"
	"strings"
)

// Stack is the desired state of a set of local AWS resources.
type Stack struct {
	Queues     map[string]Queue
	Topics     map[string]Topic
	Buckets    map[string]Bucket
	Tables     map[string]Table
	Functions  map[string]Function
	Rules      map[string]Rule
	Keys       map[string]Key
	Secrets    map[string]Secret
	Parameters map[string]Parameter
}

type Queue struct {
	FIFO         bool
	ContentDedup bool
	DLQ          string // "auto" or a queue name
	MaxReceives  int
	Visibility   int
	Delay        int
	Retention    int
	ReceiveWait  int // long-poll default, seconds
	MaxSize      int // MaximumMessageSize, bytes
	Tags         map[string]string
}

type Topic struct {
	Subscriptions []Subscription
	Tags          map[string]string
}

// Subscription names exactly one endpoint kind.
type Subscription struct {
	Queue  string
	Lambda string
	HTTP   string
	Filter Doc  // SNS filter policy (inline YAML or JSON string)
	Raw    bool // raw message delivery
}

type Bucket struct {
	Versioning bool
	ObjectLock bool
	Notify     []Notify
	CORS       []CORSRule
	Lifecycle  []LifecycleRule
	Website    *Website
	Tags       map[string]string
}

// CORSRule mirrors one S3 CORSRule; preflight evaluation is real locally.
type CORSRule struct {
	Origins []string
	Methods []string
	Headers []string
	Expose  []string
	MaxAge  int
}

// LifecycleRule covers the expiry rules the local janitor actually enforces:
// current-version expiry, noncurrent-version expiry, and stale-multipart abort.
type LifecycleRule struct {
	Prefix          string
	ExpireDays      int
	NoncurrentDays  int
	AbortUploadDays int
}

// Website enables bucket-website index/error document serving.
type Website struct {
	Index string // IndexDocument suffix, e.g. index.html
	Error string // ErrorDocument key, e.g. 404.html
}

// Notify wires bucket events to exactly one destination kind.
type Notify struct {
	Events []string // default ["s3:ObjectCreated:*"]
	Prefix string
	Suffix string
	Queue  string
	Topic  string
	Lambda string
}

type Table struct {
	Key                string // "pk:S" or "pk:S sk:N"
	TTL                string
	GSIs               map[string]GSI
	LSIs               map[string]LSI
	DeletionProtection *bool
	Tags               map[string]string
}

type GSI struct {
	Key        string   // same shorthand as Table.Key
	Projection string   // ALL (default) | KEYS_ONLY | INCLUDE
	Include    []string // non-key attributes, with projection: INCLUDE
}

// LSI declares a local secondary index: the table's partition key plus this
// sort key.
type LSI struct {
	Key        string // the sort key, "attr:TYPE"
	Projection string
	Include    []string
}

type Function struct {
	Runtime   string
	Handler   string
	Code      string // local path (the _local_ extension)
	Command   []string
	Env       map[string]string
	Timeout   int
	Memory    int
	DLQ       *Dest // DeadLetterConfig: where exhausted async invokes land
	Retries   *int  // async MaximumRetryAttempts (0–2, default 2)
	OnSuccess *Dest
	OnFailure *Dest
	Triggers  []Trigger
	Tags      map[string]string
}

// Dest names exactly one destination kind.
type Dest struct {
	Queue  string
	Topic  string
	Lambda string
}

type Trigger struct {
	Queue   string
	Batch   int
	Enabled *bool // default true; false parks the poller
}

type Rule struct {
	Bus      string // default "default"
	Pattern  Doc
	Schedule string
	Enabled  *bool // default true; false stores the rule DISABLED
	Targets  []Target
}

// Target is one rule target: the "queue:orders" / "topic:t" / "lambda:fn"
// scalar shorthand, or a mapping that adds input shaping:
//
//	targets:
//	  - queue: audit
//	  - lambda: resize
//	    input_path: $.detail
//	  - topic: alerts
//	    template: '{"msg": <msg>}'
//	    paths: {msg: $.detail.message}
type Target struct {
	Queue  string
	Topic  string
	Lambda string

	Input     Doc               // literal event to deliver instead
	InputPath string            // JSONPath into the event, e.g. $.detail
	Template  string            // InputTransformer template with <name> slots
	Paths     map[string]string // InputTransformer name → JSONPath
}

type Key struct {
	Spec        string // default SYMMETRIC_DEFAULT
	Usage       string // ENCRYPT_DECRYPT | SIGN_VERIFY | GENERATE_VERIFY_MAC (default per spec)
	Description string
	Rotation    bool
	Tags        map[string]string
}

type Secret struct {
	Value       string
	Binary      string // base64 SecretBinary (instead of value)
	Description string
	Force       bool // overwrite a live value on apply
	Tags        map[string]string
}

// Parameter accepts a scalar shorthand:
//
//	parameters:
//	  /app/db/host: localhost
type Parameter struct {
	Value       string
	Type        string // String | SecureString | StringList
	Description string
	Force       bool
	Tags        map[string]string
}

// Doc is a JSON document carried verbatim (event patterns, filter policies).
type Doc struct {
	JSON string
}

func (d Doc) IsZero() bool { return d.JSON == "" }

// keyAttr is one parsed "name:TYPE" key element.
type keyAttr struct{ Name, Type string }

// parseKey parses the "pk:S" / "pk:S sk:N" shorthand.
func parseKey(s string) (hash keyAttr, rng *keyAttr, err error) {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 2 {
		return hash, nil, fmt.Errorf("key %q: want \"attr:TYPE\" or \"pk:TYPE sk:TYPE\"", s)
	}
	parse := func(f string) (keyAttr, error) {
		name, typ, ok := strings.Cut(f, ":")
		if !ok || name == "" {
			return keyAttr{}, fmt.Errorf("key part %q: want \"attr:TYPE\"", f)
		}
		typ = strings.ToUpper(typ)
		if typ != "S" && typ != "N" && typ != "B" {
			return keyAttr{}, fmt.Errorf("key part %q: type must be S, N or B", f)
		}
		return keyAttr{name, typ}, nil
	}
	if hash, err = parse(fields[0]); err != nil {
		return hash, nil, err
	}
	if len(fields) == 2 {
		r, err := parse(fields[1])
		if err != nil {
			return hash, nil, err
		}
		rng = &r
	}
	return hash, rng, nil
}
