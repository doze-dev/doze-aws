package asl

import (
	"encoding/json"
	"testing"
	"time"
)

// The interpreter tests drive whole machines through Advance/Wake the way the
// engine will, asserting on the effects and the final output. testEnv pins
// the clock so Wait arithmetic is exact.

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func testEnv() Env { return Env{Now: testNow} }

func mustDef(t *testing.T, def string) *Definition {
	t.Helper()
	d, rep := ValidateDefinition([]byte(def))
	if !rep.OK() {
		t.Fatalf("definition invalid: %s", rep.Error())
	}
	return d
}

func newExec(input string) *Exec {
	return StartExec("arn:aws:states:us-east-1:000000000000:execution:m:e", "e",
		"arn:aws:states:us-east-1:000000000000:stateMachine:m", "m", "role",
		json.RawMessage(input), testNow)
}

// run drives the root frame until it suspends or terminates, waking sleeps by
// jumping the clock, and returns the last effect.
func run(t *testing.T, d *Definition, ex *Exec) Effect {
	t.Helper()
	env := testEnv()
	f := ex.Root()
	for i := 0; i < 100; i++ {
		var eff Effect
		var err error
		switch f.Status {
		case FrameRunnable:
			eff, _, err = Advance(d, ex, f, env)
		case FrameSleeping:
			env.Now = time.UnixMilli(f.WakeAt)
			eff, _, err = Wake(d, ex, f, env)
		default:
			t.Fatalf("run stuck on a %s frame", f.Status)
		}
		if err != nil {
			t.Fatalf("interpreter error: %v", err)
		}
		switch eff.(type) {
		case EffContinue:
			continue
		case EffSleep:
			continue
		default:
			return eff
		}
	}
	t.Fatal("machine did not settle in 100 steps")
	return nil
}

func wantDone(t *testing.T, eff Effect, wantOutput string) {
	t.Helper()
	done, ok := eff.(EffDone)
	if !ok {
		t.Fatalf("wanted EffDone, got %#v", eff)
	}
	var got, want any
	if err := json.Unmarshal(done.Output, &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(wantOutput), &want); err != nil {
		t.Fatalf("bad want: %v", err)
	}
	gotRaw, _ := json.Marshal(got)
	wantRaw, _ := json.Marshal(want)
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("output = %s, want %s", gotRaw, wantRaw)
	}
}

func wantFail(t *testing.T, eff Effect, name string) *Failure {
	t.Helper()
	fail, ok := eff.(EffFail)
	if !ok {
		t.Fatalf("wanted EffFail, got %#v", eff)
	}
	if fail.Failure.Name != name {
		t.Fatalf("failure name = %q, want %q (cause: %s)", fail.Failure.Name, name, fail.Failure.Cause)
	}
	return fail.Failure
}

func TestPassPipelineOrder(t *testing.T) {
	// ResultPath merges into the RAW input, not the post-InputPath effective
	// input — the classic emulator divergence.
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "InputPath": "$.inner",
			"ResultPath": "$.result", "End": true}}
	}`)
	eff := run(t, d, newExec(`{"inner": {"a": 1}, "keep": true}`))
	wantDone(t, eff, `{"inner": {"a": 1}, "keep": true, "result": {"a": 1}}`)
}

func TestPassResultAndParameters(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass",
			"Parameters": {"doubled.$": "$.n", "fixed": "x", "nested": {"deep.$": "$$.State.Name"}},
			"End": true}}
	}`)
	eff := run(t, d, newExec(`{"n": 7}`))
	wantDone(t, eff, `{"doubled": 7, "fixed": "x", "nested": {"deep": "P"}}`)

	// An explicit Result replaces everything.
	d = mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "Result": {"fixed": true}, "End": true}}
	}`)
	eff = run(t, d, newExec(`{"n": 7}`))
	wantDone(t, eff, `{"fixed": true}`)
}

func TestNullPathsAreDistinctFromAbsent(t *testing.T) {
	// "InputPath": null discards the document; absent means "$".
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "InputPath": null, "End": true}}
	}`)
	eff := run(t, d, newExec(`{"a": 1}`))
	wantDone(t, eff, `{}`)

	// "ResultPath": null discards the result and passes raw input through.
	d = mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "Result": {"dropped": true},
			"ResultPath": null, "End": true}}
	}`)
	eff = run(t, d, newExec(`{"a": 1}`))
	wantDone(t, eff, `{"a": 1}`)

	// "OutputPath": null yields the empty object.
	d = mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "OutputPath": null, "End": true}}
	}`)
	eff = run(t, d, newExec(`{"a": 1}`))
	wantDone(t, eff, `{}`)
}

