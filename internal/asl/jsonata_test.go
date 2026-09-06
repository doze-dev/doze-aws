package asl

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The JSONata dialect, driven the way the engine drives it. runAll extends
// exec_test's run to Task, Parallel and Map: tasks are answered by the
// callback, children are advanced in turn, and joins are delivered when
// JoinReady says so.

type taskFn func(resource string, input json.RawMessage) TaskResult

func runAll(t *testing.T, d *Definition, ex *Exec, tasks taskFn) Effect {
	t.Helper()
	env := testEnv()
	env.NewToken = func() string { return "tok" }
	env.Rand = func() float64 { return 0.25 }
	for i := 0; i < 500; i++ {
		var f *Frame
		for _, cand := range ex.Frames {
			switch cand.Status {
			case FrameRunnable, FrameSleeping, FrameCalling, FrameParked:
				f = cand
			}
			if f != nil {
				break
			}
		}
		if f == nil {
			root := ex.Root()
			switch root.Status {
			case FrameDone:
				return EffDone{Frame: 1, Output: root.Output}
			case FrameFailed:
				return EffFail{Frame: 1, Failure: root.Failure}
			}
			t.Fatalf("no frame to drive; root is %s", root.Status)
		}
		var eff Effect
		var err error
		switch f.Status {
		case FrameRunnable:
			eff, _, err = Advance(d, ex, f, env)
		case FrameSleeping:
			env.Now = time.UnixMilli(f.WakeAt)
			eff, _, err = Wake(d, ex, f, env)
		case FrameCalling, FrameParked:
			if tasks == nil {
				t.Fatalf("frame %d called a task and the test provided none", f.ID)
			}
			eff, _, err = Deliver(d, ex, f, tasks(f.State, f.TaskInput), env)
		}
		if err != nil {
			t.Fatalf("interpreter error: %v", err)
		}
		switch eff.(type) {
		case EffDone, EffFail:
			settled := f
			if settled.Parent == 0 {
				return eff
			}
			parent := ex.Frame(settled.Parent)
			if res, ready := JoinReady(d, ex, parent); ready {
				AbandonSiblings(ex, parent)
				if _, _, err := Deliver(d, ex, parent, res, env); err != nil {
					t.Fatal(err)
				}
			} else {
				PromotePending(d, ex, parent)
			}
		}
	}
	t.Fatal("machine did not settle in 500 steps")
	return nil
}

func TestJSONataPassOutput(t *testing.T) {
	d := mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "P",
		"States": {"P": {"Type": "Pass",
			"Output": {
				"greeting": "{% 'hi ' & $states.input.name %}",
				"doubled": "{% $states.input.n * 2 %}",
				"literal": "{not an expression}",
				"nested": {"deep": ["{% $states.context.State.Name %}", 1, true]},
				"filtered": "{% $states.input.items[x > 1].x %}"
			},
			"End": true}}
	}`)
	eff := run(t, d, newExec(`{"name": "Ann", "n": 21, "items": [{"x": 1}, {"x": 2}, {"x": 3}]}`))
	wantDone(t, eff, `{"greeting": "hi Ann", "doubled": 42, "literal": "{not an expression}",
		"nested": {"deep": ["P", 1, true]}, "filtered": [2, 3]}`)

	// No Output: a Pass passes its input through; a bare expression Output
	// may yield any JSON type.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "P",
		"States": {"P": {"Type": "Pass", "Next": "Q"},
		           "Q": {"Type": "Pass", "Output": "{% $count($states.input.items) %}", "End": true}}}`)
	wantDone(t, run(t, d, newExec(`{"items": [1, 2, 3]}`)), `3`)
}

