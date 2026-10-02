package cloudformation_test

// Refusals the boto3 conformance suite found missing
// (conformance/tests/test_cloudformation.py).

import (
	"net/http"
	"strings"
	"testing"
)

const fixTemplate = `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"fix-q"}}}}`

func TestStackNamesAreHeldToWhatCloudFormationAccepts(t *testing.T) {
	ts := cfnServer(t)
	for _, name := range []string{"has_underscore", "9starts-with-a-digit", "has space", strings.Repeat("a", 129)} {
		code, body := call(t, ts, "CreateStack", map[string]any{"StackName": name, "TemplateBody": fixTemplate})
		if code != http.StatusBadRequest || !strings.Contains(body, "[a-zA-Z][-a-zA-Z0-9]*") {
			t.Errorf("CreateStack(%.20q) = %d: %s", name, code, body)
		}
	}
}

// An update has to update something. The same template with the same
// parameters used to be re-applied and reported UPDATE_COMPLETE.
func TestAnUpdateThatChangesNothingIsRefused(t *testing.T) {
	ts := cfnServer(t)
	if code, body := call(t, ts, "CreateStack", map[string]any{"StackName": "steady", "TemplateBody": fixTemplate}); code != http.StatusOK {
		t.Fatalf("CreateStack = %d: %s", code, body)
	}
	code, body := call(t, ts, "UpdateStack", map[string]any{"StackName": "steady", "TemplateBody": fixTemplate})
	if code != http.StatusBadRequest || !strings.Contains(body, "No updates are to be performed.") {
		t.Fatalf("an update with no change = %d: %s", code, body)
	}
	changed := strings.Replace(fixTemplate, `"Resources"`, `"Description":"now described","Resources"`, 1)
	if code, body := call(t, ts, "UpdateStack", map[string]any{"StackName": "steady", "TemplateBody": changed}); code != http.StatusOK {
		t.Fatalf("an update with a change = %d: %s", code, body)
	}
}

// A reference to nothing is refused at the call, by validation and by create
// alike, and no stack is left behind.
func TestDanglingReferencesAreRefusedAtTheCall(t *testing.T) {
	ts := cfnServer(t)
	dangling := `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":{"Ref":"Ghost"}}}}}`
	for _, op := range []string{"ValidateTemplate", "CreateStack"} {
		code, body := call(t, ts, op, map[string]any{"StackName": "dangling", "TemplateBody": dangling})
		if code != http.StatusBadRequest || !strings.Contains(body, "Unresolved resource dependencies [Ghost]") {
			t.Errorf("%s = %d: %s", op, code, body)
		}
	}
	if code, _ := call(t, ts, "DescribeStacks", map[string]any{"StackName": "dangling"}); code == http.StatusOK {
		t.Error("the refused create left a stack behind")
	}
}

// When a resource fails, the event trail says so. It used to report every
// resource — the one that failed included — as CREATE_COMPLETE.
func TestAFailedResourceIsNotReportedAsCreated(t *testing.T) {
	ts := cfnServer(t)
	failing := `{"Resources":{
		"Good":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"fix-good"}},
		"Bad":{"Type":"AWS::SQS::Queue","Properties":{"VisibilityTimeout":99999}}}}`
	call(t, ts, "CreateStack", map[string]any{"StackName": "failing", "TemplateBody": failing})
	_, events := call(t, ts, "DescribeStackEvents", map[string]any{"StackName": "failing"})
	bad := events[strings.Index(events, "<LogicalResourceId>Bad</LogicalResourceId>"):]
	if !strings.Contains(events, "CREATE_FAILED") || strings.Contains(firstEvent(bad), "CREATE_COMPLETE") {
		t.Fatalf("the trail does not say Bad failed:\n%s", events)
	}
	for _, chunk := range strings.Split(events, "<member>") {
		if strings.Contains(chunk, "<LogicalResourceId>Bad</LogicalResourceId>") && strings.Contains(chunk, "CREATE_COMPLETE") {
			t.Fatalf("an event says the failed resource was created:\n%s", chunk)
		}
	}
}

func firstEvent(s string) string {
	if i := strings.Index(s, "</member>"); i >= 0 {
		return s[:i]
	}
	return s
}
