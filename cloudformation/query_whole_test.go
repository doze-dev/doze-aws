package cloudformation_test

// A request is validated in every element of its lists (B13). See
// internal/modelcheck/query_whole_test.go for what was wrong; this is the same
// thing through CloudFormation's handler, with the violation never in
// element 1.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestCloudFormationValidatesEveryElementOfARequest(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a stack")
	}
	ts := cfnServer(t)
	tag := func(k, v string) map[string]any { return map[string]any{"Key": k, "Value": v} }
	// A new stack name each run: a run that is wrongly accepted creates the
	// stack, and the next would be refused for existing.
	create := func(prefix string, extra map[string]any) func(int) (int, string) {
		return func(run int) (int, string) {
			body := map[string]any{"StackName": prefix + "-" + strconv.Itoa(run), "TemplateBody": auditTemplate}
			for k, v := range extra {
				body[k] = v
			}
			return call(t, ts, "CreateStack", body)
		}
	}

	// The control: every capability the model names and three valid tags.
	if code, resp := create("whole-control", map[string]any{
		"Capabilities": []any{"CAPABILITY_IAM", "CAPABILITY_NAMED_IAM", "CAPABILITY_AUTO_EXPAND"},
		"Tags":         []any{tag("env", "dev"), tag("team", "core"), tag("cost", "x")},
	})(0); code != http.StatusOK {
		t.Fatalf("a valid CreateStack with three capabilities and three tags: %d %s", code, resp)
	}

	dozetest.RefusedEveryTime(t, "CreateStack, second capability not in the enum", "capabilities.2",
		create("whole-cap2", map[string]any{"Capabilities": []any{"CAPABILITY_IAM", "CAPABILITY_EVERYTHING"}}))
	dozetest.RefusedEveryTime(t, "CreateStack, third capability not in the enum", "capabilities.3",
		create("whole-cap3", map[string]any{"Capabilities": []any{"CAPABILITY_IAM", "CAPABILITY_NAMED_IAM", "CAPABILITY_EVERYTHING"}}))
	dozetest.RefusedEveryTime(t, "CreateStack, second tag key over 128", "tags.2.key",
		create("whole-tag2", map[string]any{"Tags": []any{tag("env", "dev"), tag(strings.Repeat("k", 129), "x")}}))
	dozetest.RefusedEveryTime(t, "CreateStack, third tag value over 256", "tags.3.value",
		create("whole-tag3", map[string]any{"Tags": []any{tag("env", "dev"), tag("team", "core"), tag("cost", strings.Repeat("v", 257))}}))
}