func TestJSONataLargeIntegerSurvives(t *testing.T) {
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "P",
		"States": {"P": {"Type": "Pass", "Output": {"id": "{% $states.input.id %}", "same": "{% $states.input %}"}, "End": true}}}`)
	done := run(t, d, newExec(`{"id": 9007199254740993}`)).(EffDone)
	if !strings.Contains(string(done.Output), `"id":9007199254740993`) {
		t.Fatalf("large integer mangled: %s", done.Output)
	}
}

func TestJSONataTaskArgumentsAndAssign(t *testing.T) {
	d := mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "Remember",
		"States": {
			"Remember": {"Type": "Pass", "Assign": {"customer": "{% $states.input.who %}", "n": 1}, "Next": "Call"},
			"Call": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
				"Arguments": {"title": "{% $states.input.title %}", "name": "{% $customer %}", "raw": "$customer"},
				"Assign": {"n": "{% $n + 1 %}", "price": "{% $states.result.price %}", "seen": "{% $n %}"},
				"Output": {"got": "{% $states.result %}", "in": "{% $states.input.title %}"},
				"Next": "Show"},
			"Show": {"Type": "Pass", "Output": {"n": "{% $n %}", "seen": "{% $seen %}", "price": "{% $price %}", "prev": "{% $states.input.got.price %}"}, "End": true}
		}
	}`)
	var called json.RawMessage
	eff := runAll(t, d, newExec(`{"who": "María", "title": "Doctor"}`), func(_ string, in json.RawMessage) TaskResult {
		called = in
		return TaskResult{Output: []byte(`{"price": 9.5}`)}
	})
	var got map[string]any
	json.Unmarshal(called, &got)
	if got["title"] != "Doctor" || got["name"] != "María" || got["raw"] != "$customer" {
		t.Fatalf("task input = %s", called)
	}
	// Assign evaluates against entry values: seen reads the old n.
	wantDone(t, eff, `{"n": 2, "seen": 1, "price": 9.5, "prev": 9.5}`)
}

func TestJSONataChoice(t *testing.T) {
	def := `{
		"QueryLanguage": "JSONata",
		"StartAt": "C",
		"States": {
			"C": {"Type": "Choice", "Choices": [
				{"Condition": "{% $states.input.n > 5 %}", "Next": "Big", "Assign": {"size": "big"}},
				{"Condition": %s, "Next": "Small"}
			], "Default": "Small", "Assign": {"size": "small", "checked": true}},
			"Big": {"Type": "Pass", "Output": {"size": "{% $size %}", "checked": "{% $checked %}"}, "End": true},
			"Small": {"Type": "Pass", "Output": {"size": "{% $size %}"}, "End": true}
		}
	}`
	d := mustDef(t, strings.Replace(def, "%s", `false`, 1))
	// The rule's Assign wins over the state's for the same name.
	wantDone(t, run(t, d, newExec(`{"n": 9}`)), `{"size": "big", "checked": true}`)
	wantDone(t, run(t, d, newExec(`{"n": 1}`)), `{"size": "small"}`)

	// A non-boolean Condition is an evaluation error, not "false".
	d = mustDef(t, strings.Replace(def, "%s", `"{% $states.input.n %}"`, 1))
	fail := wantFail(t, run(t, d, newExec(`{"n": 1}`)), ErrQueryEvaluationError)
	if !strings.Contains(fail.Cause, "boolean") {
		t.Fatalf("cause = %q", fail.Cause)
	}
}

func TestJSONataWaitSeconds(t *testing.T) {
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "W",
		"States": {"W": {"Type": "Wait", "Seconds": "{% $states.input.delay * 2 %}",
			"Assign": {"waited": "{% $states.input.delay %}"},
			"Output": {"done": true, "waited": "{% $states.input.delay %}"}, "End": true}}}`)
	ex := newExec(`{"delay": 30}`)
	f := ex.Root()
	eff, _, err := Advance(d, ex, f, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	sleep, ok := eff.(EffSleep)
	if !ok || sleep.Until != testNow.Add(60*time.Second).UnixMilli() {
		t.Fatalf("wanted a 60s sleep, got %#v", eff)
	}
	wantDone(t, run(t, d, ex), `{"done": true, "waited": 30}`)
	if string(f.Vars["waited"]) != "30" {
		t.Fatalf("Assign on Wait did not write: %v", f.Vars)
	}

	// Timestamp may be an expression too, and a past one completes at once.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "W",
		"States": {"W": {"Type": "Wait", "Timestamp": "{% $states.input.at %}", "End": true}}}`)
	wantDone(t, run(t, d, newExec(`{"at": "2020-01-01T00:00:00Z"}`)), `{"at": "2020-01-01T00:00:00Z"}`)
}

