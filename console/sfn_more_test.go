package console_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The second half of the Step Functions surface: versions and aliases,
// Express and TestState, redrive, Map Runs, activities. Each test walks one
// panel the way a person would, through the console's own routes.

const sfnTestRole = "arn:aws:iam::000000000000:role/stepfunctions"

// waitHistory polls an execution's history partial until it says what the
// caller waits for — the way the page polls it — or gives up.
func waitHistory(t *testing.T, h http.Handler, machine, exec, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		hist := req(t, h, "GET", "/_console/sfn/"+machine+"/execution/"+exec+"/history", nil).Body.String()
		if strings.Contains(hist, want) {
			return hist
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution %s/%s never showed %q:\n%s", machine, exec, want, hist)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConsoleStepFunctionsVersionsAndAliases(t *testing.T) {
	h := newConsole(t)
	definition := `{"StartAt":"P","States":{"P":{"Type":"Pass","Result":{"v":1},"End":true}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"versioned"}, "role": {sfnTestRole}, "definition": {definition}})

	// The tab starts empty and the alias form says why it is disabled.
	tab := req(t, h, "GET", "/_console/sfn/versioned?tab=versions", nil).Body.String()
	if !strings.Contains(tab, "No versions yet") || !strings.Contains(tab, "publish a version first") {
		t.Fatalf("empty versions tab:\n%s", tab)
	}

	// Publish freezes the definition; the table shows the description.
	if loc := create(t, h, "/_console/sfn/versioned/publish", url.Values{"description": {"first cut"}}); !strings.Contains(loc, "tab=versions") {
		t.Fatalf("publish location = %q", loc)
	}
	tab = req(t, h, "GET", "/_console/sfn/versioned?tab=versions", nil).Body.String()
	if !strings.Contains(tab, ">v1<") || !strings.Contains(tab, "first cut") {
		t.Fatalf("versions tab after publish:\n%s", tab)
	}
	// Clicking a version describes the version ARN: the frozen definition.
	edited := strings.Replace(definition, `"v":1`, `"v":2`, 1)
	req(t, h, "POST", "/_console/sfn/versioned/definition", url.Values{"definition": {edited}})
	detail := req(t, h, "GET", "/_console/sfn/versioned/version/1", nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), `&#34;v&#34;: 1`) || !strings.Contains(detail.Body.String(), ":versioned:1") {
		t.Fatalf("version detail should be the frozen definition: %d\n%s", detail.Code, detail.Body)
	}
	create(t, h, "/_console/sfn/versioned/publish", url.Values{"description": {"second cut"}})

	// An alias on v1, then split across both.
	if loc := create(t, h, "/_console/sfn/versioned/alias/create", url.Values{"name": {"PROD"}, "v1": {"1"}, "description": {"what callers pin"}}); !strings.Contains(loc, "tab=versions") {
		t.Fatalf("alias create location = %q", loc)
	}
	tab = req(t, h, "GET", "/_console/sfn/versioned?tab=versions", nil).Body.String()
	if !strings.Contains(tab, "PROD") || !strings.Contains(tab, "v1 100%") || !strings.Contains(tab, "what callers pin") {
		t.Fatalf("aliases table after create:\n%s", tab)
	}
	if rec := req(t, h, "POST", "/_console/sfn/versioned/alias/PROD/update", url.Values{"v1": {"1"}, "w1": {"70"}, "v2": {"2"}, "w2": {"30"}}); rec.Code != 303 {
		t.Fatalf("alias update: %d\n%s", rec.Code, rec.Body)
	}
	tab = req(t, h, "GET", "/_console/sfn/versioned?tab=versions", nil).Body.String()
	if !strings.Contains(tab, "v1 70% · v2 30%") || !strings.Contains(tab, "what callers pin") {
		t.Fatalf("routing edit should change the split and keep the description:\n%s", tab)
	}
	// Weights that do not sum to 100 are the wire's refusal, not a redirect.
	if rec := req(t, h, "POST", "/_console/sfn/versioned/alias/PROD/update", url.Values{"v1": {"1"}, "w1": {"70"}, "v2": {"2"}, "w2": {"70"}}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "sum to 100") {
		t.Fatalf("bad weights: %d\n%s", rec.Code, rec.Body)
	}

	// The Start tab offers the alias and the versions; starting through the
	// alias lands on an execution that says so.
	start := req(t, h, "GET", "/_console/sfn/versioned?tab=start", nil).Body.String()
	for _, want := range []string{`<option value="PROD">`, `<option value="1">`, `<option value="2">`} {
		if !strings.Contains(start, want) {
			t.Errorf("start tab is missing %q:\n%s", want, start)
		}
	}
	loc := create(t, h, "/_console/sfn/versioned/start", url.Values{"name": {"via-alias"}, "target": {"PROD"}})
	if !strings.Contains(loc, "/execution/via-alias") {
		t.Fatalf("start via alias location = %q", loc)
	}
	waitHistory(t, h, "versioned", "via-alias", `data-status="SUCCEEDED"`)
	page := req(t, h, "GET", "/_console/sfn/versioned/execution/via-alias", nil).Body.String()
	if !strings.Contains(page, "alias PROD") {
		t.Errorf("execution page should say it started via the alias:\n%s", page)
	}
	loc = create(t, h, "/_console/sfn/versioned/start", url.Values{"name": {"via-version"}, "target": {"1"}})
	waitHistory(t, h, "versioned", "via-version", `data-status="SUCCEEDED"`)
	if io := req(t, h, "GET", "/_console/sfn/versioned/execution/via-version?tab=io", nil).Body.String(); !strings.Contains(io, `&#34;v&#34;: 1`) {
		t.Errorf("an execution of version 1 should run the frozen definition:\n%s", io)
	}

	// A version an alias routes to cannot go; the alias can, then the version.
	if rec := req(t, h, "POST", "/_console/sfn/versioned/version/2/delete", nil); rec.Code != 400 {
		t.Fatalf("deleting a routed version should be refused: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, h, "POST", "/_console/sfn/versioned/alias/PROD/delete", nil); rec.Code != 303 {
		t.Fatalf("alias delete: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, h, "POST", "/_console/sfn/versioned/version/2/delete", nil); rec.Code != 303 {
		t.Fatalf("version delete: %d\n%s", rec.Code, rec.Body)
	}
	tab = req(t, h, "GET", "/_console/sfn/versioned?tab=versions", nil).Body.String()
	if strings.Contains(tab, ">v2<") || strings.Contains(tab, ":versioned:PROD&#34;") || !strings.Contains(tab, ">v1<") {
		t.Fatalf("tab after deletes:\n%s", tab)
	}
}

func TestConsoleStepFunctionsExpressAndTestState(t *testing.T) {
	h := newConsole(t)
	definition := `{"StartAt":"Prep","States":{"Prep":{"Type":"Pass","Result":{"ready":true},"ResultPath":"$.prep","Next":"Done"},"Done":{"Type":"Succeed"}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"fast"}, "type": {"EXPRESS"}, "role": {sfnTestRole}, "definition": {definition}})

	// No executions list for Express, and the Start tab is the sync form.
	page := req(t, h, "GET", "/_console/sfn/fast", nil).Body.String()
	if !strings.Contains(page, "Express executions leave no record") || strings.Contains(page, `id="sfn-executions"`) {
		t.Fatalf("express machine page:\n%s", page)
	}
	start := req(t, h, "GET", "/_console/sfn/fast?tab=start", nil).Body.String()
	if !strings.Contains(start, "/sfn/fast/start-sync") || !strings.Contains(start, "Run synchronously") {
		t.Fatalf("express start tab should post to start-sync:\n%s", start)
	}
	// The synchronous run renders its result in place: status, output, billing.
	rec := req(t, h, "POST", "/_console/sfn/fast/start-sync", url.Values{"name": {"sync-1"}, "input": {`{"orderId":"X-9"}`}})
	if rec.Code != 200 {
		t.Fatalf("start-sync: %d\n%s", rec.Code, rec.Body)
	}
	for _, want := range []string{`data-status="SUCCEEDED"`, `&#34;ready&#34;: true`, "X-9", "ms · ", "leave no record", ":express:fast:sync-1:"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("sync result is missing %q:\n%s", want, rec.Body)
		}
	}
	// A plain start on an Express machine has no page to land on.
	if loc := create(t, h, "/_console/sfn/fast/start", url.Values{"name": {"async-1"}}); !strings.Contains(loc, "tab=start") || !strings.Contains(loc, "no+record") {
		t.Errorf("express async start location = %q", loc)
	}

	// Test a state: the Definition tab lists the states, the partial reports
	// where the state goes next, and DEBUG adds the pipeline.
	def := req(t, h, "GET", "/_console/sfn/fast?tab=definition", nil).Body.String()
	if !strings.Contains(def, `<option value="Prep">`) || !strings.Contains(def, `<option value="Done">`) || !strings.Contains(def, "/sfn/fast/test-state") {
		t.Fatalf("definition tab should carry the test-state form:\n%s", def)
	}
	rec = req(t, h, "POST", "/_console/sfn/fast/test-state", url.Values{
		"definition": {definition}, "state": {"Prep"}, "input": {`{"orderId":"X-9"}`}, "level": {"DEBUG"},
	})
	if rec.Code != 200 {
		t.Fatalf("test-state: %d\n%s", rec.Code, rec.Body)
	}
	for _, want := range []string{`data-status="SUCCEEDED"`, ">Done<", `&#34;ready&#34;: true`, "afterInputPath", "afterParameters"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("test-state result is missing %q:\n%s", want, rec.Body)
		}
	}
	// An unsaved edit is what gets tested — the form sends the editor's text.
	rec = req(t, h, "POST", "/_console/sfn/fast/test-state", url.Values{
		"definition": {strings.Replace(definition, `"ready":true`, `"ready":"draft"`, 1)}, "state": {"Prep"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "draft") {
		t.Errorf("test-state should run the submitted definition: %d\n%s", rec.Code, rec.Body)
	}
	// A mock on a Pass is refused by the service, by name.
	rec = req(t, h, "POST", "/_console/sfn/fast/test-state", url.Values{
		"definition": {definition}, "state": {"Prep"}, "mock": {"result"}, "mock_result": {`{"x":1}`},
	})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Task, Map or Parallel") {
		t.Errorf("mock on a Pass: %d\n%s", rec.Code, rec.Body)
	}
	// A mocked error on a Task reads as a failure with the given name.
	task := `{"StartAt":"Call","States":{"Call":{"Type":"Task","Resource":"arn:aws:lambda:us-east-1:000000000000:function:nope","End":true}}}`
	rec = req(t, h, "POST", "/_console/sfn/fast/test-state", url.Values{
		"definition": {task}, "state": {"Call"}, "mock": {"error"}, "mock_error": {"Lambda.Boom"}, "mock_cause": {"mocked"}, "level": {"TRACE"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Lambda.Boom") || !strings.Contains(rec.Body.String(), `data-status="FAILED"`) {
		t.Errorf("mocked error: %d\n%s", rec.Code, rec.Body)
	}
}

func TestConsoleStepFunctionsRedrive(t *testing.T) {
	h := newConsole(t)
	waiting := `{"StartAt":"W","States":{"W":{"Type":"Wait","Seconds":300,"Next":"D"},"D":{"Type":"Pass","End":true}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"slow"}, "role": {sfnTestRole}, "definition": {waiting}})
	create(t, h, "/_console/sfn/slow/start", url.Values{"name": {"run-1"}})
	waitHistory(t, h, "slow", "run-1", "WaitStateEntered")

	// Running: no Redrive button. Aborted: the button, and the reason a
	// SUCCEEDED one would not have it is on the fact strip instead.
	page := req(t, h, "GET", "/_console/sfn/slow/execution/run-1", nil).Body.String()
	if strings.Contains(page, "/run-1/redrive") {
		t.Fatalf("a RUNNING execution must not offer redrive:\n%s", page)
	}
	req(t, h, "POST", "/_console/sfn/slow/execution/run-1/stop", url.Values{"error": {"Operator"}})
	page = req(t, h, "GET", "/_console/sfn/slow/execution/run-1", nil).Body.String()
	if !strings.Contains(page, "/run-1/redrive") || !strings.Contains(page, `data-status="ABORTED"`) {
		t.Fatalf("an ABORTED execution should offer redrive:\n%s", page)
	}
	rec := req(t, h, "POST", "/_console/sfn/slow/execution/run-1/redrive", nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/sfn/slow/execution/run-1?flash=") {
		t.Fatalf("redrive: %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	hist := waitHistory(t, h, "slow", "run-1", "ExecutionRedriven")
	if !strings.Contains(hist, `data-status="RUNNING"`) || strings.Contains(hist, "data-live-paused") {
		t.Errorf("a redriven execution runs again and its history polls again:\n%s", hist)
	}
	page = req(t, h, "GET", "/_console/sfn/slow/execution/run-1", nil).Body.String()
	if !strings.Contains(page, `id="sum-sfn-exec-redrives"`) || !strings.Contains(page, "1×") || strings.Contains(page, "/run-1/redrive") {
		t.Errorf("header should count the redrive and drop the button while RUNNING:\n%s", page)
	}

	// A SUCCEEDED execution is never redrivable, and the console says so.
	quick := `{"StartAt":"P","States":{"P":{"Type":"Pass","End":true}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"quick"}, "role": {sfnTestRole}, "definition": {quick}})
	create(t, h, "/_console/sfn/quick/start", url.Values{"name": {"run-1"}})
	waitHistory(t, h, "quick", "run-1", `data-status="SUCCEEDED"`)
	if rec := req(t, h, "POST", "/_console/sfn/quick/execution/run-1/redrive", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "ExecutionNotRedrivable") {
		t.Errorf("redrive of a SUCCEEDED execution: %d\n%s", rec.Code, rec.Body)
	}
}

var mapRunARN = regexp.MustCompile(`data-copy="(arn:aws:states:[^"]*:mapRun:[^"]*)"`)

func TestConsoleStepFunctionsMapRuns(t *testing.T) {
	h := newConsole(t)
	// The Distributed Map from stepfunctions/maprun_test.go: every item its
	// own execution under a Map Run.
	distributed := `{"StartAt":"Each","States":{"Each":{"Type":"Map","ItemsPath":"$.items","MaxConcurrency":2,"ResultPath":"$.done","End":true,
	  "ItemProcessor":{"ProcessorConfig":{"Mode":"DISTRIBUTED","ExecutionType":"STANDARD"},
	    "StartAt":"Double","States":{"Double":{"Type":"Pass","Parameters":{"twice.$":"States.MathAdd($.n, $.n)"},"End":true}}}}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"fanout"}, "role": {sfnTestRole}, "definition": {distributed}})
	create(t, h, "/_console/sfn/fanout/start", url.Values{"name": {"run-1"}, "input": {`{"items":[{"n":1},{"n":2},{"n":3}]}`}})
	hist := waitHistory(t, h, "fanout", "run-1", `data-status="SUCCEEDED"`)

	// The Map Run panel sits in the history region with its counts.
	for _, want := range []string{"Map Run", `data-maprun-status="SUCCEEDED"`, "sfn-counts", "Set concurrency", "Child executions"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history is missing %q:\n%s", want, hist)
		}
	}
	m := mapRunARN.FindStringSubmatch(hist)
	if m == nil {
		t.Fatalf("no Map Run ARN in the history:\n%s", hist)
	}
	arn := m[1]
	counts := regexp.MustCompile(`(?s)<tbody><tr>(.*?)</tr></tbody>`).FindStringSubmatch(hist[strings.Index(hist, "sfn-counts"):])
	if counts == nil || strings.Count(counts[1], `<td class="right num">3</td>`) != 2 {
		t.Errorf("counts row should say 3 succeeded of 3 total:\n%s", counts)
	}

	// The children list: three executions, reachable by name.
	kids := req(t, h, "GET", "/_console/sfn/fanout/execution/run-1/maprun-children?arn="+url.QueryEscape(arn), nil)
	if kids.Code != 200 || strings.Count(kids.Body.String(), `data-status="SUCCEEDED"`) != 3 {
		t.Fatalf("children: %d\n%s", kids.Code, kids.Body)
	}
	child := regexp.MustCompile(`href="/_console/sfn/fanout/execution/([^"]+)"`).FindStringSubmatch(kids.Body.String())
	if child == nil {
		t.Fatalf("no child link:\n%s", kids.Body)
	}
	childPage := req(t, h, "GET", "/_console/sfn/fanout/execution/"+child[1], nil)
	if childPage.Code != 200 || !strings.Contains(childPage.Body.String(), "map run") {
		t.Errorf("a child's page should render and name its Map Run: %d\n%s", childPage.Code, childPage.Body)
	}
	// The machine's own list hides the children.
	list := req(t, h, "GET", "/_console/sfn/fanout", nil).Body.String()
	if strings.Contains(list, child[1]) {
		t.Errorf("the machine's executions list should not show Map Run children:\n%s", list)
	}

	// UpdateMapRun, then the history panel again with the new number.
	rec := req(t, h, "POST", "/_console/sfn/fanout/execution/run-1/maprun", url.Values{"arn": {arn}, "max": {"5"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `value="5"`) || !strings.Contains(rec.Header().Get("HX-Trigger"), "Concurrency set to 5") {
		t.Errorf("update map run: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, h, "POST", "/_console/sfn/fanout/execution/run-1/maprun", url.Values{"arn": {arn}, "max": {"lots"}}); rec.Code != 400 {
		t.Errorf("a non-number is refused: %d", rec.Code)
	}
}

func TestConsoleStepFunctionsActivities(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sfn/activities", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "No activities") {
		t.Fatalf("activities page: %d\n%s", page.Code, page.Body)
	}
	if loc := create(t, h, "/_console/sfn/activities/create", url.Values{"name": {"approve"}}); !strings.Contains(loc, "/sfn/activities?flash=") {
		t.Fatalf("activity create location = %q", loc)
	}
	body := req(t, h, "GET", "/_console/sfn/activities", nil).Body.String()
	if !strings.Contains(body, ":activity:approve") || !strings.Contains(body, "/sfn/activities/approve/take") {
		t.Fatalf("activities table:\n%s", body)
	}
	// The list pane's pseudo-row counts it, on every Step Functions page.
	if home := req(t, h, "GET", "/_console/sfn", nil).Body.String(); !strings.Contains(home, `href="/_console/sfn/activities"`) || !strings.Contains(home, `<span class="sb">1</span>`) {
		t.Errorf("list pane should carry the Activities row with its count:\n%s", home)
	}

	// Nothing queued: the short poll says so within its budget.
	started := time.Now()
	rec := req(t, h, "POST", "/_console/sfn/activities/approve/take", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Nothing queued") {
		t.Fatalf("empty take: %d\n%s", rec.Code, rec.Body)
	}
	if took := time.Since(started); took > 4*time.Second {
		t.Errorf("an empty take should give up after the budget, took %s", took)
	}

	// A Task on the activity parks its execution; Take a task claims it,
	// the pre-filled form redeems it, and the execution moves on.
	arn := "arn:aws:states:us-east-1:000000000000:activity:approve"
	def := `{"StartAt":"Work","States":{"Work":{"Type":"Task","Resource":"` + arn + `","ResultPath":"$.decision","End":true}}}`
	create(t, h, "/_console/sfn/create", url.Values{"name": {"approvals"}, "role": {sfnTestRole}, "definition": {def}})
	create(t, h, "/_console/sfn/approvals/start", url.Values{"name": {"order-7"}, "input": {`{"orderId":"O-7"}`}})
	waitHistory(t, h, "approvals", "order-7", "ActivityScheduled")
	rec = req(t, h, "POST", "/_console/sfn/activities/approve/take", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "O-7") || !strings.Contains(rec.Body.String(), `name="token" value="`) {
		t.Fatalf("take: %d\n%s", rec.Code, rec.Body)
	}
	token := regexp.MustCompile(`name="token" value="([^"]+)"`).FindStringSubmatch(rec.Body.String())[1]
	waitHistory(t, h, "approvals", "order-7", "ActivityStarted")
	// A second take finds the queue empty — one worker per task.
	if rec := req(t, h, "POST", "/_console/sfn/activities/approve/take", nil); !strings.Contains(rec.Body.String(), "Nothing queued") {
		t.Errorf("a claimed task should not be handed out twice:\n%s", rec.Body)
	}
	// Heartbeat keeps it; a wrong token is the wire's refusal.
	if rec := req(t, h, "POST", "/_console/sfn/activities/heartbeat", url.Values{"token": {token}}); rec.Code != 204 || !strings.Contains(rec.Header().Get("HX-Trigger"), "Heartbeat sent") {
		t.Errorf("heartbeat: %d %s", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	if rec := req(t, h, "POST", "/_console/sfn/activities/heartbeat", url.Values{"token": {"nonesuch"}}); rec.Code != 400 {
		t.Errorf("heartbeat on a bogus token: %d", rec.Code)
	}
	rec = req(t, h, "POST", "/_console/sfn/activities/task-result", url.Values{
		"token": {token}, "outcome": {"success"}, "output": {`{"approved":true}`}, "activity": {"approve"},
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Task succeeded") {
		t.Fatalf("task-result: %d\n%s", rec.Code, rec.Body)
	}
	waitHistory(t, h, "approvals", "order-7", `data-status="SUCCEEDED"`)
	if io := req(t, h, "GET", "/_console/sfn/approvals/execution/order-7?tab=io", nil).Body.String(); !strings.Contains(io, `&#34;approved&#34;: true`) {
		t.Errorf("the worker's output should be the state's result:\n%s", io)
	}

	if rec := req(t, h, "POST", "/_console/sfn/activities/approve/delete", nil); rec.Code != 303 {
		t.Fatalf("activity delete: %d\n%s", rec.Code, rec.Body)
	}
	if rec := req(t, h, "POST", "/_console/sfn/activities/approve/take", nil); rec.Code != 400 {
		t.Errorf("taking from a deleted activity should be refused: %d", rec.Code)
	}
}
