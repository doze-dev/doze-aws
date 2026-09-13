// Package dozeaws assembles the doze-aws services into one embeddable stack:
// every enabled service constructed over a shared data root, wired to each
// other in-process, and fronted by the shared-endpoint gateway. This is what
// the doze-aws binary serves, and what a Go program embeds when it wants all
// of local AWS behind a single http.Handler:
//
//	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: "./data"})
//	defer stack.Close()
//	http.ListenAndServe("127.0.0.1:4566", stack.Handler())
//
// Programs that want a single service (their own process supervision, custom
// wiring) skip this package and construct the service directly — every service
// package (sts, sqs, ...) exports New(Options) returning an http.Handler +
// io.Closer.
package dozeaws

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/apigateway"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/cloudwatch"
	"github.com/doze-dev/doze-aws/dynamodb"
	"github.com/doze-dev/doze-aws/eventbridge"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/kinesis"
	"github.com/doze-dev/doze-aws/kms"
	"github.com/doze-dev/doze-aws/lambda"
	"github.com/doze-dev/doze-aws/logs"
	"github.com/doze-dev/doze-aws/peers"
	"github.com/doze-dev/doze-aws/s3"
	"github.com/doze-dev/doze-aws/secretsmanager"
	"github.com/doze-dev/doze-aws/sns"
	"github.com/doze-dev/doze-aws/sqs"
	"github.com/doze-dev/doze-aws/ssm"
	"github.com/doze-dev/doze-aws/stepfunctions"
	"github.com/doze-dev/doze-aws/sts"
)

// Implemented lists the services this build of doze-aws can serve, in gateway
// order (currently the full set gateway.Services knows about).
var Implemented = []string{"s3", "dynamodb", "sqs", "sns", "sts", "kms", "ssm", "secretsmanager", "eventbridge", "lambda", "kinesis", "iam", "cloudformation", "apigateway", "stepfunctions", "logs", "cloudwatch"}

// GlobalDir is the directory holding the services that have no region.
//
// It is a name no region can collide with: AWS region codes are
// <area>-<direction>-<number> and never begin with an underscore.
const GlobalDir = "_global"

// Global names the services whose resources are genuinely region-less, and so
// are shared by every region rather than duplicated per region.
//
// This is not a judgement call: it is exactly the set that mints ARNs through
// awsident.GlobalARN — arn:aws:iam::<account>:… with an empty region segment —
// which is AWS's own way of saying a resource is account-wide. A user or role
// created in one region is the same user or role in every other.
var Global = map[string]bool{"iam": true, "sts": true}

// ServiceDir is where a service's data lives under the data directory:
// <region>/<service>, or _global/<service> for the region-less ones.
//
// A region is a folder. That is the whole multi-region storage mechanism —
// every service's store is opened under one, so none of the 245 store methods
// or 76 bbolt buckets had to learn what a region is.
func ServiceDir(region, service string) string {
	if Global[service] {
		return filepath.Join(GlobalDir, service)
	}
	if region == "" {
		region = awsident.Region
	}
	return filepath.Join(region, service)
}