func TestInputPathSelectingNothingIsNull(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "InputPath": "$.missing", "End": true}}
	}`)
	eff := run(t, d, newExec(`{"a": 1}`))
	wantDone(t, eff, `null`)
}

func TestResultPathOnNonObjectFails(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "Result": 1, "ResultPath": "$.out", "End": true}}
	}`)
	eff := run(t, d, newExec(`[1, 2]`))
	wantFail(t, eff, ErrResultPathMatchFailure)
}

func TestChoiceRouting(t *testing.T) {
	def := `{
		"StartAt": "C",
		"States": {
			"C": {"Type": "Choice", "Choices": [
				{"Variable": "$.n", "NumericGreaterThan": 10, "Next": "Big"},
				{"And": [
					{"Variable": "$.n", "NumericGreaterThanEquals": 5},
					{"Variable": "$.tag", "StringMatches": "v*.\\*"}
				], "Next": "Mid"}
			], "Default": "Small"},
			"Big": {"Type": "Pass", "Result": "big", "End": true},
			"Mid": {"Type": "Pass", "Result": "mid", "End": true},
			"Small": {"Type": "Pass", "Result": "small", "End": true}
		}
	}`
	d := mustDef(t, def)
	wantDone(t, run(t, d, newExec(`{"n": 11, "tag": ""}`)), `"big"`)
	wantDone(t, run(t, d, newExec(`{"n": 6, "tag": "v1.*"}`)), `"mid"`)
	wantDone(t, run(t, d, newExec(`{"n": 6, "tag": "v1.x"}`)), `"small"`)
	wantDone(t, run(t, d, newExec(`{"n": 1, "tag": ""}`)), `"small"`)
}

func TestChoiceMissingVariable(t *testing.T) {
	// A comparison over a missing variable is States.Runtime; IsPresent is the
	// one safe probe. A present variable of the wrong type is false, not an
	// error.
	d := mustDef(t, `{
		"StartAt": "C",
		"States": {
			"C": {"Type": "Choice", "Choices": [
				{"Variable": "$.missing", "StringEquals": "x", "Next": "Hit"}
			], "Default": "Hit"},
			"Hit": {"Type": "Succeed"}
		}
	}`)
	wantFail(t, run(t, d, newExec(`{}`)), ErrRuntime)

	d = mustDef(t, `{
		"StartAt": "C",
		"States": {
			"C": {"Type": "Choice", "Choices": [
				{"Variable": "$.missing", "IsPresent": true, "Next": "Present"},
				{"Variable": "$.n", "StringEquals": "x", "Next": "Present"}
			], "Default": "Absent"},
			"Present": {"Type": "Pass", "Result": "present", "End": true},
			"Absent": {"Type": "Pass", "Result": "absent", "End": true}
		}
	}`)
	wantDone(t, run(t, d, newExec(`{"n": 5}`)), `"absent"`)
}

