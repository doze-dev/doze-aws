package stepfunctions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/peers"
)

// Distributed Map: every item as its own execution under a Map Run.

// stubS3 is a bucket in a map: GET and PUT by path-style key.
type stubS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newStubS3(t *testing.T, seed map[string]string) (*stubS3, peers.Directory) {
	t.Helper()
	s := &stubS3{objects: map[string][]byte{}}
	for k, v := range seed {
		s.objects[k] = []byte(v)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/")
		switch r.Method {
		case "GET":
			body, ok := s.objects[key]
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>gone</Message></Error>`))
				return
			}
			w.Write(body)
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			s.objects[key] = body
			w.Header().Set("ETag", `"x"`)
		}
	}))
	t.Cleanup(ts.Close)
	return s, peers.Static{"s3": peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}}
}

const distributedDef = `{"StartAt":"Each","States":{"Each":{"Type":"Map","ItemsPath":"$.items","MaxConcurrency":2,"ResultPath":"$.done","End":true,
  "ItemSelector":{"n.$":"$$.Map.Item.Value","i.$":"$$.Map.Item.Index"},
  "ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED","ExecutionType":"STANDARD"},
    "StartAt":"Double","States":{"Double":{"Type":"Pass","Parameters":{"twice.$":"States.MathAdd($.n, $.n)"},"End":true}}}}}}`

func TestDistributedMapRunsItemsAsExecutions(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "dm", distributedDef)
	startExecInput(t, s, "dm", "run", `{"items":[1,2,3,4,5]}`)
	out := waitSucceeded(t, s, "dm", "run")
	if !strings.Contains(out, `"done":[{"twice":2},{"twice":4},{"twice":6},{"twice":8},{"twice":10}]`) {
		t.Fatalf("output = %s", out)
	}
	// The run record and its children are reachable through the API.
	runs, _ := s.listMapRuns(context.Background(), map[string]any{"executionArn": execARN(awsident.Default(), "dm", "run")})
	list := runs.(map[string]any)["mapRuns"].([]any)
	if len(list) != 1 {
		t.Fatalf("mapRuns = %v", list)
	}
	arn := list[0].(map[string]any)["mapRunArn"].(string)
	if !strings.Contains(arn, ":mapRun:dm/run:") {
		t.Errorf("mapRunArn = %s", arn)
	}
	desc, aerr := s.describeMapRun(context.Background(), map[string]any{"mapRunArn": arn})
	if aerr != nil {
		t.Fatal(aerr)
	}
	d := desc.(map[string]any)
	counts := d["itemCounts"].(map[string]any)
	if d["status"] != "SUCCEEDED" || counts["succeeded"] != 5 || counts["total"] != 5 || d["maxConcurrency"] != 2 {
		t.Errorf("describe = %v", d)
	}
	children, _ := s.listExecutions(context.Background(), map[string]any{"mapRunArn": arn})
	if n := len(children.(map[string]any)["executions"].([]any)); n != 5 {
		t.Errorf("ListExecutions(mapRunArn) = %d executions, want 5", n)
	}
	plain, _ := s.listExecutions(context.Background(), map[string]any{"stateMachineArn": machineARN(awsident.Default(), "dm")})
	if n := len(plain.(map[string]any)["executions"].([]any)); n != 1 {
		t.Errorf("a plain ListExecutions should not show the children, got %d", n)
	}
	child := children.(map[string]any)["executions"].([]any)[0].(map[string]any)
	cd, _ := s.describeExecution(context.Background(), map[string]any{"executionArn": child["executionArn"]})
	if cd.(map[string]any)["mapRunArn"] != arn {
		t.Errorf("a child's DescribeExecution should carry mapRunArn: %v", cd)
	}
	// History on the parent names the run.
	e, _ := s.store.GetExecution("dm", "run")
	events, _, _ := s.store.HistoryPage(e.Key(), 0, 1000)
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	joined := strings.Join(types, " ")
	if !strings.Contains(joined, "MapStateEntered MapRunStarted MapStateStarted MapRunSucceeded MapStateSucceeded MapStateExited") {
		t.Errorf("history = %s", joined)
	}
}

func TestDistributedMapTolerance(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	proc := `"ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED"},"StartAt":"C","States":{
	  "C":{"Type":"Choice","Choices":[{"Variable":"$","NumericEquals":3,"Next":"Bad"}],"Default":"Ok"},
	  "Ok":{"Type":"Pass","End":true},"Bad":{"Type":"Fail","Error":"Item.Bad","Cause":"three"}}}`
	createMachine(t, s, "tol", `{"StartAt":"M","States":{"M":{"Type":"Map","ToleratedFailureCount":1,"End":true,`+proc+`}}}`)
	startExecInput(t, s, "tol", "run", `[1,2,3,4]`)
	if out := waitSucceeded(t, s, "tol", "run"); out != `[1,2,null,4]` {
		t.Errorf("tolerated output = %s", out)
	}
	createMachine(t, s, "strict", `{"StartAt":"M","States":{"M":{"Type":"Map","End":true,
	  "Catch":[{"ErrorEquals":["States.ExceedToleratedFailureThreshold"],"Next":"H"}],`+proc+`},
	  "H":{"Type":"Pass","End":true}}}`)
	startExecInput(t, s, "strict", "run", `[1,2,3,4]`)
	out := waitSucceeded(t, s, "strict", "run")
	if !strings.Contains(out, `"Error":"States.ExceedToleratedFailureThreshold"`) {
		t.Errorf("strict output = %s", out)
	}
	createMachine(t, s, "strict2", `{"StartAt":"M","States":{"M":{"Type":"Map","End":true,`+proc+`}}}`)
	startExecInput(t, s, "strict2", "run", `[3]`)
	if e := waitFailed(t, s, "strict2", "run"); e.Error != "States.ExceedToleratedFailureThreshold" {
		t.Errorf("uncaught = %q", e.Error)
	}
}

func TestDistributedMapReaderBatcherWriter(t *testing.T) {
	clock := &testClock{now: time.Now()}
	bucket, dir := newStubS3(t, map[string]string{
		"data/items.csv": "sku,qty\nA,1\nB,2\nC,3\n",
	})
	s := newTestServerPeers(t, t.TempDir(), clock, dir)
	defer s.Close()
	createMachine(t, s, "csv", `{"StartAt":"M","States":{"M":{"Type":"Map","End":true,
	  "ItemReader":{"Resource":"arn:aws:states:::s3:getObject","ReaderConfig":{"InputType":"CSV","CSVHeaderLocation":"FIRST_ROW"},
	    "Parameters":{"Bucket":"data","Key.$":"$.key"}},
	  "ItemBatcher":{"MaxItemsPerBatch":2,"BatchInput":{"job":"restock"}},
	  "ResultWriter":{"Resource":"arn:aws:states:::s3:putObject","Parameters":{"Bucket":"data","Prefix":"out"}},
	  "ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED","ExecutionType":"EXPRESS"},"StartAt":"Count","States":{
	    "Count":{"Type":"Pass","Parameters":{"job.$":"$.job","n.$":"States.ArrayLength($.Items)"},"End":true}}}}}}`)
	startExecInput(t, s, "csv", "run", `{"key":"items.csv"}`)
	out := waitSucceeded(t, s, "csv", "run")
	var v struct {
		MapRunArn           string
		ResultWriterDetails struct{ Bucket, Key string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.ResultWriterDetails.Bucket != "data" || !strings.HasSuffix(v.ResultWriterDetails.Key, "/manifest.json") {
		t.Fatalf("output = %s (%v)", out, err)
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	manifest := bucket.objects["data/"+v.ResultWriterDetails.Key]
	if !strings.Contains(string(manifest), `"SUCCEEDED"`) {
		t.Errorf("manifest = %s", manifest)
	}
	succeeded := bucket.objects["data/"+strings.Replace(v.ResultWriterDetails.Key, "manifest.json", "SUCCEEDED_0.json", 1)]
	// Three rows in batches of two: batch sizes 2 and 1, both carrying the job.
	if !strings.Contains(string(succeeded), `\"n\":2`) || !strings.Contains(string(succeeded), `\"n\":1`) || !strings.Contains(string(succeeded), `restock`) {
		t.Errorf("SUCCEEDED_0.json = %s", succeeded)
	}
	if _, found := bucket.objects["data/"+strings.Replace(v.ResultWriterDetails.Key, "manifest.json", "FAILED_0.json", 1)]; found {
		t.Error("no FAILED_0.json expected when nothing failed")
	}
}