// StackConfig configures a Stack.
type StackConfig struct {
	// DataDir is the root under which each service gets its own subdirectory.
	// Required once any stateful service is enabled; the Phase-1 services are
	// stateless and tolerate it empty.
	DataDir string
	// Services to enable; nil enables every implemented service. Unknown or
	// unimplemented names are an error.
	Services []string
	// Logf receives service and gateway log lines; nil discards.
	Logf func(format string, args ...any)
	// LambdaIdleTimeout is how long a warm Lambda function keeps its process(es)
	// before scaling to zero. Zero uses the service default (10m).
	LambdaIdleTimeout time.Duration
	// LambdaQuiet stops function output from being echoed to Logf.
	LambdaQuiet bool
	// LambdaRuntimes overrides the interpreter per runtime family.
	LambdaRuntimes map[string]string
	// IAMMode selects how far the IAM service goes on the request path:
	// "soft" (the default) evaluates and records without blocking, "off"
	// never evaluates anything, "enforce" returns real AccessDenied errors.
	IAMMode iam.Mode
	// Endpoint is the externally-reachable base URL of this stack's gateway
	// (e.g. "http://127.0.0.1:4566"). It is injected into Lambda function
	// processes as AWS_ENDPOINT_URL so handler code using an AWS SDK reaches
	// sibling services. Leave empty when running fully embedded with no HTTP
	// listener; service-to-service calls still work via in-process peers.
	Endpoint string
	// Suffix is this instance's DNS suffix, standing in for amazonaws.com in
	// the AWS-shaped hostnames it recognises and mints. Empty means only the
	// conventional infixes are recognised.
	Suffix string
	// Shared, when set, supplies the region-less services (IAM, STS) rather
	// than this stack building its own. Regions sets it so every region reaches
	// one _global store — bbolt is single-writer, so a second opener would
	// block forever. Nil means build them, which is the single-region case.
	Shared *Shared
	// Identity is the region and account this stack mints ARNs for. The zero
	// value means the conventional local identity (us-east-1, 000000000000),
	// so an embedder that does not care never has to name one.
	Identity awsident.Identity
	// Clock overrides time.Now for every service in the stack. Nil means real
	// time, which is what every deployment wants.
	//
	// Each service has taken a Clock since it was written, and until now the
	// stack passed one to none of them — so the seam existed, was exercised by
	// per-service unit tests, and was thrown away by the assembly that every
	// deployment and every cross-service test actually goes through. A test
	// that needed to move time could only do it one service at a time, which is
	// useless for anything involving two.
	//
	// It does NOT make a stack deterministic on its own: a dozen background
	// sweepers still run on wall-clock tickers, so an advanced clock is noticed
	// on the next real tick rather than immediately. It makes the RECORDED
	// times consistent, and it is the prerequisite for anything that wants to
	// age a whole stack rather than wait.
	Clock func() time.Time
}

// Stack is a running set of services behind one gateway.
type Stack struct {
	gw      *gateway.Gateway
	id      awsident.Identity // the region and account this stack mints ARNs for
	suffix  string            // stands in for amazonaws.com in hostnames
	closers []io.Closer
	// iam is retained so Handler can install the authorization middleware. It
	// is nil when the service is disabled, and unused when its mode is off.
	iam *iam.Server
	// lambda is retained so its event-source pollers can be given a trace sink
	// after the recorder exists.
	lambda *lambda.Server
	// stepfunctions is retained for the same reason: its engine advances
	// executions from a scheduler goroutine, which has no request context.
	stepfunctions *stepfunctions.Server
	// cloudwatch is retained for the same reason: its alarm evaluator runs on
	// a ticker and fires SNS and Lambda actions with no request to inherit.
	cloudwatch *cloudwatch.Server
	// closeOnce serialises Close. Nilling closers made a second SEQUENTIAL
	// close a no-op already; two at once raced on that field, and this is the
	// object a signal handler and a defer both plausibly hold.
	closeOnce sync.Once
	// faults records every 5xx this stack answered with.
	//
	// Per-stack rather than the package-level hook, because that hook is global
	// and the last stack constructed owns it — so with two stacks running at
	// once, which the test suite does deliberately, one stack's faults were
	// reported to the other's logger. Here they are reported to the stack that
	// produced them, and Faults() makes "did this stack fault?" a question a
	// test can ask rather than a line someone has to notice in the output.
	faultMu sync.Mutex
	faults  []Fault
}

// Fault is one 5xx a stack answered with.
type Fault struct {
	RequestID string
	Code      string
	Status    int
}

// Faults returns the server faults this stack has answered with, in order.
//
// A 5xx during a request a test believes is valid is a bug by definition, which
// makes this the cheapest assertion in the suite: no new test, no new fixture,
// just "and nothing broke while you did that".
func (s *Stack) Faults() []Fault {
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	return append([]Fault(nil), s.faults...)
}

