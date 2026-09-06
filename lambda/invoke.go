package lambda

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// invoke handles POST /functions/{name}/invocations.
func (s *Server) invoke(w http.ResponseWriter, r *http.Request, name, qualifier string) *awshttp.APIError {
	f, version, aerr := s.resolve(name, qualifier)
	if aerr != nil {
		return aerr
	}
	// The version that ran, as AWS reports it: the number behind an alias,
	// $LATEST otherwise.
	w.Header().Set("X-Amz-Executed-Version", version)
	// Reserved concurrency 0 means "throttle every invocation" in real Lambda,
	// not "use the default pool size".
	if f.ReservedConcurrency != nil && *f.ReservedConcurrency == 0 {
		return awshttp.Errf(429, "TooManyRequestsException", "function %s is throttled (reserved concurrency 0)", name)
	}
	payload, _ := io.ReadAll(io.LimitReader(r.Body, 6<<20))
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	invType := r.Header.Get("X-Amz-Invocation-Type")
	if invType == "" {
		invType = "RequestResponse"
	}

	if invType == "Event" {
		go s.invokeAsync(f, payload)
		w.WriteHeader(202)
		return nil
	}
	if invType == "DryRun" {
		w.WriteHeader(204)
		return nil
	}

	in := lambdaruntime.Input{Payload: payload, TraceID: r.Header.Get("X-Amzn-Trace-Id")}
	if qualifier != "" {
		in.InvokedARN = f.ARN() + ":" + qualifier
	}
	if cc := r.Header.Get("X-Amz-Client-Context"); cc != "" {
		// The header is base64 JSON; the function receives the JSON.
		if raw, err := base64.StdEncoding.DecodeString(cc); err == nil {
			in.ClientContext = string(raw)
		}
	}
	res, err := s.runInvokeInput(context.Background(), f, in)
	if err != nil {
		return awshttp.Errf(500, "ServiceException", "invoke: %v", err)
	}
	// The id the function saw, which its log lines carry — what a console
	// or a test uses to find this invocation's output.
	w.Header().Set("X-Amzn-RequestId", res.RequestID)
	if res.FunctionErr != "" {
		w.Header().Set("X-Amz-Function-Error", res.FunctionErr)
	}
	if r.Header.Get("X-Amz-Log-Type") == "Tail" {
		tail := res.Logs
		if len(tail) > 4096 {
			tail = tail[len(tail)-4096:]
		}
		w.Header().Set("X-Amz-Log-Result", base64.StdEncoding.EncodeToString(tail))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(res.Payload)
	return nil
}

// errThrottled is returned when a function's reserved concurrency is 0.
var errThrottled = errors.New("function throttled (reserved concurrency 0)")

// runInvoke ensures the function's runner exists and drives one invocation.
func (s *Server) runInvoke(ctx context.Context, f *Function, payload []byte) (lambdaruntime.Result, error) {
	return s.runInvokeInput(ctx, f, lambdaruntime.Input{Payload: payload})
}

func (s *Server) runInvokeInput(ctx context.Context, f *Function, in lambdaruntime.Input) (lambdaruntime.Result, error) {
	if f.ReservedConcurrency != nil && *f.ReservedConcurrency == 0 {
		return lambdaruntime.Result{}, errThrottled
	}
	// The backstop has to be at least as long as the runtime's own bound, or it
	// fires first and turns a function timeout into a transport error. Deriving
	// it from MaxWait keeps the two from drifting apart again.
	ctx, cancel := context.WithTimeout(ctx,
		lambdaruntime.MaxWait(time.Duration(f.Timeout)*time.Second))
	defer cancel()
	// If the pool was stopped underneath us by a concurrent restart (code/config
	// update), retry once against the freshly-created pool.
	res, err := s.runnerFor(f).InvokeInput(ctx, in)
	if errors.Is(err, lambdaruntime.ErrPoolClosed) {
		res, err = s.runnerFor(f).InvokeInput(ctx, in)
	}
	return res, err
}

// invokeAsync runs an Event invocation and routes failures to the DLQ /
// destination (best-effort).
func (s *Server) invokeAsync(f *Function, payload []byte) {
	var res lambdaruntime.Result
	var err error
	retries := 2 // AWS default for async invocations
	if f.MaxRetryAttempts != nil {
		retries = *f.MaxRetryAttempts
	}
	for attempt := 0; attempt <= retries; attempt++ { // 1 try + N retries
		res, err = s.runInvoke(context.Background(), f, payload)
		if err == nil && res.FunctionErr == "" {
			s.routeDestination(f, payload, res, true)
			return
		}
	}
	s.logf("lambda %s: async invocation failed after retries", f.Name)
	s.routeDestination(f, payload, res, false)
	if f.DeadLetterArn != "" {
		s.deliverToArn(f.DeadLetterArn, payload)
	}
}

// routeDestination delivers to the OnSuccess/OnFailure destination if set.
func (s *Server) routeDestination(f *Function, payload []byte, res lambdaruntime.Result, success bool) {
	if len(f.Destinations) == 0 {
		return
	}
	var dc struct {
		OnSuccess struct{ Destination string } `json:"OnSuccess"`
		OnFailure struct{ Destination string } `json:"OnFailure"`
	}
	if json.Unmarshal(f.Destinations, &dc) != nil {
		return
	}
	arn := dc.OnFailure.Destination
	if success {
		arn = dc.OnSuccess.Destination
	}
	if arn == "" {
		return
	}
	record, _ := json.Marshal(map[string]any{
		"requestContext":  map[string]any{"functionArn": f.ARN(), "condition": conditionOf(success)},
		"requestPayload":  json.RawMessage(payload),
		"responsePayload": json.RawMessage(orJSON(res.Payload)),
	})
	s.deliverToArn(arn, record)
}

func conditionOf(success bool) string {
	if success {
		return "Success"
	}
	return "RetriesExhausted"
}

func orJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// deliverToArn routes a payload to an SQS/SNS/Lambda ARN via peers.
func (s *Server) deliverToArn(arn string, payload []byte) {
	switch {
	case strings.Contains(arn, ":sqs:"):
		queue := arn[strings.LastIndex(arn, ":")+1:]
		if err := peercall.SQSSend(context.Background(), s.peers, queue, string(payload), nil); err != nil {
			s.logf("lambda: deliver to sqs %s: %v", queue, err)
		}
	case strings.Contains(arn, ":sns:"):
		if err := peercall.SNSPublish(context.Background(), s.peers, arn, string(payload)); err != nil {
			s.logf("lambda: deliver to sns: %v", err)
		}
	case strings.Contains(arn, ":lambda:"):
		fn := arn[strings.LastIndex(arn, ":")+1:]
		if err := peercall.LambdaInvokeAsync(context.Background(), s.peers, fn, payload); err != nil {
			s.logf("lambda: deliver to lambda %s: %v", fn, err)
		}
	}
}

// warnRuntime says at create time what the first invoke would otherwise
// discover: the interpreter this runtime needs is not here, or the package
// lacks the client it needs. AWS accepts the create either way, so this is a
// warning in the log, not a refusal — the invoke answers the same message.
func (s *Server) warnRuntime(name, runtime, codeDir string, command []string) {
	if len(command) > 0 {
		return // an explicit Command is the caller's own runner
	}
	if err := lambdaruntime.CheckRuntime(runtime, codeDir, s.interps); err != nil {
		s.logf("lambda %s: %v — the first invoke will fail with this", name, err)
	}
}

// loggingEnv turns a function's LoggingConfig into the variables the runtime
// clients read: AWS_LAMBDA_LOG_FORMAT and AWS_LAMBDA_LOG_LEVEL, unless the
// function's own environment already sets them.
func loggingEnv(f *Function) map[string]string {
	env := map[string]string{}
	for k, v := range f.Env {
		env[k] = v
	}
	if len(f.LoggingConfig) == 0 {
		return env
	}
	var lc struct {
		LogFormat           string `json:"LogFormat"`
		ApplicationLogLevel string `json:"ApplicationLogLevel"`
	}
	if json.Unmarshal(f.LoggingConfig, &lc) != nil {
		return env
	}
	if lc.LogFormat != "" {
		if _, set := env["AWS_LAMBDA_LOG_FORMAT"]; !set {
			env["AWS_LAMBDA_LOG_FORMAT"] = lc.LogFormat
		}
	}
	if lc.ApplicationLogLevel != "" {
		if _, set := env["AWS_LAMBDA_LOG_LEVEL"]; !set {
			env["AWS_LAMBDA_LOG_LEVEL"] = lc.ApplicationLogLevel
		}
	}
	return env
}

// runnerFor returns (creating if needed) the concurrency pool for a function.
// The pool's ceiling is the function's reserved concurrency, if set.
// Pools are keyed by name and version: version 2 and $LATEST run different
// code from different directories, so they cannot share a process.
func poolKey(f *Function) string {
	if f.Version == "" || f.Version == "$LATEST" {
		return f.Name
	}
	return f.Name + ":" + f.Version
}

func (s *Server) runnerFor(f *Function) *lambdaruntime.Pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := poolKey(f)
	if r := s.runners[key]; r != nil {
		return r
	}
	max := 0 // NewPool defaults it
	if f.ReservedConcurrency != nil {
		max = *f.ReservedConcurrency
	}
	sink := newLogSink(f.Name, s.peers, s.logf, s.echo)
	r := lambdaruntime.NewPool(lambdaruntime.Spec{
		Name:         f.Name,
		Handler:      f.Handler,
		Runtime:      f.Runtime,
		Command:      f.Command,
		Dir:          f.CodeDir,
		Env:          loggingEnv(f),
		Timeout:      time.Duration(f.Timeout) * time.Second,
		MemorySize:   f.MemorySize,
		Endpoints:    s.endpointEnv(),
		LogSink:      sink,
		Version:      f.Version,
		LayerDirs:    s.layerDirs(f),
		ShimDir:      s.shimDir,
		Interpreters: s.interps,
	}, max, s.logf)
	if s.idleTimeout > 0 {
		r.SetIdleTimeout(s.idleTimeout)
	}
	s.runners[key] = r
	s.sinks[key] = sink
	return r
}