func TestChoiceNoMatchNoDefault(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "C",
		"States": {
			"C": {"Type": "Choice", "Choices": [
				{"Variable": "$.n", "NumericEquals": 1, "Next": "S"}
			]},
			"S": {"Type": "Succeed"}
		}
	}`)
	wantFail(t, run(t, d, newExec(`{"n": 2}`)), ErrNoChoiceMatched)
}

func TestWaitSleepsAndWakes(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "W",
		"States": {
			"W": {"Type": "Wait", "Seconds": 30, "Next": "S"},
			"S": {"Type": "Pass", "Result": "woke", "End": true}
		}
	}`)
	ex := newExec(`{}`)
	f := ex.Root()
	eff, _, err := Advance(d, ex, f, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	sleep, ok := eff.(EffSleep)
	if !ok {
		t.Fatalf("wanted EffSleep, got %#v", eff)
	}
	if want := testNow.Add(30 * time.Second).UnixMilli(); sleep.Until != want {
		t.Fatalf("Until = %d, want %d", sleep.Until, want)
	}
	if f.Status != FrameSleeping || f.WakeAt != sleep.Until {
		t.Fatalf("frame not SLEEPING with WakeAt: %+v", f)
	}
	wantDone(t, run(t, d, ex), `"woke"`)
}

func TestWaitTimestampInThePastCompletesNow(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "W",
		"States": {"W": {"Type": "Wait", "Timestamp": "2020-01-01T00:00:00Z", "Next": "S"},
			"S": {"Type": "Succeed"}}
	}`)
	ex := newExec(`{"a": 1}`)
	eff, _, err := Advance(d, ex, ex.Root(), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eff.(EffContinue); !ok {
		t.Fatalf("a past Wait should continue immediately, got %#v", eff)
	}
}

func TestWaitSecondsPath(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "W",
		"States": {"W": {"Type": "Wait", "SecondsPath": "$.delay", "Next": "S"},
			"S": {"Type": "Succeed"}}
	}`)
	ex := newExec(`{"delay": 90}`)
	eff, _, err := Advance(d, ex, ex.Root(), testEnv())
	if err != nil {
		t.Fatal(err)
	}
	sleep, ok := eff.(EffSleep)
	if !ok {
		t.Fatalf("wanted EffSleep, got %#v", eff)
	}
	if want := testNow.Add(90 * time.Second).UnixMilli(); sleep.Until != want {
		t.Fatalf("Until = %d, want %d", sleep.Until, want)
	}
}