// recordFault is the sink installed on every response this stack writes.
func (s *Stack) recordFault(id string, e *awshttp.APIError) {
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	s.faults = append(s.faults, Fault{RequestID: id, Code: e.Code, Status: e.Status})
}

// NewStack constructs and wires the requested services.
func NewStack(cfg StackConfig) (*Stack, error) {
	names := cfg.Services
	if names == nil {
		names = Implemented
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Before anything is opened: the data records what identity it was created
	// under, and refuses a different account. Here rather than in the binary
	// because this is the one path EVERY deployment goes through — including
	// doze, which constructs a Stack directly with nothing but a data
	// directory. See instance.go.
	if err := stampInstance(cfg.DataDir, cfg.Identity, logf); err != nil {
		return nil, err
	}

	// An unexpected error becomes an opaque 500 on the wire, which is right —
	// internal detail must not leak to a client. It used to become nothing at
	// all in the log, which is not: the one person who could act on it is the
	// one running the server.
	awshttp.SetInternalFaultHandler(func(err error) {
		logf("doze-aws: internal fault (answered 500): %v", err)
	})
	// The other half of that line. The fault hook has the cause but not the
	// request; this one has the request id but not the cause, because it fires
	// where the response is written. Together a 500 puts both in the log, and
	// the id is the half a user can actually quote back at you — it is the one
	// the SDK prints and the console's Traffic row now carries.
	awshttp.SetFaultResponseHandler(func(id string, e *awshttp.APIError) {
		logf("doze-aws: answered %d %s — request id %s", e.Status, e.Code, id)
	})

	gw := gateway.New(gateway.Options{Logf: logf, Now: cfg.Clock, Identity: cfg.Identity, Suffix: cfg.Suffix})
	st := &Stack{gw: gw, id: cfg.Identity, suffix: cfg.Suffix}
	for _, name := range names {
		if !gateway.KnownService(name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: unknown service %q (known: %s)", name, strings.Join(gateway.Services, ", "))
		}
		if !slices.Contains(Implemented, name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: service %q is not implemented yet (implemented: %s)", name, strings.Join(Implemented, ", "))
		}
		// A region-less service is built once and shared when Regions supplies
		// one: its store lives under _global, and bbolt is single-writer, so a
		// per-region copy would block on the file lock rather than work. It is
		// still REGISTERED here, so a service in this region resolves it
		// through peers exactly as if it were local.
		if cfg.Shared != nil && Global[name] {
			h, ok := cfg.Shared.handlers[name]
			if !ok {
				continue // not enabled
			}
			gw.Register(name, h)
			if name == "iam" {
				st.iam = cfg.Shared.iam
			}
			continue
		}
		h, closer, err := st.build(name, cfg, logf)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("dozeaws: start %s: %w", name, err)
		}
		gw.Register(name, h)
		if closer != nil {
			st.closers = append(st.closers, closer)
		}
	}
	return st, nil
}

