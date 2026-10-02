package cloudformation_test

// What happens to a stack when a resource fails — see rollback.go.
//
// The failing resource throughout is an SSM parameter with a name SSM
// refuses. Parameters are applied late, so the queues beside it have already
// been made when it fails, which is the situation a rollback exists for.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const badParameter = `"Bad":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/rb/has space","Type":"String","Value":"v"}}`

func rbQueue(logical, name string, timeout int, extra string) string {
	return fmt.Sprintf(`%q:{"Type":"AWS::SQS::Queue"%s,"Properties":{"QueueName":%q,"VisibilityTimeout":%d}}`,
		logical, extra, name, timeout)
}

func rbTemplate(resources ...string) string {
	return `{"Resources":{` + strings.Join(resources, ",") + `}}`
}

// sqs speaks to the queue service behind the same gateway, to see what a
// stack really left in the account.
func rbSQS(t *testing.T, ts *httptest.Server, action, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func queueExists(t *testing.T, ts *httptest.Server, name string) bool {
	t.Helper()
	code, _ := rbSQS(t, ts, "GetQueueUrl", `{"QueueName":"`+name+`"}`)
	return code == http.StatusOK
}

func queueTimeout(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	_, body := rbSQS(t, ts, "GetQueueAttributes",
		`{"QueueUrl":"http://sqs.doze-aws.internal/000000000000/`+name+`","AttributeNames":["VisibilityTimeout"]}`)
	m := regexp.MustCompile(`"VisibilityTimeout":"(\d+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no VisibilityTimeout for %s: %s", name, body)
	}
	return m[1]
}

func stackStatus(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	_, body := call(t, ts, "DescribeStacks", map[string]any{"StackName": name})
	m := regexp.MustCompile(`<StackStatus>([A-Z_]+)</StackStatus>`).FindStringSubmatch(body)
	if m == nil {
		return "(none: " + firstLine(body) + ")"
	}
	return m[1]
}

func firstLine(s string) string {
	if len(s) > 160 {
		s = s[:160]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

var eventRE = regexp.MustCompile(`(?s)<member>.*?<LogicalResourceId>([^<]*)</LogicalResourceId>.*?<ResourceStatus>([A-Z_]+)</ResourceStatus>.*?</member>`)

// trail is a stack's events, oldest first, as "LogicalId STATUS".
func trail(t *testing.T, ts *httptest.Server, name string) []string {
	t.Helper()
	_, body := call(t, ts, "DescribeStackEvents", map[string]any{"StackName": name})
	var out []string
	for _, m := range eventRE.FindAllStringSubmatch(body, -1) {
		out = append([]string{m[1] + " " + m[2]}, out...) // the API answers newest first
	}
	return out
}

// inOrder reports the first of want that does not appear, in order, in got.
func inOrder(got []string, want ...string) string {
	i := 0
	for _, w := range want {
		for i < len(got) && got[i] != w {
			i++
		}
		if i == len(got) {
			return w
		}
		i++
	}
	return ""
}

func rbCreate(t *testing.T, ts *httptest.Server, name, body string, extra map[string]any) (int, string) {
	t.Helper()
	req := map[string]any{"StackName": name, "TemplateBody": body}
	for k, v := range extra {
		req[k] = v
	}
	return call(t, ts, "CreateStack", req)
}

func TestAFailedCreateIsRolledBack(t *testing.T) {
	ts := cfnServer(t)
	code, body := rbCreate(t, ts, "rb", rbTemplate(rbQueue("Good", "rb-good", 30, ""), badParameter), nil)
	// The call succeeded. The stack is what failed, and it says so itself.
	if code != http.StatusOK || !strings.Contains(body, "<StackId>") {
		t.Fatalf("CreateStack = %d: %s", code, body)
	}
	if got := stackStatus(t, ts, "rb"); got != "ROLLBACK_COMPLETE" {
		t.Fatalf("status = %s, want ROLLBACK_COMPLETE", got)
	}
	if queueExists(t, ts, "rb-good") {
		t.Error("the queue the failed create had made is still there")
	}
	events := trail(t, ts, "rb")
	if missing := inOrder(events,
		"rb CREATE_IN_PROGRESS", "Good CREATE_COMPLETE", "Bad CREATE_FAILED",
		"rb ROLLBACK_IN_PROGRESS", "Good DELETE_COMPLETE", "rb ROLLBACK_COMPLETE"); missing != "" {
		t.Errorf("the trail lacks %q in its place:\n%s", missing, strings.Join(events, "\n"))
	}
	for _, e := range events {
		if e == "Bad CREATE_COMPLETE" {
			t.Errorf("the trail says the failed resource was created:\n%s", strings.Join(events, "\n"))
		}
	}

	// A rolled-back stack can only be deleted.
	code, body = call(t, ts, "UpdateStack", map[string]any{"StackName": "rb", "TemplateBody": rbTemplate(rbQueue("Good", "rb-good", 30, ""))})
	if code != http.StatusBadRequest || !strings.Contains(body, "is in ROLLBACK_COMPLETE state and can not be updated") {
		t.Errorf("UpdateStack on a rolled-back stack = %d: %s", code, body)
	}
	if code, body := rbCreate(t, ts, "rb", rbTemplate(rbQueue("Good", "rb-good", 30, "")), nil); code != http.StatusBadRequest || !strings.Contains(body, "AlreadyExistsException") {
		t.Errorf("CreateStack over a rolled-back stack = %d: %s", code, body)
	}
	if code, body := call(t, ts, "DeleteStack", map[string]any{"StackName": "rb"}); code != http.StatusOK {
		t.Fatalf("DeleteStack = %d: %s", code, body)
	}
	if code, body := rbCreate(t, ts, "rb", rbTemplate(rbQueue("Good", "rb-good", 30, "")), nil); code != http.StatusOK {
		t.Fatalf("CreateStack once it is deleted = %d: %s", code, body)
	}
	if got := stackStatus(t, ts, "rb"); got != "CREATE_COMPLETE" {
		t.Errorf("status after a clean create = %s", got)
	}
}

// A template can name a resource that already exists. The rollback of a stack
// that never made it must leave it alone, and so must deleting that stack.
func TestARollbackDeletesOnlyWhatItMade(t *testing.T) {
	ts := cfnServer(t)
	if code, body := rbSQS(t, ts, "CreateQueue", `{"QueueName":"rb-mine"}`); code != http.StatusOK {
		t.Fatalf("CreateQueue = %d: %s", code, body)
	}
	rbCreate(t, ts, "adopter", rbTemplate(rbQueue("Mine", "rb-mine", 30, ""), rbQueue("New", "rb-new", 30, ""), badParameter), nil)
	if got := stackStatus(t, ts, "adopter"); got != "ROLLBACK_COMPLETE" {
		t.Fatalf("status = %s, want ROLLBACK_COMPLETE", got)
	}
	if queueExists(t, ts, "rb-new") {
		t.Error("the queue the stack made was not rolled back")
	}
	if !queueExists(t, ts, "rb-mine") {
		t.Fatal("the rollback deleted a queue that existed before the stack did")
	}
	call(t, ts, "DeleteStack", map[string]any{"StackName": "adopter"})
	if !queueExists(t, ts, "rb-mine") {
		t.Fatal("deleting the rolled-back stack deleted a queue it never owned")
	}
}

func TestTheCallerCanAskForAFailureToBeLeftOrDeleted(t *testing.T) {
	ts := cfnServer(t)
	body := func(q string) string { return rbTemplate(rbQueue("Good", q, 30, ""), badParameter) }

	rbCreate(t, ts, "kept", body("rb-kept"), map[string]any{"DisableRollback": "true"})
	if got := stackStatus(t, ts, "kept"); got != "CREATE_FAILED" {
		t.Errorf("DisableRollback: status = %s, want CREATE_FAILED", got)
	}
	if !queueExists(t, ts, "rb-kept") {
		t.Error("DisableRollback: what was made should have been left")
	}

	rbCreate(t, ts, "nothing", body("rb-nothing"), map[string]any{"OnFailure": "DO_NOTHING"})
	if got := stackStatus(t, ts, "nothing"); got != "CREATE_FAILED" || !queueExists(t, ts, "rb-nothing") {
		t.Errorf("OnFailure=DO_NOTHING: status = %s, queue exists = %v", got, queueExists(t, ts, "rb-nothing"))
	}

	code, resp := rbCreate(t, ts, "gone", body("rb-gone"), map[string]any{"OnFailure": "DELETE"})
	if code != http.StatusOK {
		t.Fatalf("OnFailure=DELETE: CreateStack = %d: %s", code, resp)
	}
	if queueExists(t, ts, "rb-gone") {
		t.Error("OnFailure=DELETE: what was made should have been deleted")
	}
	// A deleted stack answers to its id, not its name.
	id := regexp.MustCompile(`<StackId>([^<]+)</StackId>`).FindStringSubmatch(resp)
	if id == nil {
		t.Fatalf("OnFailure=DELETE: no StackId in %s", resp)
	}
	if events := trail(t, ts, id[1]); inOrder(events, "gone ROLLBACK_IN_PROGRESS", "gone DELETE_COMPLETE") != "" {
		t.Errorf("OnFailure=DELETE: the stack should end deleted:\n%s", strings.Join(events, "\n"))
	}
	if got := stackStatus(t, ts, id[1]); got != "DELETE_COMPLETE" {
		t.Errorf("OnFailure=DELETE: status = %s, want DELETE_COMPLETE", got)
	}
}

func TestAFailedUpdateGoesBackToThePreviousTemplate(t *testing.T) {
	ts := cfnServer(t)
	v1 := rbTemplate(rbQueue("Q", "rb-q", 30, ""))
	if code, body := rbCreate(t, ts, "upd", v1, nil); code != http.StatusOK {
		t.Fatalf("CreateStack = %d: %s", code, body)
	}
	v2 := rbTemplate(rbQueue("Q", "rb-q", 45, ""), rbQueue("Extra", "rb-extra", 30, ""), badParameter)
	code, body := call(t, ts, "UpdateStack", map[string]any{"StackName": "upd", "TemplateBody": v2})
	if code != http.StatusOK {
		t.Fatalf("UpdateStack = %d: %s", code, body)
	}
	if got := stackStatus(t, ts, "upd"); got != "UPDATE_ROLLBACK_COMPLETE" {
		t.Fatalf("status = %s, want UPDATE_ROLLBACK_COMPLETE", got)
	}
	if got := queueTimeout(t, ts, "rb-q"); got != "30" {
		t.Errorf("the queue's timeout is %s; the failed update's 45 should have been put back to 30", got)
	}
	if queueExists(t, ts, "rb-extra") {
		t.Error("the queue the failed update added is still there")
	}
	if _, got := call(t, ts, "GetTemplate", map[string]any{"StackName": "upd"}); strings.Contains(got, "rb-extra") {
		t.Errorf("the stack's template is the one that failed:\n%s", got)
	}
	events := trail(t, ts, "upd")
	if missing := inOrder(events,
		"upd CREATE_COMPLETE", "upd UPDATE_IN_PROGRESS", "Bad UPDATE_FAILED",
		"upd UPDATE_ROLLBACK_IN_PROGRESS", "Extra DELETE_COMPLETE", "upd UPDATE_ROLLBACK_COMPLETE"); missing != "" {
		t.Errorf("the trail lacks %q in its place:\n%s", missing, strings.Join(events, "\n"))
	}

	// It is a working stack again, and can be updated properly.
	v3 := rbTemplate(rbQueue("Q", "rb-q", 60, ""))
	if code, body := call(t, ts, "UpdateStack", map[string]any{"StackName": "upd", "TemplateBody": v3}); code != http.StatusOK {
		t.Fatalf("a good update after the rollback = %d: %s", code, body)
	}
	if got, timeout := stackStatus(t, ts, "upd"), queueTimeout(t, ts, "rb-q"); got != "UPDATE_COMPLETE" || timeout != "60" {
		t.Errorf("after a good update: status %s, timeout %s", got, timeout)
	}

	// DisableRollback leaves the failed update where it fell.
	call(t, ts, "UpdateStack", map[string]any{"StackName": "upd", "TemplateBody": v2, "DisableRollback": "true"})
	if got := stackStatus(t, ts, "upd"); got != "UPDATE_FAILED" {
		t.Errorf("DisableRollback on an update: status = %s, want UPDATE_FAILED", got)
	}
	if !queueExists(t, ts, "rb-extra") {
		t.Error("DisableRollback on an update: what it made should have been left")
	}
}

// A resource taken out of a template is deleted by the update that takes it
// out. It used to stay running, owned by nothing.
func TestAnUpdateDeletesWhatItsTemplateDrops(t *testing.T) {
	ts := cfnServer(t)
	keep := rbQueue("Keep", "rb-keep", 30, "")
	rbCreate(t, ts, "drop", rbTemplate(keep, rbQueue("Drop", "rb-drop", 30, ""), rbQueue("Held", "rb-held", 30, `,"DeletionPolicy":"Retain"`)), nil)
	if code, body := call(t, ts, "UpdateStack", map[string]any{"StackName": "drop", "TemplateBody": rbTemplate(keep)}); code != http.StatusOK {
		t.Fatalf("UpdateStack = %d: %s", code, body)
	}
	if got := stackStatus(t, ts, "drop"); got != "UPDATE_COMPLETE" {
		t.Fatalf("status = %s", got)
	}
	if queueExists(t, ts, "rb-drop") {
		t.Error("the queue the template dropped is still there")
	}
	if !queueExists(t, ts, "rb-keep") {
		t.Error("the queue the template kept was deleted")
	}
	if !queueExists(t, ts, "rb-held") {
		t.Error("a queue with DeletionPolicy Retain was deleted when the template dropped it")
	}
	events := trail(t, ts, "drop")
	if missing := inOrder(events, "drop UPDATE_IN_PROGRESS", "drop UPDATE_COMPLETE_CLEANUP_IN_PROGRESS",
		"Drop DELETE_COMPLETE", "drop UPDATE_COMPLETE"); missing != "" {
		t.Errorf("the trail lacks %q in its place:\n%s", missing, strings.Join(events, "\n"))
	}
}

func TestDeletingAStackKeepsWhatItSaysToRetain(t *testing.T) {
	ts := cfnServer(t)
	rbCreate(t, ts, "ret", rbTemplate(rbQueue("Gone", "rb-ret-gone", 30, ""), rbQueue("Held", "rb-ret-held", 30, `,"DeletionPolicy":"Retain"`)), nil)
	call(t, ts, "DeleteStack", map[string]any{"StackName": "ret"})
	if queueExists(t, ts, "rb-ret-gone") {
		t.Error("deleting the stack left its queue")
	}
	if !queueExists(t, ts, "rb-ret-held") {
		t.Error("deleting the stack deleted a queue with DeletionPolicy Retain")
	}
}
