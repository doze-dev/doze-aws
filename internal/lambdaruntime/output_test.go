package lambdaruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowSink takes its time over each line, the way a sink under load does. It
// is what turns "the copy usually wins the race" into "the copy loses".
type slowSink struct {
	mu    sync.Mutex
	lines []sinkLine
}

func (s *slowSink) Line(stream, rid string, _ time.Time, line []byte) {
	time.Sleep(20 * time.Microsecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, sinkLine{stream, rid, string(line)})
}

func (s *slowSink) Flush() {}

func (s *slowSink) snapshot() []sinkLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkLine(nil), s.lines...)
}

const linesPerInvocation = 3000

// noisyBootstrap prints a few thousand numbered lines and THEN reports its
// result, so the last of them are still in the pipe when the result arrives.
const noisyBootstrap = `package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		rid := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		w := bufio.NewWriter(os.Stdout)
		for i := 0; i < 3000; i++ {
			fmt.Fprintf(w, "line %s %d\n", rid, i)
		}
		w.Flush()
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+rid+"/response",
			"application/json", bytes.NewReader([]byte("{}")))
	}
}
`

// "Invoke returned" means "the lines are there". Every line the function
// wrote before it reported its result is delivered, under that invocation,
// and before its END — at the moment Invoke returns, not eventually.
//
// It was eventually. The function's output came through a goroutine nothing
// waited for, so under load the tail of an invocation's output arrived after
// its END, or under the next invocation's id.
func TestOutputIsCompleteWhenInvokeReturns(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildBootstrapSource(t, noisyBootstrap)
	sink := &slowSink{}
	r := NewRunner(Spec{Name: "noisy", Runtime: "provided.al2023", Dir: dir, Timeout: 30 * time.Second, LogSink: sink}, t.Logf)
	defer r.Stop()

	for round := 0; round < 4; round++ {
		res, err := r.Invoke(context.Background(), []byte(`{}`))
		if err != nil || res.FunctionErr != "" {
			t.Fatalf("invoke %d: %v %s", round, err, res.Payload)
		}
		// Read at once: the claim is about this instant.
		var mine []string
		for _, l := range sink.snapshot() {
			if l.rid == res.RequestID {
				mine = append(mine, l.text)
			}
		}
		want := linesPerInvocation + 3 // START, the lines, END, REPORT
		if len(mine) != want {
			t.Fatalf("invocation %d: %d lines under its id when Invoke returned, want %d (last: %q)",
				round, len(mine), want, last(mine))
		}
		if !strings.HasPrefix(mine[0], "START RequestId: "+res.RequestID) {
			t.Errorf("invocation %d: first line %q", round, mine[0])
		}
		for i := 0; i < linesPerInvocation; i++ {
			if w := fmt.Sprintf("line %s %d", res.RequestID, i); mine[i+1] != w {
				t.Fatalf("invocation %d: line %d is %q, want %q", round, i, mine[i+1], w)
			}
		}
		if !strings.HasPrefix(mine[want-2], "END RequestId: ") || !strings.HasPrefix(mine[want-1], "REPORT RequestId: ") {
			t.Errorf("invocation %d: ends %q, %q — END and REPORT come after the function's output", round, mine[want-2], mine[want-1])
		}
		if tail := string(res.Logs); !strings.Contains(tail, fmt.Sprintf("line %s %d\n", res.RequestID, linesPerInvocation-1)) {
			t.Errorf("invocation %d: the log tail Invoke returned lacks the function's last line", round)
		}
	}
}

func last(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// dyingBootstrap takes one invocation, writes half a line, and exits.
const dyingBootstrap = `package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
	if err != nil { os.Exit(1) }
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	fmt.Print("dying mid-line")
	os.Exit(3)
}
`

// A process that dies leaving a line without its newline: the line is kept,
// the invocation fails with the exit, and the runner is usable afterwards.
func TestACrashMidLineLeavesTheRunnerUsable(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildBootstrapSource(t, dyingBootstrap)
	sink := &recordingSink{}
	r := NewRunner(Spec{Name: "dying", Runtime: "provided.al2023", Dir: dir, Timeout: 3 * time.Second, LogSink: sink}, t.Logf)
	defer r.Stop()

	invoke := func(what string) Result {
		t.Helper()
		done := make(chan Result, 1)
		go func() {
			res, _ := r.Invoke(context.Background(), []byte(`{}`))
			done <- res
		}()
		select {
		case res := <-done:
			return res
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: Invoke never returned — the runner is stuck", what)
			return Result{}
		}
	}
	first := invoke("the invocation the process died in")
	if first.FunctionErr == "" || !strings.Contains(string(first.Payload), "Runtime.ExitError") {
		t.Errorf("the invocation the process died in: %s %s, want Runtime.ExitError", first.FunctionErr, first.Payload)
	}
	// The runner restarts the process for the next one; it must get that far.
	invoke("the invocation after the crash")

	lines, _ := sink.snapshot()
	found := false
	for _, l := range lines {
		found = found || l.text == "dying mid-line"
	}
	if !found {
		t.Errorf("the half-written last line never reached the sink: %+v", lines)
	}
}