func TestFailState(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "F",
		"States": {"F": {"Type": "Fail", "Error": "Custom.Broke", "Cause": "on purpose"}}
	}`)
	fail := wantFail(t, run(t, d, newExec(`{}`)), "Custom.Broke")
	if fail.Cause != "on purpose" {
		t.Fatalf("cause = %q", fail.Cause)
	}

	d = mustDef(t, `{
		"StartAt": "F",
		"States": {"F": {"Type": "Fail", "ErrorPath": "$.err", "CausePath": "$.why"}}
	}`)
	fail = wantFail(t, run(t, d, newExec(`{"err": "E.FromPath", "why": "dynamic"}`)), "E.FromPath")
	if fail.Cause != "dynamic" {
		t.Fatalf("cause = %q", fail.Cause)
	}
}

func TestTaskStateAsksForTheCall(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "T",
		"States": {"T": {"Type": "Task",
			"Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
			"Parameters": {"n.$": "$.n"}, "TimeoutSeconds": 60, "End": true}}
	}`)
	ex := newExec(`{"n": 3}`)
	f := ex.Root()
	eff, _, err := Advance(d, ex, f, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	call, ok := eff.(EffCallTask)
	if !ok {
		t.Fatalf("wanted EffCallTask, got %#v", eff)
	}
	if string(call.Input) != `{"n":3}` || call.Token != "" {
		t.Fatalf("call = %+v", call)
	}
	if f.Status != FrameCalling || string(f.TaskInput) != `{"n":3}` {
		t.Fatalf("frame not CALLING with TaskInput: %+v", f)
	}
	if want := testNow.Add(60 * time.Second).UnixMilli(); call.Deadline != want || f.Deadline != want {
		t.Fatalf("deadline = %d, want %d", call.Deadline, want)
	}

	// Success delivery runs ResultSelector → ResultPath → OutputPath.
	eff, _, err = Deliver(d, ex, f, TaskResult{Output: []byte(`{"ok": true}`)}, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	wantDone(t, eff, `{"ok": true}`)
}

func TestTaskTokenPattern(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "T",
		"States": {"T": {"Type": "Task",
			"Resource": "arn:aws:states:::sqs:sendMessage.waitForTaskToken",
			"Parameters": {"QueueUrl": "q", "MessageBody": {"token.$": "$$.Task.Token"}},
			"End": true}}
	}`)
	ex := newExec(`{}`)
	f := ex.Root()
	env := testEnv()
	env.NewToken = func() string { return "tok-1" }
	eff, _, err := Advance(d, ex, f, env)
	if err != nil {
		t.Fatal(err)
	}
	call := eff.(EffCallTask)
	if call.Token != "tok-1" || f.Status != FrameParked {
		t.Fatalf("token task did not park: %+v / %+v", call, f)
	}
	// The minted token must be visible to the template — minted before
	// Parameters evaluation, or the callback pattern cannot work.
	if want := `{"MessageBody":{"token":"tok-1"},"QueueUrl":"q"}`; string(call.Input) != want {
		t.Fatalf("task input = %s, want %s", call.Input, want)
	}
}

func TestRetryThenCatch(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "T",
		"States": {
			"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
				"Retry": [{"ErrorEquals": ["MyError"], "IntervalSeconds": 2, "MaxAttempts": 2, "BackoffRate": 3}],
				"Catch": [{"ErrorEquals": ["States.ALL"], "ResultPath": "$.err", "Next": "Fallback"}],
				"End": true},
			"Fallback": {"Type": "Pass", "End": true}
		}
	}`)
	ex := newExec(`{"keep": 1}`)
	f := ex.Root()
	env := testEnv()

	if _, _, err := Advance(d, ex, f, env); err != nil {
		t.Fatal(err)
	}
	boom := TaskResult{Failure: &Failure{Name: "MyError", Cause: "attempt 1"}}

	// First failure: retry after IntervalSeconds.
	eff, _, err := Deliver(d, ex, f, boom, env)
	if err != nil {
		t.Fatal(err)
	}
	sleep := eff.(EffSleep)
	if want := testNow.Add(2 * time.Second).UnixMilli(); sleep.Until != want {
		t.Fatalf("first backoff = %d, want %d", sleep.Until, want)
	}
	if f.Status != FrameRetryWait || f.RetryCount() != 1 {
		t.Fatalf("frame = %+v", f)
	}

	// Wake re-dispatches the SAME task input.
	env.Now = time.UnixMilli(f.WakeAt)
	eff, _, err = Wake(d, ex, f, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, isCall := eff.(EffCallTask); !isCall || f.Status != FrameCalling {
		t.Fatalf("retry wake should re-dispatch, got %#v (%s)", eff, f.Status)
	}

	// Second failure: backoff * BackoffRate.
	eff, _, err = Deliver(d, ex, f, boom, env)
	if err != nil {
		t.Fatal(err)
	}
	sleep = eff.(EffSleep)
	if want := env.Now.Add(6 * time.Second).UnixMilli(); sleep.Until != want {
		t.Fatalf("second backoff = %d, want %d (interval * backoff)", sleep.Until, want)
	}

	// Third failure: retries exhausted, the Catch takes it.
	env.Now = time.UnixMilli(f.WakeAt)
	if _, _, err := Wake(d, ex, f, env); err != nil {
		t.Fatal(err)
	}
	eff, _, err = Deliver(d, ex, f, boom, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, isCont := eff.(EffContinue); !isCont || f.State != "Fallback" {
		t.Fatalf("catch should route to Fallback, got %#v (state %s)", eff, f.State)
	}
	eff, _, err = Advance(d, ex, f, env)
	if err != nil {
		t.Fatal(err)
	}
	wantDone(t, eff, `{"keep": 1, "err": {"Error": "MyError", "Cause": "attempt 1"}}`)
}

func TestUncaughtTaskFailureFailsTheFrame(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "T",
		"States": {"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
			"Catch": [{"ErrorEquals": ["SomethingElse"], "Next": "T2"}], "End": true},
			"T2": {"Type": "Succeed"}}
	}`)
	ex := newExec(`{}`)
	f := ex.Root()
	if _, _, err := Advance(d, ex, f, testEnv()); err != nil {
		t.Fatal(err)
	}
	eff, _, err := Deliver(d, ex, f, TaskResult{Failure: &Failure{Name: "States.TaskFailed", Cause: "x"}}, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	wantFail(t, eff, "States.TaskFailed")
}

// TestSnapshotRoundTrip is the frozen-contract test: an Exec marshalled
// mid-flight and unmarshalled into a fresh value continues identically to the
// original. Everything the interpreter needs must therefore live in the
// snapshot — the moment a field is forgotten, this diverges.
func TestSnapshotRoundTrip(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "A",
		"States": {
			"A": {"Type": "Pass", "Parameters": {"n.$": "$.n", "entered.$": "$$.State.Name"}, "Next": "W"},
			"W": {"Type": "Wait", "Seconds": 60, "Next": "B"},
			"B": {"Type": "Pass", "ResultPath": "$.b", "End": true}
		}
	}`)
	ex := newExec(`{"n": 42}`)
	env := testEnv()

	// Run to the Wait suspension.
	for {
		eff, _, err := Advance(d, ex, ex.Root(), env)
		if err != nil {
			t.Fatal(err)
		}
		if _, sleeping := eff.(EffSleep); sleeping {
			break
		}
	}

	// Snapshot, restore, and run both to completion.
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	restored := &Exec{}
	if err := json.Unmarshal(raw, restored); err != nil {
		t.Fatal(err)
	}

	finish := func(ex *Exec) json.RawMessage {
		env := testEnv()
		f := ex.Root()
		env.Now = time.UnixMilli(f.WakeAt)
		for i := 0; i < 50; i++ {
			var eff Effect
			var err error
			if f.Status == FrameSleeping {
				eff, _, err = Wake(d, ex, f, env)
			} else {
				eff, _, err = Advance(d, ex, f, env)
			}
			if err != nil {
				t.Fatal(err)
			}
			if done, finished := eff.(EffDone); finished {
				return done.Output
			}
		}
		t.Fatal("did not finish")
		return nil
	}

	a, b := finish(ex), finish(restored)
	if string(a) != string(b) {
		t.Fatalf("restored execution diverged:\n original: %s\n restored: %s", a, b)
	}
	wantDone(t, EffDone{Output: a}, `{"n": 42, "entered": "A", "b": {"n": 42, "entered": "A"}}`)
}