func TestJSONataFail(t *testing.T) {
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "F",
		"States": {"F": {"Type": "Fail", "Error": "{% $states.input.code %}", "Cause": "{% 'bad: ' & $states.input.why %}"}}}`)
	fail := wantFail(t, run(t, d, newExec(`{"code": "Custom.Nope", "why": "x"}`)), "Custom.Nope")
	if fail.Cause != "bad: x" {
		t.Fatalf("cause = %q", fail.Cause)
	}
	// Literal Error and Cause stay literal.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "F",
		"States": {"F": {"Type": "Fail", "Error": "Plain", "Cause": "text"}}}`)
	wantFail(t, run(t, d, newExec(`{}`)), "Plain")
}

func TestJSONataMapItemsAndSelector(t *testing.T) {
	d := mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "M",
		"States": {"M": {"Type": "Map",
			"Items": "{% $states.input.order.items %}",
			"ItemSelector": {"idx": "{% $states.context.Map.Item.Index %}", "item": "{% $states.context.Map.Item.Value %}", "order": "{% $states.input.order.id %}", "tag": "{% $tag %}"},
			"MaxConcurrency": "{% $states.input.par %}",
			"ItemProcessor": {"StartAt": "Each", "States": {"Each": {"Type": "Pass",
				"Assign": {"inner": "{% $states.input.idx %}"},
				"Output": "{% $states.input.order & '-' & $states.input.tag & ':' & $string($states.input.idx) & '=' & $states.input.item.name %}", "End": true}}},
			"Output": {"lines": "{% $states.result %}", "count": "{% $count($states.result) %}"},
			"Assign": {"total": "{% $count($states.result) %}"},
			"Next": "After"},
			"After": {"Type": "Pass", "Output": {"total": "{% $total %}", "inner": "{% $inner ? 'leaked' : 'scoped' %}", "lines": "{% $states.input.lines %}"}, "End": true}}
	}`)
	ex := newExec(`{"order": {"id": "o1", "items": [{"name": "a"}, {"name": "b"}, {"name": "c"}]}, "par": 2}`)
	ex.Root().Vars = map[string]json.RawMessage{"tag": json.RawMessage(`"t"`)}
	eff := runAll(t, d, ex, nil)
	// The iteration's Assign never reaches the parent scope.
	wantDone(t, eff, `{"total": 3, "inner": "scoped", "lines": ["o1-t:0=a", "o1-t:1=b", "o1-t:2=c"]}`)

	// Items that is not an array is an evaluation error, catchable.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "M",
		"States": {"M": {"Type": "Map", "Items": "{% $states.input.n %}",
			"ItemProcessor": {"StartAt": "E", "States": {"E": {"Type": "Pass", "End": true}}},
			"Catch": [{"ErrorEquals": ["States.QueryEvaluationError"], "Next": "C"}], "End": true},
			"C": {"Type": "Pass", "Output": "{% $states.input.Error %}", "End": true}}}`)
	wantDone(t, runAll(t, d, newExec(`{"n": 4}`), nil), `"States.QueryEvaluationError"`)
}

