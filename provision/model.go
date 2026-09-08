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
// It is public because doze-modules' aws module is a second front end: its
// HCL blocks decode straight into a Stack and hand it to Apply. Stack and the
// resource types are therefore an API — add fields, do not rename them.
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
	Queues        map[string]Queue
	Topics        map[string]Topic
	Buckets       map[string]Bucket
	Tables        map[string]Table
	Functions     map[string]Function
	Rules         map[string]Rule
	Keys          map[string]Key
	Secrets       map[string]Secret
	Parameters    map[string]Parameter
	APIs          map[string]API
	StateMachines map[string]StateMachine
	Activities    map[string]Activity
	LogGroups     map[string]LogGroup
	Layers        map[string]Layer
	// Connections and APIDestinations are EventBridge's HTTP targets: a
	// destination names the connection that authenticates it, and a rule
	// target names the destination.
	Connections     map[string]Connection
	APIDestinations map[string]APIDestination
}

// LogGroup is a CloudWatch Logs log group: a name, an optional retention in
// days, and tags. Lambda creates /aws/lambda/<fn> itself on the first line,
// so a template only needs one when it sets retention or names the group.
type LogGroup struct {
	RetentionDays int
	Tags          map[string]string
}

// StateMachine is a Step Functions state machine: its definition text (ASL),
// the role it claims, and its type. The definition travels as the exact text
// the template carried — Step Functions itself round-trips it byte-identical,
// and deploy tools diff on that.
type StateMachine struct {
	Definition string
	RoleARN    string
	Type       string // STANDARD | EXPRESS; empty means STANDARD
	Tags       map[string]string
	// Logging is the loggingConfiguration as the API takes it (level,
	// includeExecutionData, destinations); empty leaves logging off.
	Logging Doc
	// Publish records a version of the definition on every apply — what an
	// AWS::StepFunctions::StateMachineVersion in the template asks for.
	// Publishing an unchanged revision returns the version it already has,
	// so a repeated deploy does not pile up versions.
	Publish bool
	// Aliases, by name, each routing all of its traffic to the version this
	// apply published. CloudFormation's DeploymentPreference shifts an alias
	// between two versions over minutes; locally the shift is instant, which
	// is the outcome a deploy converges to anyway.
	Aliases map[string]StateMachineAlias
}

// StateMachineAlias is a named pointer at a state machine's current version.
type StateMachineAlias struct {
	Description string
}

// Activity is a Step Functions activity: a named queue a worker polls with
// GetActivityTask. It has no configuration beyond its name and tags.
type Activity struct {
	Tags map[string]string
}

// API is a REST API fronting Lambda functions.
//
// The IR is deliberately route-shaped rather than mirroring API Gateway's
// resource/method/integration tree: everything that reaches it — a SAM Api
// event, a CDK construct — is ultimately "this method on this path calls this
// function", and the tree is rebuilt from the routes at apply time.
type API struct {
	Stage  string
	Routes []Route
	// AccessLog and MethodSettings are the stage's logging settings, as an
	// AWS::ApiGateway::Stage or a Serverless::Api declares them.
	AccessLog      *APIAccessLog
	MethodSettings []APIMethodSetting
}

// APIAccessLog is a stage's access log: the log group ARN and the $context
// format each request is written in.
type APIAccessLog struct {
	DestinationARN string
	Format         string
}

// APIMethodSetting is one entry of a stage's MethodSettings: which methods
// it covers ("*" for all, or a resource path and a verb) and the logging
// it asks for.
type APIMethodSetting struct {
	Path         string // "/*" or a resource path
	Method       string // "*" or a verb
	LoggingLevel string // OFF | ERROR | INFO
	DataTrace    bool
	Metrics      bool
}

// Route is one method+path binding.
type Route struct {
	Method string // GET, POST, ANY, …
	Path   string // /orders/{id}, /{proxy+}
	Lambda string
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
	// PublicAccess is the public access block; nil leaves the bucket's
	// default (every block on). BlockPublicPolicy is enforced locally.
	PublicAccess *PublicAccessBlock
	// Ownership is the ObjectOwnership setting, stored and reported.
	Ownership string
	// Policy is the bucket policy document; applied after PublicAccess, so a
	// public policy under BlockPublicPolicy fails the apply as it does on AWS.
	Policy Doc
}

// PublicAccessBlock is S3's four-flag block public access setting.
type PublicAccessBlock struct {
	BlockPublicAcls       bool
	IgnorePublicAcls      bool
	BlockPublicPolicy     bool
	RestrictPublicBuckets bool
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
	// Layers, in order: the name of a layer in this stack (its version
	// published by this apply is used) or a full layer version ARN.
	Layers []string
	// Publish records a version of the function on every apply — what an
	// AWS::Lambda::Version in the template asks for. Publishing unchanged
	// code and configuration returns the version it already has, so a
	// repeated deploy does not pile up versions.
	Publish bool
	// Aliases, by name, each pointing at the version this apply published.
	// A weighted RoutingConfig collapses to that version, which is where a
	// CloudFormation deployment converges anyway.
	Aliases map[string]FunctionAlias
	// URL, when set, gives the function a function URL.
	URL *FunctionURL
}

// FunctionAlias is a named pointer at a function version: the one this
// apply publishes when Version is empty, or an explicit version number.
type FunctionAlias struct {
	Version     string
	Description string
}

// FunctionURL is a function URL config: NONE or AWS_IAM (accepted and
// served without checking, locally), with an optional CORS document.
type FunctionURL struct {
	AuthType string
	CORS     Doc
}

// Layer is a Lambda layer: its content (a local path — a directory laid out
// like an unpacked layer, or a zip — or an s3://bucket/key a deploy tool
// staged), and the runtimes it declares. Each apply publishes a version when
// the content changed and reuses the latest one when it did not.
type Layer struct {
	Code        string
	Runtimes    []string
	Description string
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
	Queue          string
	Topic          string
	Lambda         string
	APIDestination string // name of an APIDestination in the stack

	// HTTP shaping for an API destination target: values for the endpoint's
	// `*` segments in order, plus headers and query parameters.
	PathParams []string
	Headers    map[string]string
	Query      map[string]string

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