func TestIntrinsicInTemplate(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass",
			"Parameters": {"msg.$": "States.Format('n is {}', $.n)"}, "End": true}}
	}`)
	wantDone(t, run(t, d, newExec(`{"n": 1}`)), `{"msg": "n is 1"}`)

	d = mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass",
			"Parameters": {"msg.$": "States.NoSuchThing($.n)"}, "End": true}}
	}`)
	wantFail(t, run(t, d, newExec(`{"n": 1}`)), ErrIntrinsicFailure)
}

func TestContextObjectFields(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "Parameters": {
			"execName.$": "$$.Execution.Name",
			"machineId.$": "$$.StateMachine.Id",
			"execInput.$": "$$.Execution.Input",
			"retries.$": "$$.State.RetryCount"
		}, "End": true}}
	}`)
	eff := run(t, d, newExec(`{"seed": 9}`))
	wantDone(t, eff, `{
		"execName": "e",
		"machineId": "arn:aws:states:us-east-1:000000000000:stateMachine:m",
		"execInput": {"seed": 9},
		"retries": 0
	}`)
}

func TestLargeIntegerSurvivesThePipeline(t *testing.T) {
	d := mustDef(t, `{
		"StartAt": "P",
		"States": {"P": {"Type": "Pass", "Parameters": {"id.$": "$.id"}, "End": true}}
	}`)
	eff := run(t, d, newExec(`{"id": 9007199254740993}`))
	done := eff.(EffDone)
	if string(done.Output) != `{"id":9007199254740993}` {
		t.Fatalf("large integer mangled: %s", done.Output)
	}
}