func TestJSONataParallelScopeAndOutput(t *testing.T) {
	d := mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "Set",
		"States": {
			"Set": {"Type": "Pass", "Assign": {"outer": "o"}, "Next": "P"},
			"P": {"Type": "Parallel",
				"Arguments": {"id": "{% $states.input.id %}", "from": "{% $outer %}"},
				"Branches": [
					{"StartAt": "A", "States": {"A": {"Type": "Pass", "Assign": {"mine": "a"}, "Output": {"a": "{% $states.input.from & $states.input.id %}"}, "End": true}}},
					{"StartAt": "B", "States": {"B": {"Type": "Pass", "Assign": {"mine": "b"}, "Output": {"b": "{% $outer %}"}, "End": true}}}
				],
				"Output": "{% $merge($states.result) %}",
				"Next": "After"},
			"After": {"Type": "Pass", "Output": {"merged": "{% $states.input %}", "mine": "{% $exists($mine) %}", "outer": "{% $outer %}"}, "End": true}
		}
	}`)
	eff := runAll(t, d, newExec(`{"id": 7}`), nil)
	wantDone(t, eff, `{"merged": {"a": "o7", "b": "o"}, "mine": false, "outer": "o"}`)
}

func TestJSONataCatchErrorOutput(t *testing.T) {
	d := mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "T",
		"States": {
			"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
				"Catch": [{"ErrorEquals": ["States.ALL"], "Next": "Handle",
					"Output": {"err": "{% $states.errorOutput.Error %}", "why": "{% $states.errorOutput.Cause %}", "orig": "{% $states.input.k %}"},
					"Assign": {"failedWith": "{% $states.errorOutput.Error %}"}}],
				"End": true},
			"Handle": {"Type": "Pass", "Output": {"in": "{% $states.input %}", "var": "{% $failedWith %}"}, "End": true}
		}
	}`)
	eff := runAll(t, d, newExec(`{"k": "v"}`), func(string, json.RawMessage) TaskResult {
		return TaskResult{Failure: &Failure{Name: "Custom.Boom", Cause: "kaboom"}}
	})
	wantDone(t, eff, `{"in": {"err": "Custom.Boom", "why": "kaboom", "orig": "v"}, "var": "Custom.Boom"}`)

	// With no Output on the catcher, the error output is the next input.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "T",
		"States": {"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
			"Catch": [{"ErrorEquals": ["States.ALL"], "Next": "H"}], "End": true},
			"H": {"Type": "Pass", "End": true}}}`)
	eff = runAll(t, d, newExec(`{}`), func(string, json.RawMessage) TaskResult {
		return TaskResult{Failure: &Failure{Name: "E", Cause: "c"}}
	})
	wantDone(t, eff, `{"Error": "E", "Cause": "c"}`)
}

func TestJSONataRuntimeErrorIsCatchable(t *testing.T) {
	// A field that does not exist yields no result, which is an error, not
	// null; a type error is one too. Both are States.QueryEvaluationError,
	// which a Task's Catch absorbs.
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "T",
		"States": {"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
			"Arguments": {"x": "{% $states.input.missing %}"},
			"Catch": [{"ErrorEquals": ["States.QueryEvaluationError"], "Next": "H"}], "End": true},
			"H": {"Type": "Pass", "Output": "{% $states.input.Error %}", "End": true}}}`)
	wantDone(t, runAll(t, d, newExec(`{}`), nil), `"States.QueryEvaluationError"`)

	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "P",
		"States": {"P": {"Type": "Pass", "Output": "{% $states.input.s + 1 %}", "End": true}}}`)
	wantFail(t, run(t, d, newExec(`{"s": "str"}`)), ErrQueryEvaluationError)
}

func TestJSONataFunctions(t *testing.T) {
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "P",
		"States": {"P": {"Type": "Pass", "Output": {
			"partition": "{% $partition([1,2,3,4,5], $states.input.chunk) %}",
			"range": "{% $range(0, 10, 5) %}",
			"down": "{% $range(3, 1, -1) %}",
			"hash": "{% $hash('abc', 'SHA-256') %}",
			"seeded": "{% $random(42) = $random(42) %}",
			"unseeded": "{% $random() < 1 and $random() >= 0 %}",
			"uuid": "{% $uuid() %}",
			"parsed": "{% $parse($states.input.json).a %}",
			"builtin": "{% $uppercase('x') & $string($count([1,2])) %}"
		}, "End": true}}}`)
	done := runAll(t, d, newExec(`{"chunk": 2, "json": "{\"a\": [1, 9007199254740993]}"}`), nil).(EffDone)
	var got map[string]any
	if err := json.Unmarshal(done.Output, &got); err != nil {
		t.Fatal(err)
	}
	check := func(key, want string) {
		t.Helper()
		raw, _ := json.Marshal(got[key])
		if string(raw) != want {
			t.Errorf("%s = %s, want %s", key, raw, want)
		}
	}
	check("partition", `[[1,2],[3,4],[5]]`)
	check("range", `[0,5,10]`)
	check("down", `[3,2,1]`)
	check("hash", `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`)
	check("seeded", `true`)
	check("unseeded", `true`)
	check("builtin", `"X2"`)
	if s, _ := got["uuid"].(string); len(s) != 36 || s[14] != '4' {
		t.Errorf("uuid = %v", got["uuid"])
	}
	if !strings.Contains(string(done.Output), `9007199254740993`) {
		t.Errorf("$parse lost a large integer: %s", done.Output)
	}

	// The intrinsic's argument rules apply under the new name.
	d = mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "P",
		"States": {"P": {"Type": "Pass", "Output": "{% $hash('x', 'CRC') %}", "End": true}}}`)
	fail := wantFail(t, run(t, d, newExec(`{}`)), ErrQueryEvaluationError)
	if !strings.Contains(fail.Cause, "$hash") {
		t.Fatalf("cause = %q", fail.Cause)
	}
}

func TestJSONataPerStateOverride(t *testing.T) {
	// A JSONPath machine upgrades one state; the rest keep their paths, and
	// a variable assigned in the JSONata state is readable by a later one.
	d := mustDef(t, `{
		"StartAt": "Old",
		"States": {
			"Old": {"Type": "Pass", "Parameters": {"n.$": "$.n", "tag": "old"}, "Next": "New"},
			"New": {"Type": "Pass", "QueryLanguage": "JSONata", "Assign": {"seen": "{% $states.input.n %}"},
				"Output": {"n": "{% $states.input.n + 1 %}", "tag": "{% $states.input.tag %}"}, "Next": "Newer"},
			"Newer": {"Type": "Pass", "QueryLanguage": "JSONata", "Output": {"n": "{% $states.input.n %}", "seen": "{% $seen %}"}, "Next": "Last"},
			"Last": {"Type": "Pass", "ResultPath": "$.copy", "End": true}
		}
	}`)
	wantDone(t, run(t, d, newExec(`{"n": 1}`)), `{"n": 2, "seen": 1, "copy": {"n": 2, "seen": 1}}`)
}

func TestJSONataAnalyserRefusals(t *testing.T) {
	for _, tc := range []struct{ name, def, want string }{
		{"compile error",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Output":"{% $states.input. %}","End":true}}}`,
			"JSONata expression"},
		{"unclosed expression",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Output":"{% $states.input","End":true}}}`,
			"must start with {% and end with %}"},
		{"Parameters on a JSONata state",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Parameters":{"a":1},"End":true}}}`,
			"Field 'Parameters' is not supported"},
		{"InputPath on a JSONata state",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","InputPath":"$.a","End":true}}}`,
			"Field 'InputPath' is not supported"},
		{"Output on a JSONPath state",
			`{"StartAt":"P","States":{"P":{"Type":"Pass","Output":{"a":1},"End":true}}}`,
			"Field 'Output' is not supported"},
		{"Variable in a JSONata rule",
			`{"QueryLanguage":"JSONata","StartAt":"C","States":{"C":{"Type":"Choice","Choices":[{"Variable":"$.a","StringEquals":"x","Next":"S"}],"Default":"S"},"S":{"Type":"Succeed"}}}`,
			"Field 'Variable' is not supported"},
		{"Condition in a JSONPath rule",
			`{"StartAt":"C","States":{"C":{"Type":"Choice","Choices":[{"Condition":"{% true %}","Next":"S"}],"Default":"S"},"S":{"Type":"Succeed"}}}`,
			"Field 'Condition' is not supported"},
		{"result before there is one",
			`{"QueryLanguage":"JSONata","StartAt":"T","States":{"T":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:f","Arguments":{"a":"{% $states.result %}"},"End":true}}}`,
			"$states.result is not available"},
		{"errorOutput outside Catch",
			`{"QueryLanguage":"JSONata","StartAt":"T","States":{"T":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:f","Output":"{% $states.errorOutput %}","End":true}}}`,
			"$states.errorOutput is only available"},
		{"downgrade to JSONPath",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","QueryLanguage":"JSONPath","End":true}}}`,
			"may not be JSONPath"},
		{"string Seconds on a JSONPath state",
			`{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":"{% 1 %}","End":true}}}`,
			"must be a number"},
		{"bad variable name",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Assign":{"x.y":1},"End":true}}}`,
			"not allowed in an identifier"},
		{"reserved variable",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Assign":{"states":1},"End":true}}}`,
			"reserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rep := ValidateDefinition([]byte(tc.def))
			if rep.OK() {
				t.Fatalf("accepted: %s", tc.def)
			}
			var all []string
			for _, diag := range rep.Diagnostics {
				all = append(all, diag.String())
			}
			if !strings.Contains(strings.Join(all, "; "), tc.want) {
				t.Fatalf("diagnostics = %q, want one to mention %q", all, tc.want)
			}
		})
	}

	// And what AWS accepts must be accepted: every JSONata field in place.
	mustDef(t, `{
		"QueryLanguage": "JSONata",
		"StartAt": "T",
		"States": {
			"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
				"Arguments": {"a": "{% $states.input %}"}, "TimeoutSeconds": "{% $t %}", "HeartbeatSeconds": 5,
				"Output": "{% $states.result %}", "Assign": {"v": "{% $states.result.x %}"},
				"Retry": [{"ErrorEquals": ["States.ALL"]}],
				"Catch": [{"ErrorEquals": ["States.ALL"], "Next": "M", "Output": "{% $states.errorOutput %}", "Assign": {"e": "{% $states.errorOutput.Error %}"}}],
				"Next": "M"},
			"M": {"Type": "Map", "Items": [1, "{% $v %}"], "ItemSelector": {"i": "{% $states.context.Map.Item.Index %}"}, "MaxConcurrency": "{% 2 %}",
				"ItemProcessor": {"StartAt": "W", "States": {"W": {"Type": "Wait", "Seconds": "{% 0 %}", "End": true}}},
				"Next": "C"},
			"C": {"Type": "Choice", "Choices": [{"Condition": "{% $v = 1 %}", "Next": "S", "Assign": {"z": 1}}], "Default": "F"},
			"S": {"Type": "Succeed", "Output": "{% $states.input %}"},
			"F": {"Type": "Fail", "Error": "{% $e %}", "Cause": "text"}
		}
	}`)
}