// build constructs one service. Cross-service wiring uses peers.InProcess over
// the gateway's registry, so a service finds its siblings no matter the
// construction order.
func (st *Stack) build(name string, cfg StackConfig, logf func(string, ...any)) (http.Handler, io.Closer, error) {
	dataDir := ""
	if cfg.DataDir != "" {
		dataDir = filepath.Join(cfg.DataDir, ServiceDir(cfg.Identity.RegionName(), name))
	}
	// Peers resolve through the gateway registry at call time, so services
	// find their siblings regardless of construction order.
	dir := peers.InProcess(st.gw.Handler)
	switch name {
	case "s3":
		s, err := s3.New(s3.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity, Suffix: cfg.Suffix})
		return s, s, err
	case "dynamodb":
		s, err := dynamodb.New(dynamodb.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "sts":
		s, err := sts.New(sts.Options{DataDir: dataDir, Clock: cfg.Clock, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "sqs":
		s, err := sqs.New(sqs.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "sns":
		s, err := sns.New(sns.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "kms":
		s, err := kms.New(kms.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "ssm":
		s, err := ssm.New(ssm.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "secretsmanager":
		s, err := secretsmanager.New(secretsmanager.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "logs":
		s, err := logs.New(logs.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "cloudwatch":
		s, err := cloudwatch.New(cloudwatch.Options{
			DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		if err == nil {
			st.cloudwatch = s // retained so the evaluator can be given a trace sink
		}
		return s, s, err
	case "stepfunctions":
		s, err := stepfunctions.New(stepfunctions.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity})
		if err == nil {
			st.stepfunctions = s // retained so the engine can be given a trace sink
		}
		return s, s, err
	case "eventbridge":
		s, err := eventbridge.New(eventbridge.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "lambda":
		s, err := lambda.New(lambda.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IdleTimeout: cfg.LambdaIdleTimeout, QuietFunctions: cfg.LambdaQuiet, Runtimes: cfg.LambdaRuntimes, Endpoint: cfg.Endpoint, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity, Suffix: cfg.Suffix})
		if err == nil {
			st.lambda = s // retained so its pollers can be given a trace sink
		}
		return s, s, err
	case "kinesis":
		s, err := kinesis.New(kinesis.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "apigateway":
		s, err := apigateway.New(apigateway.Options{DataDir: dataDir, Clock: cfg.Clock, Peers: dir, Logf: logf, Identity: cfg.Identity, Suffix: cfg.Suffix, Endpoint: cfg.Endpoint})
		return s, s, err
	case "cloudformation":
		// CloudFormation provisions across every other service, so it is the
		// one service handed the whole gateway. It resolves at request time,
		// so construction order does not matter.
		s, err := cloudformation.New(cloudformation.Options{Identity: cfg.Identity, Clock: cfg.Clock,
			DataDir: dataDir, Gateway: st.gw, Peers: dir, Logf: logf,
			Endpoint: cfg.Endpoint, Suffix: cfg.Suffix,
		})
		return s, s, err
	case "iam":
		s, err := iam.New(iam.Options{DataDir: dataDir, Clock: cfg.Clock, Mode: cfg.IAMMode, Peers: dir, Logf: logf, Identity: cfg.Identity})
		if err == nil {
			st.iam = s
		}
		return s, s, err
	}
	return nil, nil, fmt.Errorf("no constructor for %q", name)
}

// Handler returns the shared-endpoint gateway handler.
//
// In soft — the default — and in enforce, the gateway is wrapped in the
// authorization middleware. With IAM off it is wrapped in a thinner handler
// that only strips the client's X-Doze-* headers, because the service guards
// read the mode from one and a header a client can set is not one anything
// should trust.
func (s *Stack) Handler() http.Handler {
	return s.recordingFaults(s.handler())
}

// recordingFaults installs this stack's fault sink on every response.
//
// Outermost, and before the gateway mints the request id: NoteFault walks the
// same Unwrap chain ResponseID does, so both wrappers are found wherever they
// sit relative to each other, and putting this one outside means it also covers
// the routing errors the gateway answers before any service is reached.
func (s *Stack) recordingFaults(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(awshttp.WithFaultRecorder(w, s.recordFault), r)
	})
}

func (s *Stack) handler() http.Handler {
	if s.iam == nil || s.iam.Mode() == iam.ModeOff {
		// Still stripped. Off means nothing is enforced, so a client that
		// stamps its own handoff headers gains nothing today — but the
		// guards read those headers to decide the mode they run in, and a
		// header a client can set is not one anything should trust. The
		// strip belongs on the way in, not on the branch that happens to
		// evaluate policies.
		return iamguard.StripHandler(s.gw)
	}
	return s.authorized(s.gw)
}

// authorized wraps h with IAM evaluation. The service is resolved with the
// gateway's own routing rules, so the middleware and the dispatcher can never
// disagree about which service a request belongs to.
func (s *Stack) authorized(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A client cannot claim a principal or a verdict: the handoff headers
		// are the middleware's to write.
		iamguard.Strip(r)
		res := s.iam.Authorize(r, gateway.Route(s.id, s.suffix, r))
		if res.Err != nil {
			writeDenied(w, res.Err)
			return
		}
		if res.Action == "" {
			h.ServeHTTP(w, r)
			return
		}
		// The service finishes the question with its resource policy and
		// reports the verdict on the response for the recorder. It can ask
		// for the identity verdict again on a pair it resolves differently.
		iamguard.Stamp(r, string(s.iam.Mode()), res.Principal, res.Identity, res.Action, res.Resource)
		r = iamguard.WithReauthorize(r, s.iam.Identity)
		h.ServeHTTP(w, r)
		if dec, ok := iam.ParseDecision(w.Header().Get(iamguard.HeaderDecision)); ok {
			by, source := w.Header().Get(iamguard.HeaderMatchedBy), "resource"
			if by == "" {
				// The identity half decided: the caption stays its statement.
				by, source = res.MatchedBy, "identity"
			}
			s.iam.RecordResource(res.Principal, res.Action, res.Resource, dec, by, source)
		}
	})
}

// writeDenied renders an AccessDenied. The requester's protocol is not
// reliably known at this point, so the JSON error shape is used — both SDK
// generations surface the code and message from it, even for XML services.
func writeDenied(w http.ResponseWriter, e *awshttp.APIError) {
	body, _ := json.Marshal(map[string]string{"__type": e.Code, "message": e.Message})
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", e.Code)
	w.WriteHeader(e.Status)
	w.Write(body)
}

// Service returns one service's handler (bypassing gateway routing), or nil if
// it isn't enabled — useful for mounting a service on its own listener.
func (s *Stack) Service(name string) http.Handler { return s.gw.Handler(name) }

// Identity is the region and account this stack mints ARNs for — the
// StackConfig value, or the conventional local identity when that was the zero
// value.
//
// Exported because two things an embedder assembles AROUND a stack now require
// the SAME identity it was built with, and getting it wrong is quiet rather
// than loud: console.NewRecorder classifies a queue URL by its account prefix,
// so a recorder given the wrong account labels SQS traffic as S3, and
// provision.Apply mints ARNs that are SENT to the stack. Before this an
// embedder had to remember what it passed to NewStack and pass the same thing
// again; now it can ask.
func (s *Stack) Identity() awsident.Identity { return s.id }

// SetTraceSink tells services that do their own polling where to report the
// work a queued message caused.
//
// It is a setter rather than a config field because the recorder wraps the
// assembled stack — it cannot exist before the services it will observe.
func (s *Stack) SetTraceSink(sink trace.Sink) {
	if s.lambda != nil {
		s.lambda.SetTraceSink(sink)
	}
	if s.stepfunctions != nil {
		s.stepfunctions.SetTraceSink(sink)
	}
	// The alarm evaluator fires actions from a ticking goroutine with no
	// request context, so its cascade only reaches the recorder this way.
	if s.cloudwatch != nil {
		s.cloudwatch.SetTraceSink(sink)
	}
}

// Close shuts every service down, releasing stores and background janitors.
func (s *Stack) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		for _, c := range s.closers {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		s.closers = nil
	})
	return firstErr
}

// OperationResolvers maps each path-routed service onto the function that names
// the AWS operation a request addresses.
//
// S3, Lambda and API Gateway carry their operation in the PATH rather than in an
// X-Amz-Target header or an Action parameter, so naming one means consulting
// that service's own route table. This hands those tables to a consumer that
// must not import the service packages — the console, which in the module
// topology runs as a separate process over unix sockets.
//
// Returning it from here rather than from the console keeps the dependency
// pointing the right way: this package already imports every service, and the
// console imports none of them.
func OperationResolvers() map[string]func(*http.Request) string {
	return map[string]func(*http.Request) string{
		"s3":     s3.OperationFor,
		"lambda": lambda.OperationFor,
		"apigw":  apigateway.OperationFor,
	}
}
