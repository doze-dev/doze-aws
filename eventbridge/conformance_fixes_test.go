package eventbridge

// Refusals the boto3 conformance suite found missing (conformance/tests/test_eventbridge.py).
// None is in the service model, so the model-derived audit could not see them.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

func TestRateUnitsAgreeWithTheirNumber(t *testing.T) {
	for _, expr := range []string{"rate(1 minute)", "rate(5 minutes)", "rate(1 hour)", "rate(2 days)"} {
		if _, ok := parseRate(expr); !ok {
			t.Errorf("%s refused", expr)
		}
	}
	for _, expr := range []string{"rate(1 minutes)", "rate(5 minute)", "rate(2 hour)", "rate(1 days)", "rate(0 minutes)"} {
		if _, ok := parseRate(expr); ok {
			t.Errorf("%s accepted", expr)
		}
	}
}

func TestSchedulesBelongToTheDefaultBus(t *testing.T) {
	ts := ebServer(t)
	call(t, ts, "CreateEventBus", map[string]any{"Name": "custom"})
	code, body := call(t, ts, "PutRule", map[string]any{
		"Name": "tick", "EventBusName": "custom", "ScheduleExpression": "rate(5 minutes)"})
	if code != http.StatusBadRequest || !strings.Contains(body, "only on the default event bus") {
		t.Fatalf("a schedule on a custom bus = %d: %s", code, body)
	}
	if code, body := call(t, ts, "PutRule", map[string]any{"Name": "tick", "ScheduleExpression": "rate(5 minutes)"}); code != http.StatusOK {
		t.Fatalf("a schedule on the default bus = %d: %s", code, body)
	}
}

// A rule with targets is refused until RemoveTargets has emptied it. It used
// to be deleted, targets and all.
func TestARuleWithTargetsCannotBeDeleted(t *testing.T) {
	ts := ebServer(t)
	call(t, ts, "PutRule", map[string]any{"Name": "r", "EventPattern": `{"source":["x"]}`})
	call(t, ts, "PutTargets", map[string]any{"Rule": "r", "Targets": []map[string]any{
		{"Id": "t", "Arn": "arn:aws:sqs:us-east-1:000000000000:q"}}})

	code, body := call(t, ts, "DeleteRule", map[string]any{"Name": "r"})
	if code != http.StatusBadRequest || !strings.Contains(body, "Rule can't be deleted since it has targets.") {
		t.Fatalf("DeleteRule with targets = %d: %s", code, body)
	}
	if code, _ := call(t, ts, "DescribeRule", map[string]any{"Name": "r"}); code != http.StatusOK {
		t.Fatal("the refused delete removed the rule anyway")
	}
	call(t, ts, "RemoveTargets", map[string]any{"Rule": "r", "Ids": []string{"t"}})
	if code, body := call(t, ts, "DeleteRule", map[string]any{"Name": "r"}); code != http.StatusOK {
		t.Fatalf("DeleteRule once empty = %d: %s", code, body)
	}
	// And deleting what is not there is still not an error.
	if code, body := call(t, ts, "DeleteRule", map[string]any{"Name": "r"}); code != http.StatusOK {
		t.Fatalf("DeleteRule again = %d: %s", code, body)
	}
}

// Buses take tags, the default one included. Every tag call used to parse its
// ARN as a rule's and refuse a bus.
func TestABusCanBeTagged(t *testing.T) {
	ts := ebServer(t)
	call(t, ts, "CreateEventBus", map[string]any{"Name": "custom"})
	for _, arn := range []string{busARN(awsident.Default(), "custom"), busARN(awsident.Default(), DefaultBus)} {
		code, body := call(t, ts, "TagResource", map[string]any{"ResourceARN": arn,
			"Tags": []map[string]string{{"Key": "env", "Value": "dev"}, {"Key": "team", "Value": "a"}}})
		if code != http.StatusOK {
			t.Fatalf("TagResource(%s) = %d: %s", arn, code, body)
		}
		call(t, ts, "UntagResource", map[string]any{"ResourceARN": arn, "TagKeys": []string{"team"}})
		_, body = call(t, ts, "ListTagsForResource", map[string]any{"ResourceARN": arn})
		if !strings.Contains(body, `"env"`) || strings.Contains(body, `"team"`) {
			t.Errorf("tags of %s: %s", arn, body)
		}
	}
	code, body := call(t, ts, "TagResource", map[string]any{"ResourceARN": busARN(awsident.Default(), "absent"),
		"Tags": []map[string]string{{"Key": "k", "Value": "v"}}})
	if code != http.StatusBadRequest || !strings.Contains(body, "ResourceNotFoundException") {
		t.Errorf("tagging an absent bus = %d: %s", code, body)
	}
	// Tagging the default bus stores it, and it must still be listed once.
	_, body = call(t, ts, "ListEventBuses", map[string]any{})
	if n := strings.Count(body, `"Name":"default"`); n != 1 {
		t.Errorf("the default bus is listed %d times: %s", n, body)
	}
}