func TestJSONataTimeoutExpressionSurvivesRetry(t *testing.T) {
	d := mustDef(t, `{"QueryLanguage": "JSONata", "StartAt": "T",
		"States": {"T": {"Type": "Task", "Resource": "arn:aws:lambda:us-east-1:000000000000:function:f",
			"TimeoutSeconds": "{% $states.input.t %}",
			"Retry": [{"ErrorEquals": ["States.ALL"], "IntervalSeconds": 1, "MaxAttempts": 1}], "End": true}}}`)
	ex := newExec(`{"t": 45}`)
	f := ex.Root()
	env := testEnv()
	eff, _, err := Advance(d, ex, f, env)
	if err != nil {
		t.Fatal(err)
	}
	if call := eff.(EffCallTask); call.Deadline != testNow.Add(45*time.Second).UnixMilli() {
		t.Fatalf("deadline = %d", call.Deadline)
	}
	if _, _, err := Deliver(d, ex, f, TaskResult{Failure: &Failure{Name: "X"}}, env); err != nil {
		t.Fatal(err)
	}
	env.Now = time.UnixMilli(f.WakeAt)
	eff, _, err = Wake(d, ex, f, env)
	if err != nil {
		t.Fatal(err)
	}
	if call := eff.(EffCallTask); call.Deadline != env.Now.Add(45*time.Second).UnixMilli() {
		t.Fatalf("re-dispatch deadline = %d, want the same 45s from the new now", call.Deadline)
	}
}
