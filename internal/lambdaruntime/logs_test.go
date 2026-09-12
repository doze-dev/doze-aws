package lambdaruntime

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

// recordingSink keeps every line with its attribution.
type recordingSink struct {
	mu      sync.Mutex
	lines   []sinkLine
	flushes int
}

type sinkLine struct {
	stream, rid, text string
}

func (s *recordingSink) Line(stream, rid string, _ time.Time, line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, sinkLine{stream, rid, string(line)})
}

func (s *recordingSink) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
}

func (s *recordingSink) snapshot() ([]sinkLine, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkLine(nil), s.lines...), s.flushes
}

func TestLineSplitterAttributesAndReassembles(t *testing.T) {
	sink := &recordingSink{}
	rid := ""
	s := newLineSplitter(newRingBuffer(1024), sink, "st", func() string { return rid })
	s.Write([]byte("init out"))
	s.Write([]byte("put\nsecond\r\n"))
	rid = "req-1"
	s.Write([]byte("during\n"))
	s.Write([]byte("tail without newline"))
	s.flushPartial()
	lines, _ := sink.snapshot()
	want := []sinkLine{{"st", "", "init output"}, {"st", "", "second"}, {"st", "req-1", "during"}, {"st", "req-1", "tail without newline"}}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
	// A line beyond the CloudWatch cap is split, never dropped.
	sink.lines = nil
	s.Write([]byte(strings.Repeat("x", maxLineBytes+10) + "\n"))
	lines, _ = sink.snapshot()
	if len(lines) != 2 || len(lines[0].text) != maxLineBytes || len(lines[1].text) != 10 {
		t.Errorf("a long line should split at the cap: got %d pieces", len(lines))
	}
}

func TestStreamNameIsAWSShaped(t *testing.T) {
	re := regexp.MustCompile(`^\d{4}/\d{2}/\d{2}/\[\$LATEST\][0-9a-f]{32}$`)
	if n := streamName("", time.Now()); !re.MatchString(n) {
		t.Errorf("stream name %q", n)
	}
	if n := streamName("3", time.Now()); !strings.Contains(n, "/[3]") {
		t.Errorf("a version names its stream: %q", n)
	}
}

const chattyBootstrap = `package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	fmt.Println("booting")
	fmt.Fprintln(os.Stderr, "stderr at init")
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		arn := resp.Header.Get("Lambda-Runtime-Invoked-Function-Arn")
		trace := resp.Header.Get("Lambda-Runtime-Trace-Id")
		cc := resp.Header.Get("Lambda-Runtime-Client-Context")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		fmt.Println("handling " + reqID)
		fmt.Fprintln(os.Stderr, "warn "+reqID)
		body := fmt.Sprintf(` + "`" + `{"rid":%q,"arn":%q,"trace":%q,"cc":%q,"version":%q,"group":%q,"stream":%q,"root":%q,"token":%q}` + "`" + `,
			reqID, arn, trace, cc, os.Getenv("AWS_LAMBDA_FUNCTION_VERSION"), os.Getenv("AWS_LAMBDA_LOG_GROUP_NAME"),
			os.Getenv("AWS_LAMBDA_LOG_STREAM_NAME"), os.Getenv("LAMBDA_TASK_ROOT"), os.Getenv("AWS_SESSION_TOKEN"))
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+reqID+"/response",
			"application/json", bytes.NewReader([]byte(body)))
	}
}
`

// TestOutputIsAttributedToItsInvocation is the contract the logs service
// builds on: init output carries no request id, each invocation's lines carry
// its own, the START/END/REPORT lines bracket them, and the sink is flushed
// once per invocation. It also pins the environment and headers a function
// sees, which the runtime clients read.
func TestOutputIsAttributedToItsInvocation(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildBootstrapSource(t, chattyBootstrap)
	sink := &recordingSink{}
	r := NewRunner(Spec{Name: "chatty", Runtime: "provided.al2023", Dir: dir, Timeout: 5 * time.Second,
		Version: "7", LogSink: sink}, t.Logf)
	defer r.Stop()

	var outs []map[string]string
	for i := 0; i < 2; i++ {
		res, err := r.InvokeInput(context.Background(), Input{Payload: []byte(`{}`),
			ClientContext: `{"custom":{"k":"v"}}`, InvokedARN: FunctionARN(awsident.Default(), "chatty") + ":7", TraceID: "Root=1-abc;Sampled=1"})
		if err != nil || res.FunctionErr != "" {
			t.Fatalf("invoke %d: %v %s", i, err, res.Payload)
		}
		var out map[string]string
		if err := json.Unmarshal(res.Payload, &out); err != nil {
			t.Fatalf("payload %s: %v", res.Payload, err)
		}
		if out["rid"] != res.RequestID {
			t.Errorf("Result.RequestID = %s, the function saw %s", res.RequestID, out["rid"])
		}
		outs = append(outs, out)
	}
	out := outs[0]
	if out["arn"] != FunctionARN(awsident.Default(), "chatty")+":7" || out["trace"] != "Root=1-abc;Sampled=1" || out["cc"] != `{"custom":{"k":"v"}}` {
		t.Errorf("headers seen by the function: %+v", out)
	}
	if out["version"] != "7" || out["group"] != "/aws/lambda/chatty" || out["stream"] != r.Stream() || out["root"] != dir || out["token"] != "test" {
		t.Errorf("environment seen by the function: %+v (stream %s)", out, r.Stream())
	}

	lines, flushes := sink.snapshot()
	if flushes != 2 {
		t.Errorf("flushes = %d, want one per invocation", flushes)
	}
	byRID := map[string][]string{}
	for _, l := range lines {
		if l.stream != r.Stream() {
			t.Errorf("line on stream %q, want %q", l.stream, r.Stream())
		}
		byRID[l.rid] = append(byRID[l.rid], l.text)
	}
	if got := byRID[""]; len(got) != 2 || got[0] != "booting" || got[1] != "stderr at init" {
		t.Errorf("init output = %q, want the two lines before the first fetch", got)
	}
	for _, o := range outs {
		rid := o["rid"]
		got := strings.Join(byRID[rid], "|")
		for _, want := range []string{"START RequestId: " + rid + " Version: 7", "handling " + rid, "warn " + rid, "END RequestId: " + rid, "REPORT RequestId: " + rid} {
			if !strings.Contains(got, want) {
				t.Errorf("lines for %s lack %q:\n%s", rid, want, got)
			}
		}
		if strings.Contains(got, outs[0]["rid"]) && rid != outs[0]["rid"] {
			t.Errorf("the second invocation's lines carry the first's id:\n%s", got)
		}
	}
}