func TestDistributedMapSurvivesRestartAndConcurrencyUpdate(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, dir, clock)
	createMachine(t, s, "slow", `{"StartAt":"M","States":{"M":{"Type":"Map","MaxConcurrency":1,"End":true,
	  "ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED"},"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":30,"Next":"P"},"P":{"Type":"Pass","End":true}}}}}}`)
	startExecInput(t, s, "slow", "run", `[1,2,3]`)
	var arn string
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("slow", "run")
		if e == nil || e.Exec.Root().MapRun == "" {
			return false
		}
		arn = e.Exec.Root().MapRun
		return true
	}, "the map run never started")
	waitFor(t, func() bool {
		mr, _ := s.store.GetMapRun(arn)
		return mr != nil && mr.Next == 1 && mr.Running == 1
	}, "with MaxConcurrency 1 exactly one child should be running")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := newTestServer(t, dir, clock)
	defer s2.Close()
	// Raise the concurrency mid-flight; the nudge launches the rest.
	if _, aerr := s2.updateMapRun(context.Background(), map[string]any{"mapRunArn": arn, "maxConcurrency": float64(5)}); aerr != nil {
		t.Fatal(aerr)
	}
	waitFor(t, func() bool {
		mr, _ := s2.store.GetMapRun(arn)
		if mr == nil || mr.Next != 3 {
			return false
		}
		// All three children must be in their Wait before the clock moves,
		// or the ones still to enter it would wake a minute too late.
		sleeping := 0
		for i := 0; i < 3; i++ {
			if c, _ := s2.store.GetExecution("slow", mapChildName(mr, i)); c != nil && c.Exec.Root().Status == "SLEEPING" {
				sleeping++
			}
		}
		return sleeping == 3
	}, "the raised concurrency never launched the remaining items")
	clock.Advance(time.Minute)
	if out := waitSucceeded(t, s2, "slow", "run"); out != `[1,2,3]` {
		t.Errorf("output after restart = %s", out)
	}
}

func TestDistributedMapStopAbortsChildren(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "halt", `{"StartAt":"M","States":{"M":{"Type":"Map","End":true,
	  "ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED"},"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"End":true}}}}}}`)
	startExecInput(t, s, "halt", "run", `[1,2]`)
	var arn string
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("halt", "run")
		if e != nil && e.Exec.Root().MapRun != "" {
			arn = e.Exec.Root().MapRun
			return true
		}
		return false
	}, "never started")
	if _, aerr := s.stopExecution(context.Background(), map[string]any{"executionArn": execARN(awsident.Default(), "halt", "run")}); aerr != nil {
		t.Fatal(aerr)
	}
	mr, _ := s.store.GetMapRun(arn)
	if mr.Status != "ABORTED" {
		t.Errorf("map run status = %s", mr.Status)
	}
	children, _ := s.store.ListExecutionsFor("halt")
	for _, c := range children {
		if c.MapRunARN != "" && c.Status != "ABORTED" {
			t.Errorf("child %s is %s, want ABORTED", c.Name, c.Status)
		}
	}
}
