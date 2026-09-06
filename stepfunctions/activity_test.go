package stepfunctions

import (
	"context"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Activity tests: a Task on an activity ARN parks on a token, a worker
// collects it with GetActivityTask, and the token path finishes the job.
// These drive the handlers directly for the fake clock and the store; the
// wire-level shape is sdk_activity_test.go.

func createActivity(t *testing.T, s *Server, name string) {
	t.Helper()
	if _, aerr := s.createActivity(context.Background(), map[string]any{"name": name}); aerr != nil {
		t.Fatalf("createActivity: %v", aerr)
	}
}

// pollActivity is one GetActivityTask call; nil when the poll came back empty.
func pollActivity(t *testing.T, ctx context.Context, s *Server, name, worker string) map[string]any {
	t.Helper()
	res, aerr := s.getActivityTask(ctx, map[string]any{"activityArn": activityARN(name), "workerName": worker})
	if aerr != nil {
		t.Fatalf("GetActivityTask: %v", aerr)
	}
	out := res.(map[string]any)
	if out["taskToken"] == nil {
		return nil
	}
	return out
}

func activityDef(name, extra string) string {
	return `{"StartAt":"Work","States":{"Work":{"Type":"Task","Resource":"` + activityARN(name) + `",` +
		extra + `"End":true}}}`
}

// shortPoll shortens the long-poll for the test's lifetime.
func shortPoll(t *testing.T, d time.Duration) {
	t.Helper()
	prev := activityPollTimeout
	activityPollTimeout = d
	t.Cleanup(func() { activityPollTimeout = prev })
}

// TestActivityWorkerRoundTrip: the input reaches the worker, and its
// SendTaskSuccess lands through ResultPath.
func TestActivityWorkerRoundTrip(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "approve")
	createMachine(t, s, "flow", activityDef("approve", `"ResultPath":"$.result",`))

	// The worker is already waiting when the task is scheduled: this is the
	// doorbell path, not the claim-on-arrival one.
	type polled struct {
		res  any
		aerr *awshttp.APIError
	}
	got := make(chan polled, 1)
	go func() {
		res, aerr := s.getActivityTask(context.Background(), map[string]any{
			"activityArn": activityARN("approve"), "workerName": "w1"})
		got <- polled{res, aerr}
	}()
	startExecInput(t, s, "flow", "run", `{"n":1}`)

	p := <-got
	if p.aerr != nil {
		t.Fatal(p.aerr)
	}
	task := p.res.(map[string]any)
	if task["taskToken"] == nil {
		t.Fatal("the poll came back empty")
	}
	if task["input"] != `{"n":1}` {
		t.Errorf("input = %v", task["input"])
	}
	if _, aerr := s.sendTaskSuccess(context.Background(), map[string]any{
		"taskToken": task["taskToken"], "output": `{"ok":true}`,
	}); aerr != nil {
		t.Fatal(aerr)
	}
	if out := waitSucceeded(t, s, "flow", "run"); out != `{"n":1,"result":{"ok":true}}` {
		t.Errorf("output = %s", out)
	}
	// The claimed task left the queue and the redeemed token is spent.
	if tasks, _ := s.store.ListActivityTasks("approve"); len(tasks) != 0 {
		t.Errorf("queue still holds %d tasks", len(tasks))
	}
	if ref, _ := s.store.GetToken(task["taskToken"].(string)); ref != nil {
		t.Error("the token is still redeemable after success")
	}
}

// TestActivityFailureRoutesThroughCatch: the worker's error name is what
// Catch matches.
func TestActivityFailureRoutesThroughCatch(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "review")
	createMachine(t, s, "veto", `{"StartAt":"Work","States":{
	  "Work":{"Type":"Task","Resource":"`+activityARN("review")+`",
	    "Catch":[{"ErrorEquals":["Rejected"],"Next":"No"}],"End":true},
	  "No":{"Type":"Pass","Result":"vetoed","End":true}}}`)
	startExec(t, s, "veto", "run")
	task := pollActivity(t, context.Background(), s, "review", "")
	if _, aerr := s.sendTaskFailure(context.Background(), map[string]any{
		"taskToken": task["taskToken"], "error": "Rejected", "cause": "nope",
	}); aerr != nil {
		t.Fatal(aerr)
	}
	if out := waitSucceeded(t, s, "veto", "run"); out != `"vetoed"` {
		t.Errorf("output = %s", out)
	}
}

