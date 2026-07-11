package console

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ---- Lambda (REST-JSON, /2015-03-31) ----

type Function struct {
	Name       string
	ARN        string
	Runtime    string
	Handler    string
	Timeout    int
	MemorySize int
	Modified   string
	Env        map[string]string
	CodeSize   int64
	Version    string
	DLQ        string
	OnSuccess  string // async destination ARN
	OnFailure  string // async destination ARN
	Mappings   []Mapping
}

type Mapping struct {
	UUID      string
	SourceARN string
	BatchSize int
	State     string
}

// InvokeResult is one playground invocation's outcome.
type InvokeResult struct {
	Payload  string
	FnError  string
	Logs     string
	Duration string
	Status   int
}

func (b *backend) ListFunctions(ctx context.Context) ([]Function, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2015-03-31/functions", nil)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Functions []lambdaConfigWire `json:"Functions"`
	}
	json.Unmarshal(body, &out)
	fns := make([]Function, 0, len(out.Functions))
	for _, f := range out.Functions {
		fns = append(fns, f.toFunction())
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].Name < fns[j].Name })
	return fns, nil
}

type lambdaConfigWire struct {
	FunctionName string `json:"FunctionName"`
	FunctionArn  string `json:"FunctionArn"`
	Runtime      string `json:"Runtime"`
	Handler      string `json:"Handler"`
	Timeout      int    `json:"Timeout"`
	MemorySize   int    `json:"MemorySize"`
	LastModified string `json:"LastModified"`
	CodeSize     int64  `json:"CodeSize"`
	Version      string `json:"Version"`
	Environment  struct {
		Variables map[string]string `json:"Variables"`
	} `json:"Environment"`
	DeadLetterConfig struct {
		TargetArn string `json:"TargetArn"`
	} `json:"DeadLetterConfig"`
}

func (w lambdaConfigWire) toFunction() Function {
	return Function{
		Name: w.FunctionName, ARN: w.FunctionArn, Runtime: w.Runtime, Handler: w.Handler,
		Timeout: w.Timeout, MemorySize: w.MemorySize, Modified: w.LastModified,
		Env: w.Environment.Variables, CodeSize: w.CodeSize, Version: w.Version,
		DLQ: w.DeadLetterConfig.TargetArn,
	}
}

func (b *backend) GetFunction(ctx context.Context, name string) (*Function, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2015-03-31/functions/"+url.PathEscape(name), nil)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Configuration lambdaConfigWire `json:"Configuration"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	f := out.Configuration.toFunction()
	if f.Name == "" { // some responses inline the configuration at the top level
		var flat lambdaConfigWire
		if json.Unmarshal(body, &flat) == nil && flat.FunctionName != "" {
			f = flat.toFunction()
		}
	}

	// Event source mappings for this function.
	mreq, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2015-03-31/event-source-mappings?FunctionName="+url.QueryEscape(name), nil)
	if mb, err := b.do(mreq); err == nil {
		var mout struct {
			EventSourceMappings []struct {
				UUID           string `json:"UUID"`
				EventSourceArn string `json:"EventSourceArn"`
				BatchSize      int    `json:"BatchSize"`
				State          string `json:"State"`
			} `json:"EventSourceMappings"`
		}
		json.Unmarshal(mb, &mout)
		for _, m := range mout.EventSourceMappings {
			f.Mappings = append(f.Mappings, Mapping{UUID: m.UUID, SourceARN: m.EventSourceArn, BatchSize: m.BatchSize, State: m.State})
		}
	}

	// Async destinations (EventInvokeConfig): OnSuccess / OnFailure.
	ereq, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2019-09-25/functions/"+url.PathEscape(name)+"/event-invoke-config", nil)
	if eb, err := b.do(ereq); err == nil {
		var eout struct {
			DestinationConfig struct {
				OnSuccess struct {
					Destination string `json:"Destination"`
				} `json:"OnSuccess"`
				OnFailure struct {
					Destination string `json:"Destination"`
				} `json:"OnFailure"`
			} `json:"DestinationConfig"`
		}
		json.Unmarshal(eb, &eout)
		f.OnSuccess = eout.DestinationConfig.OnSuccess.Destination
		f.OnFailure = eout.DestinationConfig.OnFailure.Destination
	}
	return &f, nil
}