// restartRunner stops any existing runner so the next invoke picks up new
// config/code.
func (s *Server) restartRunner(name string) {
	s.mu.Lock()
	if r := s.runners[name]; r != nil {
		r.Stop()
		delete(s.runners, name)
	}
	if k := s.sinks[name]; k != nil {
		k.Close()
		delete(s.sinks, name)
	}
	s.mu.Unlock()
}

// stopPools stops every pool for a function (the name) or one version
// ("name:3"): a deleted function or version must not keep a process.
func (s *Server) stopPools(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.runners {
		if k == key || strings.HasPrefix(k, key+":") {
			r.Stop()
			delete(s.runners, k)
			if sink := s.sinks[k]; sink != nil {
				sink.Close()
				delete(s.sinks, k)
			}
		}
	}
}

// endpointEnv builds the AWS_ENDPOINT_URL* variables injected into function
// processes so handler code using an AWS SDK reaches sibling services.
//
// These must be SDK-reachable HTTP endpoints — distinct from how the lambda
// SERVICE reaches peers (unix sockets, via s.peers). The general
// AWS_ENDPOINT_URL comes from the embedded listen address (s.endpoint); the
// per-service AWS_ENDPOINT_URL_<SVC> are passed through from this process's own
// environment (in the module topology the lambda process is handed real
// per-service domains). Peer BaseURLs are decorative in-process/unix hosts an
// SDK can't dial, so they are never used here.
func (s *Server) endpointEnv() map[string]string {
	env := map[string]string{}
	if s.endpoint != "" {
		env["AWS_ENDPOINT_URL"] = s.endpoint
	}
	for _, svc := range []string{"S3", "SQS", "SNS", "DYNAMODB", "KMS", "SSM", "SECRETSMANAGER", "STS", "EVENTBRIDGE", "LAMBDA"} {
		key := "AWS_ENDPOINT_URL_" + svc
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	return env
}

func readRand(b []byte) (int, error) { return rand.Read(b) }