// TestActivityHeartbeatTimeout: the same clock as a token task's — a
// heartbeat extends it, silence kills it with ActivityTimedOut.
func TestActivityHeartbeatTimeout(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "pulse")
	createMachine(t, s, "beat", activityDef("pulse", `"HeartbeatSeconds":30,`))
	startExec(t, s, "beat", "run")
	task := pollActivity(t, context.Background(), s, "pulse", "w")

	clock.Advance(20 * time.Second)
	if _, aerr := s.sendTaskHeartbeat(context.Background(), map[string]any{"taskToken": task["taskToken"]}); aerr != nil {
		t.Fatal(aerr)
	}
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("beat", "run")
		return e != nil && e.Exec.Root().HeartbeatAt >= clock.Now().UnixMilli()-1000
	}, "the heartbeat never landed")
	clock.Advance(35 * time.Second)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("beat", "run")
		return e != nil && e.Status == "FAILED"
	}, "the heartbeat timeout never fired")
	e, _ := s.store.GetExecution("beat", "run")
	if e.Error != asl.ErrHeartbeatTimeout {
		t.Errorf("error = %q", e.Error)
	}
	if types := historyTypes(t, s, e.Key()); !hasEvent(types, "ActivityTimedOut") {
		t.Errorf("history %v lacks ActivityTimedOut", types)
	}
}

// TestActivityPollReturnsEmpty: an idle activity answers the empty
// structure after the deadline, not an error and not a hang.
func TestActivityPollReturnsEmpty(t *testing.T) {
	shortPoll(t, 50*time.Millisecond)
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "idle")
	start := time.Now()
	if task := pollActivity(t, context.Background(), s, "idle", ""); task != nil {
		t.Fatalf("got a task from an idle activity: %v", task)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("the poll returned before its deadline")
	}
	// A cancelled request returns at once, too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shortPoll(t, time.Hour)
	if task := pollActivity(t, ctx, s, "idle", ""); task != nil {
		t.Fatal("got a task on a cancelled poll")
	}
}

// TestActivityQueueIsFIFO: two scheduled tasks come out in schedule order,
// and each is handed to exactly one poller.
func TestActivityQueueIsFIFO(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "line")
	createMachine(t, s, "q", activityDef("line", ""))
	startExecInput(t, s, "q", "first", `"a"`)
	waitFor(t, func() bool {
		tasks, _ := s.store.ListActivityTasks("line")
		return len(tasks) == 1
	}, "first never queued")
	startExecInput(t, s, "q", "second", `"b"`)
	waitFor(t, func() bool {
		tasks, _ := s.store.ListActivityTasks("line")
		return len(tasks) == 2
	}, "second never queued")

	one := pollActivity(t, context.Background(), s, "line", "")
	two := pollActivity(t, context.Background(), s, "line", "")
	if one["input"] != `"a"` || two["input"] != `"b"` {
		t.Errorf("order = %v, %v", one["input"], two["input"])
	}
	if tasks, _ := s.store.ListActivityTasks("line"); len(tasks) != 0 {
		t.Errorf("queue depth after two claims = %d", len(tasks))
	}
}

// TestActivityRestartKeepsQueuedTask: a task nobody polled survives a Close;
// the reopened stack hands it out and the token still redeems.
func TestActivityRestartKeepsQueuedTask(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, dir, clock)
	createActivity(t, s, "durable")
	createMachine(t, s, "keep", activityDef("durable", ""))
	startExecInput(t, s, "keep", "run", `{"job":7}`)
	waitFor(t, func() bool {
		tasks, _ := s.store.ListActivityTasks("durable")
		return len(tasks) == 1
	}, "the task never queued")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := newTestServerPeers(t, dir, clock, nil)
	defer s2.Close()
	task := pollActivity(t, context.Background(), s2, "durable", "w2")
	if task == nil || task["input"] != `{"job":7}` {
		t.Fatalf("after restart, poll = %v", task)
	}
	if _, aerr := s2.sendTaskSuccess(context.Background(), map[string]any{
		"taskToken": task["taskToken"], "output": `"done"`,
	}); aerr != nil {
		t.Fatal(aerr)
	}
	if out := waitSucceeded(t, s2, "keep", "run"); out != `"done"` {
		t.Errorf("output = %s", out)
	}
}

