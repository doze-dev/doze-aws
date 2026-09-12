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
	// S3Host is the host under which virtual-hosted-style S3 bucket addressing
	// is detected (a request to <bucket>.<S3Host> addresses that bucket).
	// Path-style always works.
	S3Host string
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
	// Identity is the region and account this stack mints ARNs for. The zero
	// value means the conventional local identity (us-east-1, 000000000000),
	// so an embedder that does not care never has to name one.
	Identity awsident.Identity
}

// Stack is a running set of services behind one gateway.
type Stack struct {
	gw      *gateway.Gateway
	id      awsident.Identity // the region and account this stack mints ARNs for
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

	gw := gateway.New(gateway.Options{Logf: logf, Identity: cfg.Identity})
	st := &Stack{gw: gw, id: cfg.Identity}
	for _, name := range names {
		if !gateway.KnownService(name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: unknown service %q (known: %s)", name, strings.Join(gateway.Services, ", "))
		}
		if !slices.Contains(Implemented, name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: service %q is not implemented yet (implemented: %s)", name, strings.Join(Implemented, ", "))
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
		s, err := s3.New(s3.Options{DataDir: dataDir, Host: cfg.S3Host, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "dynamodb":
		s, err := dynamodb.New(dynamodb.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "sts":
		s, err := sts.New(sts.Options{DataDir: dataDir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "sqs":
		s, err := sqs.New(sqs.Options{DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "sns":
		s, err := sns.New(sns.Options{DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "kms":
		s, err := kms.New(kms.Options{DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "ssm":
		s, err := ssm.New(ssm.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "secretsmanager":
		s, err := secretsmanager.New(secretsmanager.Options{DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "logs":
		s, err := logs.New(logs.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "cloudwatch":
		s, err := cloudwatch.New(cloudwatch.Options{
			DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		if err == nil {
			st.cloudwatch = s // retained so the evaluator can be given a trace sink
		}
		return s, s, err
	case "stepfunctions":
		s, err := stepfunctions.New(stepfunctions.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		if err == nil {
			st.stepfunctions = s // retained so the engine can be given a trace sink
		}
		return s, s, err
	case "eventbridge":
		s, err := eventbridge.New(eventbridge.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "lambda":
		s, err := lambda.New(lambda.Options{DataDir: dataDir, Peers: dir, Logf: logf, IdleTimeout: cfg.LambdaIdleTimeout, QuietFunctions: cfg.LambdaQuiet, Runtimes: cfg.LambdaRuntimes, Endpoint: cfg.Endpoint, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		if err == nil {
			st.lambda = s // retained so its pollers can be given a trace sink
		}
		return s, s, err
	case "kinesis":
		s, err := kinesis.New(kinesis.Options{DataDir: dataDir, Peers: dir, Logf: logf, IAMMode: string(cfg.IAMMode), Identity: cfg.Identity})
		return s, s, err
	case "apigateway":
		s, err := apigateway.New(apigateway.Options{DataDir: dataDir, Peers: dir, Logf: logf, Identity: cfg.Identity})
		return s, s, err
	case "cloudformation":
		// CloudFormation provisions across every other service, so it is the
		// one service handed the whole gateway. It resolves at request time,
		// so construction order does not matter.
		s, err := cloudformation.New(cloudformation.Options{Identity: cfg.Identity,
			DataDir: dataDir, Gateway: st.gw, Peers: dir, Logf: logf, Endpoint: cfg.Endpoint,
		})
		return s, s, err
	case "iam":
		s, err := iam.New(iam.Options{DataDir: dataDir, Mode: cfg.IAMMode, Peers: dir, Logf: logf, Identity: cfg.Identity})
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
		res := s.iam.Authorize(r, gateway.Route(s.id, r))
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
	for _, c := range s.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.closers = nil
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
