package console_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestConsoleStepFunctionsFlow walks the surface the way a person would:
// validate a definition, create the machine, start an execution, watch its
// history land, read the frozen definition, and stop one that is waiting.
//
// Executions run on the engine's own goroutine, so the history is polled the
// way the page polls it — until the status is terminal or a deadline passes —
// rather than asserted immediately.
func TestConsoleStepFunctionsFlow(t *testing.T) {
	h := newConsole(t)
	const role = "arn:aws:iam::000000000000:role/stepfunctions"
	definition := `{"StartAt":"Prep","States":{"Prep":{"Type":"Pass","Result":{"ready":true},"ResultPath":"$.prep","Next":"Done"},"Done":{"Type":"Succeed"}}}`

	// Validate: a broken definition gets its diagnostics, a good one a tick.
	bad := req(t, h, "POST", "/_console/sfn/validate", url.Values{"definition": {`{"StartAt":"Nope","States":{}}`}})
	if bad.Code != 200 || !strings.Contains(bad.Body.String(), "problem") {
		t.Fatalf("validate (bad): %d\n%s", bad.Code, bad.Body)
	}
	good := req(t, h, "POST", "/_console/sfn/validate", url.Values{"definition": {definition}})
	if good.Code != 200 || !strings.Contains(good.Body.String(), "valid") {
		t.Fatalf("validate (good): %d\n%s", good.Code, good.Body)
	}

	if loc := create(t, h, "/_console/sfn/create", url.Values{
		"name": {"order-flow"}, "type": {"STANDARD"}, "role": {role}, "definition": {definition},
	}); !strings.Contains(loc, "/sfn/order-flow") {
		t.Fatalf("create location = %q", loc)
	}
	page := req(t, h, "GET", "/_console/sfn/order-flow", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "No executions yet") {
		t.Fatalf("machine page: %d\n%s", page.Code, page.Body)
	}
	if def := req(t, h, "GET", "/_console/sfn/order-flow?tab=definition", nil); !strings.Contains(def.Body.String(), "ResultPath") {
		t.Fatalf("definition tab does not show the definition:\n%s", def.Body)
	}

	// Start lands on the execution page.
	loc := create(t, h, "/_console/sfn/order-flow/start", url.Values{"name": {"run-1"}, "input": {`{"orderId":"A-1"}`}})
	if !strings.Contains(loc, "/sfn/order-flow/execution/run-1") {
		t.Fatalf("start location = %q", loc)
	}
	var hist string
	deadline := time.Now().Add(5 * time.Second)
	for {
		hist = req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1/history", nil).Body.String()
		if strings.Contains(hist, `data-status="SUCCEEDED"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution never reached SUCCEEDED:\n%s", hist)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{"ExecutionStarted", "PassStateEntered", "Prep", "ExecutionSucceeded"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history is missing %q:\n%s", want, hist)
		}
	}
	// A finished execution's history region does not keep polling.
	if !strings.Contains(hist, "data-live-paused") {
		t.Errorf("finished execution's history should pause its poll:\n%s", hist)
	}
	// html/template renders quotes inside <pre> as &#34;.
	io := req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1?tab=io", nil).Body.String()
	if !strings.Contains(io, "A-1") || !strings.Contains(io, `&#34;ready&#34;: true`) {
		t.Errorf("io tab should show input and output:\n%s", io)
	}

	// The graph tab draws every state on the machine page, and colours the
	// ones the execution went through on the execution page.
	graph := req(t, h, "GET", "/_console/sfn/order-flow?tab=graph", nil).Body.String()
	for _, want := range []string{`<svg class="graph"`, `data-state="Prep"`, `data-state="Done"`, `gn-pass`, `gn-succeed`} {
		if !strings.Contains(graph, want) {
			t.Errorf("machine graph is missing %q:\n%s", want, graph)
		}
	}
	if strings.Contains(graph, "gn-succeeded") || strings.Contains(graph, "graph-legend") {
		t.Errorf("machine graph should carry no execution overlay:\n%s", graph)
	}
	overlaid := req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1?tab=graph", nil).Body.String()
	for _, want := range []string{`id="sfn-graph"`, `data-live-paused`, `gn-pass gn-start gn-succeeded" data-state="Prep"`, `gn-succeeded" data-state="Done"`, `id="sfn-history"`, `<tr class="" data-state="Prep"`} {
		if !strings.Contains(overlaid, want) {
			t.Errorf("execution graph is missing %q:\n%s", want, overlaid)
		}
	}
	// The polled partial answers 204 to its own hash and 200 to a stale one.
	live := req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1/graph", nil)
	if live.Code != 200 || !strings.Contains(live.Body.String(), `data-state="Prep"`) {
		t.Errorf("graph partial: %d\n%s", live.Code, live.Body)
	}
	hash := live.Header().Get("HX-Live-Hash")
	if rec := req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1/graph?h="+hash, nil); rec.Code != 204 {
		t.Errorf("unchanged graph should answer 204, got %d", rec.Code)
	}

	// The frozen definition survives an edit to the machine.
	edited := strings.Replace(definition, `"ready":true`, `"ready":false`, 1)
	if rec := req(t, h, "POST", "/_console/sfn/order-flow/definition", url.Values{"definition": {edited}}); rec.Code != 303 {
		t.Fatalf("update definition: %d\n%s", rec.Code, rec.Body)
	}
	frozen := req(t, h, "GET", "/_console/sfn/order-flow/execution/run-1?tab=definition", nil).Body.String()
	if !strings.Contains(frozen, `&#34;ready&#34;: true`) {
		t.Errorf("execution should show the definition it froze, not the edit:\n%s", frozen)
	}

	// A Wait long enough to still be running when stop arrives.
	waiting := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"End":true}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"slow"}, "role": {role}, "definition": {waiting}})
	create(t, h, "/_console/sfn/slow/start", url.Values{"name": {"run-1"}})
	if rec := req(t, h, "POST", "/_console/sfn/slow/execution/run-1/stop", url.Values{"error": {"Operator"}, "cause": {"enough"}}); rec.Code != 303 {
		t.Fatalf("stop: %d\n%s", rec.Code, rec.Body)
	}
	stopped := req(t, h, "GET", "/_console/sfn/slow/execution/run-1", nil).Body.String()
	if !strings.Contains(stopped, `data-status="ABORTED"`) || !strings.Contains(stopped, "Operator") {
		t.Errorf("stopped execution should read ABORTED with its error:\n%s", stopped)
	}

	// The list pane and the executions partial both know about it.
	list := req(t, h, "GET", "/_console/sfn/slow", nil).Body.String()
	if !strings.Contains(list, "order-flow") || !strings.Contains(list, "ABORTED") {
		t.Errorf("machine page should list every machine and this one's executions:\n%s", list)
	}
	if rec := req(t, h, "POST", "/_console/sfn/slow/delete", nil); rec.Code != 303 {
		t.Fatalf("delete: %d\n%s", rec.Code, rec.Body)
	}
}
