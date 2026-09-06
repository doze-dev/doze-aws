package stepfunctions

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

// The Lambda and SNS integrations, against stub peers that answer the way the
// real services do on the wire. What is pinned here is the error-name
// fidelity Retry and Catch depend on: a handler's errorType surfaces as
// itself, a Lambda API error as Lambda.<Code>, a transport failure as
// States.TaskFailed — and the shape of the result for the bare-ARN and the
// lambda:invoke spellings, which differ on AWS and so differ here.

// stubLambda serves the Invoke API. Each function name selects a behaviour.
func stubLambda(t *testing.T, calls *atomic.Int32) peers.Directory {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/2015-03-31/functions/"), "/invocations")
		w.Header().Set("Content-Type", "application/json")
		switch name {
		case "echo":
			w.Write([]byte(`{"got":` + string(body) + `}`))
		case "throws":
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
			w.Write([]byte(`{"errorType":"OrderRejected","errorMessage":"no stock"}`))
		case "throttled":
			// Two throttles, then success — the shape a Retry on
			// Lambda.TooManyRequestsException is written for.
			if calls.Load() < 3 {
				w.Header().Set("X-Amzn-Errortype", "TooManyRequestsException")
				w.WriteHeader(429)
				w.Write([]byte(`{"message":"Rate Exceeded."}`))
				return
			}
			w.Write([]byte(`{"ok":true}`))
		default:
			w.Header().Set("X-Amzn-Errortype", "ResourceNotFoundException")
			w.WriteHeader(404)
			w.Write([]byte(`{"Message":"Function not found: ` + name + `"}`))
		}
	}))
	t.Cleanup(ts.Close)
	return peers.Static{"lambda": peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}}
}

func waitFailed(t *testing.T, s *Server, machine, name string) *Execution {
	t.Helper()
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution(machine, name)
		return e != nil && e.Status == "FAILED"
	}, "execution never failed")
	e, _ := s.store.GetExecution(machine, name)
	return e
}

func TestLambdaBareARNReturnsThePayload(t *testing.T) {
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubLambda(t, &calls))
	defer s.Close()
	createMachine(t, s, "bare", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:lambda:us-east-1:000000000000:function:echo","End":true}}}`)
	startExecInput(t, s, "bare", "run", `{"a":1}`)
	if out := waitSucceeded(t, s, "bare", "run"); out != `{"got":{"a":1}}` {
		t.Errorf("bare-ARN output should be the handler's payload verbatim, got %s", out)
	}
}

func TestLambdaInvokeAPIWrapsThePayload(t *testing.T) {
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubLambda(t, &calls))
	defer s.Close()
	createMachine(t, s, "api", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:states:::lambda:invoke",
	  "Parameters":{"FunctionName":"arn:aws:lambda:us-east-1:000000000000:function:echo","Payload.$":"$"},
	  "OutputPath":"$.Payload","End":true}}}`)
	startExecInput(t, s, "api", "run", `{"a":1}`)
	if out := waitSucceeded(t, s, "api", "run"); out != `{"got":{"a":1}}` {
		t.Errorf("OutputPath $.Payload should unwrap the API result, got %s", out)
	}
	// Without OutputPath the result is API-shaped: Payload, StatusCode,
	// ExecutedVersion — what the CDK's LambdaInvoke task documents.
	createMachine(t, s, "api-raw", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:states:::lambda:invoke",
	  "Parameters":{"FunctionName":"echo","Payload":{"x":1}},"End":true}}}`)
	startExec(t, s, "api-raw", "run")
	var res struct {
		Payload         map[string]any
		StatusCode      int
		ExecutedVersion string
	}
	if err := json.Unmarshal([]byte(waitSucceeded(t, s, "api-raw", "run")), &res); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.ExecutedVersion != "$LATEST" || res.Payload["got"] == nil {
		t.Errorf("API-shaped result wrong: %+v", res)
	}
}

