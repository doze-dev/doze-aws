package lambda

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// invoke handles POST /functions/{name}/invocations.
func (s *Server) invoke(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
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

	res, err := s.runInvoke(context.Background(), f, payload)
	if err != nil {
		return awshttp.Errf(500, "ServiceException", "invoke: %v", err)
	}
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

// runInvoke ensures the function's runner exists and drives one invocation.
func (s *Server) runInvoke(ctx context.Context, f *Function, payload []byte) (lambdaruntime.Result, error) {
	runner := s.runnerFor(f)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(f.Timeout+5)*time.Second)
	defer cancel()
	return runner.Invoke(ctx, payload)
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
		if err := peercall.SQSSend(s.peers, queue, string(payload), nil); err != nil {
			s.logf("lambda: deliver to sqs %s: %v", queue, err)
		}
	case strings.Contains(arn, ":sns:"):
		if err := peercall.SNSPublish(s.peers, arn, string(payload)); err != nil {
			s.logf("lambda: deliver to sns: %v", err)
		}
	case strings.Contains(arn, ":lambda:"):
		fn := arn[strings.LastIndex(arn, ":")+1:]
		if err := peercall.LambdaInvokeAsync(s.peers, fn, payload); err != nil {
			s.logf("lambda: deliver to lambda %s: %v", fn, err)
		}
	}
}

// runnerFor returns (creating if needed) the concurrency pool for a function.
// The pool's ceiling is the function's reserved concurrency, if set.
func (s *Server) runnerFor(f *Function) *lambdaruntime.Pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.runners[f.Name]; r != nil {
		return r
	}
	max := 0 // NewPool defaults it
	if f.ReservedConcurrency != nil {
		max = *f.ReservedConcurrency
	}
	r := lambdaruntime.NewPool(lambdaruntime.Spec{
		Name:      f.Name,
		Handler:   f.Handler,
		Runtime:   f.Runtime,
		Command:   f.Command,
		Dir:       f.CodeDir,
		Env:       f.Env,
		Timeout:   time.Duration(f.Timeout) * time.Second,
		Endpoints: s.endpointEnv(),
	}, max, s.logf)
	s.runners[f.Name] = r
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
	s.mu.Unlock()
}

// endpointEnv builds the AWS_ENDPOINT_URL* variables handlers use to reach
// sibling services.
func (s *Server) endpointEnv() map[string]string {
	env := map[string]string{}
	if s.endpoint != "" {
		env["AWS_ENDPOINT_URL"] = s.endpoint
	}
	for _, svc := range []string{"s3", "sqs", "sns", "dynamodb", "kms", "ssm", "secretsmanager", "sts", "eventbridge", "lambda"} {
		if ep, ok := s.peers.Endpoint(svc); ok {
			env["AWS_ENDPOINT_URL_"+strings.ToUpper(strings.ReplaceAll(svc, "-", "_"))] = ep.BaseURL
		}
	}
	return env
}

func readRand(b []byte) (int, error) { return rand.Read(b) }
