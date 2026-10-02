package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// A deploy the API accepted is not a deploy that worked. When a resource
// fails the stack rolls back and the call still answers 200, so the console
// has to read the stack before it says anything — it used to say "Stack
// created and deployed" over one that had just undone itself.
func TestTheConsoleSaysWhenAStackRolledBack(t *testing.T) {
	h := newConsole(t)
	failing := `{"Resources":{
		"Good":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"ui-rb-good"}},
		"Bad":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/ui/has space","Type":"String","Value":"v"}}}}`
	rec := req(t, h, "POST", "/_console/cfn/create", url.Values{"name": {"ui-rb"}, "template": {failing}})
	where, _ := url.QueryUnescape(flashOf(rec))
	if !strings.Contains(where, "rolled back") {
		t.Errorf("after a create that rolled back, the console said: %s", where)
	}
	if strings.Contains(where, "created and deployed") {
		t.Errorf("the console claimed a rolled-back stack was deployed: %s", where)
	}
	if !strings.Contains(where, "tab=events") {
		t.Errorf("a failed create should land on the events, which say why: %s", where)
	}
	page := req(t, h, "GET", "/_console/cfn/ui-rb?tab=events", nil).Body.String()
	for _, want := range []string{"ROLLBACK_COMPLETE", "CREATE_FAILED", "DELETE_COMPLETE"} {
		if !strings.Contains(page, want) {
			t.Errorf("the events page lacks %s", want)
		}
	}

	// And a stack that came up still says so.
	good := `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"ui-ok"}}}}`
	rec = req(t, h, "POST", "/_console/cfn/create", url.Values{"name": {"ui-ok"}, "template": {good}})
	if where, _ := url.QueryUnescape(flashOf(rec)); !strings.Contains(where, "created and deployed") || strings.Contains(where, "tab=events") {
		t.Errorf("after a create that worked, the console said: %s", where)
	}
}