// TestMissingActivityFailsCatchably: scheduling on an activity that does
// not exist records ActivityScheduleFailed and fails the state with
// States.Runtime, which a Catch can take.
func TestMissingActivityFailsCatchably(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createMachine(t, s, "ghostly", `{"StartAt":"Work","States":{
	  "Work":{"Type":"Task","Resource":"`+activityARN("ghost")+`",
	    "Catch":[{"ErrorEquals":["States.Runtime"],"ResultPath":"$.err","Next":"Saved"}],"End":true},
	  "Saved":{"Type":"Pass","Parameters":{"got.$":"$.err.Error"},"End":true}}}`)
	startExec(t, s, "ghostly", "run")
	if out := waitSucceeded(t, s, "ghostly", "run"); out != `{"got":"States.Runtime"}` {
		t.Errorf("output = %s", out)
	}
	e, _ := s.store.GetExecution("ghostly", "run")
	if types := historyTypes(t, s, e.Key()); !hasEvent(types, "ActivityScheduleFailed") || hasEvent(types, "ActivityScheduled") {
		t.Errorf("history = %v", types)
	}
	// Uncaught, it fails the execution outright.
	createMachine(t, s, "doomed", activityDef("ghost", ""))
	startExec(t, s, "doomed", "run")
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("doomed", "run")
		return e != nil && e.Status == "FAILED"
	}, "the missing activity never failed the execution")
	e, _ = s.store.GetExecution("doomed", "run")
	if e.Error != asl.ErrRuntime {
		t.Errorf("error = %q, cause = %q", e.Error, e.Cause)
	}
}

// TestActivityTimeoutWhileQueued: a task that times out before any worker
// takes it leaves the queue with its token, so a late poller never receives
// a dead task.
func TestActivityTimeoutWhileQueued(t *testing.T) {
	shortPoll(t, 50*time.Millisecond)
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	createActivity(t, s, "slow")
	createMachine(t, s, "late", activityDef("slow", `"TimeoutSeconds":10,`))
	startExec(t, s, "late", "run")
	waitFor(t, func() bool {
		tasks, _ := s.store.ListActivityTasks("slow")
		return len(tasks) == 1
	}, "the task never queued")
	clock.Advance(20 * time.Second)
	waitFor(t, func() bool {
		e, _ := s.store.GetExecution("late", "run")
		return e != nil && e.Status == "FAILED"
	}, "the task timeout never fired")
	if tasks, _ := s.store.ListActivityTasks("slow"); len(tasks) != 0 {
		t.Errorf("the timed-out task is still queued")
	}
	if task := pollActivity(t, context.Background(), s, "slow", ""); task != nil {
		t.Errorf("a poller received the dead task: %v", task)
	}
}

// TestActivityResourceParsing: the resource table and the interpreter agree
// that an activity parks, and the explicit suffix is refused.
func TestActivityResourceParsing(t *testing.T) {
	arn := activityARN("x")
	tt, err := ParseResource(arn)
	if err != nil || tt.Kind != taskActivity || !tt.Parks {
		t.Errorf("ParseResource(%s) = %+v, %v", arn, tt, err)
	}
	if _, err := ParseResource(arn + ".waitForTaskToken"); err == nil {
		t.Error(".waitForTaskToken on an activity ARN was accepted")
	}
	if _, aerr := ParseResource("arn:aws:states:us-east-1:000000000000:activity:x"); aerr != nil {
		t.Error(aerr)
	}
	// GetActivityTask's own refusals.
	clock := &testClock{now: time.Now()}
	s := newTestServer(t, t.TempDir(), clock)
	defer s.Close()
	for _, c := range []struct{ arn, code string }{
		{"", "ValidationException"},
		{"not-an-arn", "InvalidArn"},
		{activityARN("nobody"), "ActivityDoesNotExist"},
	} {
		_, aerr := s.getActivityTask(context.Background(), map[string]any{"activityArn": c.arn})
		if aerr == nil || aerr.Code != c.code {
			t.Errorf("GetActivityTask(%q) = %v, want %s", c.arn, aerr, c.code)
		}
	}
}

func historyTypes(t *testing.T, s *Server, key string) []string {
	t.Helper()
	events, _, err := s.store.HistoryPage(key, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func hasEvent(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