// LambdaRuntimeState is a function's live process state (doze extension).
type LambdaRuntimeState struct {
	Warm     bool
	Runners  int
	IdleSecs int
	SleepAt  int64 // unix seconds the warm pool will scale to zero; 0 = no countdown
}

// IdleLabel renders the idle window compactly, e.g. "10m", "90s".
func (s LambdaRuntimeState) IdleLabel() string {
	d := (time.Duration(s.IdleSecs) * time.Second).String()
	return strings.TrimSuffix(d, "0s") // "10m0s" -> "10m"; "45s" untouched
}

// Counting reports whether a live sleep countdown is running.
func (s LambdaRuntimeState) Counting() bool { return s.Warm && s.SleepAt > 0 }

// SleepLabel renders the time left until the process sleeps for the initial
// server render; the client then ticks it every second against SleepAt.
func (s LambdaRuntimeState) SleepLabel() string {
	if !s.Counting() {
		return ""
	}
	left := time.Until(time.Unix(s.SleepAt, 0))
	if left < 0 {
		left = 0
	}
	secs := int(left.Seconds())
	if secs >= 60 {
		return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%ds", secs)
}

// LambdaRuntime reads whether the function currently holds warm processes and
// the idle window after which they scale to zero. Best-effort: on any error it
// reports cold with the default 10m window.
func (b *backend) LambdaRuntime(ctx context.Context, name string) LambdaRuntimeState {
	st := LambdaRuntimeState{IdleSecs: 600}
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/2015-03-31/functions/"+url.PathEscape(name)+"/doze-runtime", nil)
	body, err := b.do(req)
	if err != nil {
		return st
	}
	var out struct {
		Warm               bool  `json:"Warm"`
		Runners            int   `json:"Runners"`
		IdleTimeoutSeconds int   `json:"IdleTimeoutSeconds"`
		SleepAtUnix        int64 `json:"SleepAtUnix"`
	}
	if json.Unmarshal(body, &out) == nil {
		st.Warm, st.Runners, st.SleepAt = out.Warm, out.Runners, out.SleepAtUnix
		if out.IdleTimeoutSeconds > 0 {
			st.IdleSecs = out.IdleTimeoutSeconds
		}
	}
	return st
}

func (b *backend) DeleteFunction(ctx context.Context, name string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", b.base+"/2015-03-31/functions/"+url.PathEscape(name), nil)
	_, err := b.do(req)
	return err
}

// Invoke runs a synchronous invocation with tail logs, timing it.
func (b *backend) Invoke(ctx context.Context, name, payload string) (*InvokeResult, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST",
		b.base+"/2015-03-31/functions/"+url.PathEscape(name)+"/invocations",
		bytes.NewReader([]byte(payload)))
	req.Header.Set("X-Amz-Log-Type", "Tail")
	start := time.Now()
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	res := &InvokeResult{
		Status:   resp.StatusCode,
		FnError:  resp.Header.Get("X-Amz-Function-Error"),
		Duration: elapsed.Round(time.Millisecond).String(),
		Payload:  prettyJSON(string(body)),
	}
	if res.Payload == "" {
		res.Payload = string(body)
	}
	if lr := resp.Header.Get("X-Amz-Log-Result"); lr != "" {
		if logs, err := base64.StdEncoding.DecodeString(lr); err == nil {
			res.Logs = string(logs)
		}
	}
	if resp.StatusCode/100 != 2 {
		return nil, &apiErr{status: resp.StatusCode, body: string(body)}
	}
	return res, nil
}

func (b *backend) DeleteMapping(ctx context.Context, uuid string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", b.base+"/2015-03-31/event-source-mappings/"+url.PathEscape(uuid), nil)
	_, err := b.do(req)
	return err
}
