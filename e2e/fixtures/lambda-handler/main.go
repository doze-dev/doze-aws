// Command bootstrap is the e2e test fixture's Lambda Runtime Interface
// Client. It speaks the same wire protocol as AWS's real "provided.al2"
// bootstrap (see doze-aws's internal/lambdaruntime package doc): long-poll
// GET /2018-06-01/runtime/invocation/next, then POST the result to
// /2018-06-01/runtime/invocation/{id}/response.
//
// The "handler" itself just echoes the event back wrapped as
// {"echoed": <event>, "requestId": "<id>"} — enough for e2e/tests/lambda.spec.ts
// to assert on a real synchronous invoke's payload.
//
// It also appends one JSON line per invocation to invocations.log in its own
// working directory (the Lambda runtime sets the child's cwd to the
// function's code directory), so tests can observe invocations that have no
// other externally visible side effect — e.g. an async SQS-triggered invoke,
// where the console UI has nothing to show but "accepted".
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type invokeResult struct {
	Echoed    json.RawMessage `json:"echoed"`
	RequestID string          `json:"requestId"`
}

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	if api == "" {
		fmt.Fprintln(os.Stderr, "bootstrap: AWS_LAMBDA_RUNTIME_API not set")
		os.Exit(1)
	}
	base := "http://" + api + "/2018-06-01/runtime/invocation/"
	// No client timeout: GET .../next is a deliberate long-poll that blocks
	// until an invocation is queued.
	client := &http.Client{}

	for {
		id, event, err := next(client, base)
		if err != nil {
			// The runtime API listener closes when the function is stopped
			// (idle scale-to-zero, delete, config change). Back off instead
			// of busy-looping; the process gets killed from the outside.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		logInvocation(id, event)
		out, _ := json.Marshal(invokeResult{Echoed: event, RequestID: id})
		respond(client, base, id, out)
	}
}

// next blocks on the long-poll and returns the queued invocation's id and
// raw event body.
func next(client *http.Client, base string) (id string, event json.RawMessage, err error) {
	resp, err := client.Get(base + "next")
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	id = resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	if id == "" {
		return "", nil, fmt.Errorf("bootstrap: response missing Lambda-Runtime-Aws-Request-Id")
	}
	return id, json.RawMessage(body), nil
}

// respond posts the handler's result back to the runtime API.
func respond(client *http.Client, base, id string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, base+id+"/response", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// logInvocation appends one JSONL record per invocation so tests can confirm
// an invocation happened even when nothing else observes it (async/ESM
// invokes have no synchronous result to inspect).
func logInvocation(id string, event json.RawMessage) {
	f, err := os.OpenFile("invocations.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // best-effort; never fail the invocation over log I/O
	}
	defer f.Close()
	line, err := json.Marshal(map[string]any{
		"id": id, "receivedAt": time.Now().UTC().Format(time.RFC3339Nano), "event": event,
	})
	if err != nil {
		return
	}
	f.Write(line)
	f.Write([]byte("\n"))
}