func TestLambdaHandlerErrorIsCaughtByItsType(t *testing.T) {
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubLambda(t, &calls))
	defer s.Close()
	createMachine(t, s, "throws", `{"StartAt":"T","States":{
	  "T":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:throws",
	       "Catch":[{"ErrorEquals":["OrderRejected"],"ResultPath":"$.err","Next":"Handled"}],"End":true},
	  "Handled":{"Type":"Pass","End":true}}}`)
	startExecInput(t, s, "throws", "run", `{"order":1}`)
	out := waitSucceeded(t, s, "throws", "run")
	var v struct {
		Err struct{ Error, Cause string } `json:"err"`
	}
	json.Unmarshal([]byte(out), &v)
	if v.Err.Error != "OrderRejected" || !strings.Contains(v.Err.Cause, "no stock") {
		t.Errorf("Catch should receive the handler's errorType and payload, got %s", out)
	}
	// Uncaught, the same error fails the execution under its own name.
	createMachine(t, s, "throws-uncaught", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:lambda:us-east-1:000000000000:function:throws","End":true}}}`)
	startExec(t, s, "throws-uncaught", "run")
	if e := waitFailed(t, s, "throws-uncaught", "run"); e.Error != "OrderRejected" {
		t.Errorf("uncaught handler error should fail as its errorType, got %q", e.Error)
	}
}

func TestLambdaAPIErrorsCarryTheLambdaPrefix(t *testing.T) {
	var calls atomic.Int32
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubLambda(t, &calls))
	defer s.Close()
	createMachine(t, s, "missing", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:lambda:us-east-1:000000000000:function:nonesuch","End":true}}}`)
	startExec(t, s, "missing", "run")
	if e := waitFailed(t, s, "missing", "run"); e.Error != "Lambda.ResourceNotFoundException" {
		t.Errorf("error = %q, want Lambda.ResourceNotFoundException", e.Error)
	}

	// A throttle is retried by the name the CDK's default Retry uses.
	calls.Store(0)
	createMachine(t, s, "throttled", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:lambda:us-east-1:000000000000:function:throttled",
	  "Retry":[{"ErrorEquals":["Lambda.TooManyRequestsException"],"IntervalSeconds":1,"MaxAttempts":3,"BackoffRate":1}],
	  "End":true}}}`)
	startExec(t, s, "throttled", "run")
	// Each retry waits on the clock; advance it until the third call lands.
	waitFor(t, func() bool {
		clock.Advance(2 * time.Second)
		e, _ := s.store.GetExecution("throttled", "run")
		return e != nil && e.Status == "SUCCEEDED"
	}, "the throttled call was never retried to success")
	if got := calls.Load(); got != 3 {
		t.Errorf("Lambda was called %d times, want 3 (two throttles, one success)", got)
	}
}

func TestLambdaUnwiredPeerIsTaskFailed(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock) // no peers at all
	defer s.Close()
	createMachine(t, s, "nopeer", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:lambda:us-east-1:000000000000:function:echo","End":true}}}`)
	startExec(t, s, "nopeer", "run")
	if e := waitFailed(t, s, "nopeer", "run"); e.Error != "States.TaskFailed" {
		t.Errorf("error = %q, want States.TaskFailed", e.Error)
	}
}

// stubSNS answers Publish over the Query protocol with a MessageId.
func stubSNS(t *testing.T, seen *atomic.Value) peers.Directory {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		seen.Store(r.Form)
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(`<PublishResponse><PublishResult><MessageId>msg-42</MessageId></PublishResult></PublishResponse>`))
	}))
	t.Cleanup(ts.Close)
	return peers.Static{"sns": peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}}
}

func TestSNSPublishIntegration(t *testing.T) {
	var seen atomic.Value
	clock := &testClock{now: time.Now()}
	s := newTestServerPeers(t, t.TempDir(), clock, stubSNS(t, &seen))
	defer s.Close()
	createMachine(t, s, "notify", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:states:::sns:publish",
	  "Parameters":{"TopicArn":"arn:aws:sns:us-east-1:000000000000:orders","Message.$":"$.text","Subject":"hi"},
	  "End":true}}}`)
	startExecInput(t, s, "notify", "run", `{"text":"shipped"}`)
	out := waitSucceeded(t, s, "notify", "run")
	if !strings.Contains(out, "msg-42") {
		t.Errorf("result should carry the MessageId, got %s", out)
	}
	form, _ := seen.Load().(interface{ Get(string) string })
	if form == nil || form.Get("Message") != "shipped" || form.Get("Subject") != "hi" ||
		!strings.HasSuffix(form.Get("TopicArn"), ":orders") {
		t.Errorf("Publish did not carry the parameters: %v", seen.Load())
	}
}
