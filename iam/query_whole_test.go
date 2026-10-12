package iam

// A request is validated in every element of its lists (B13). See
// internal/modelcheck/query_whole_test.go for what was wrong; this is the same
// thing through IAM's handler, with the violation never in element 1.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestIAMValidatesEveryTagOfARequest(t *testing.T) {
	ts := iamServer(t)
	setUpFixture(t, ts)
	tag := func(k, v string) map[string]any { return map[string]any{"Key": k, "Value": v} }

	// The control: three valid tags are accepted.
	if code, resp := call(t, ts, "TagUser", map[string]any{"UserName": userName,
		"Tags": []any{tag("env", "dev"), tag("team", "core"), tag("cost", "x")}}); code != http.StatusOK {
		t.Fatalf("three valid tags: %d %s", code, resp)
	}
	tagUser := func(tags ...any) func(int) (int, string) {
		return func(int) (int, string) {
			return call(t, ts, "TagUser", map[string]any{"UserName": userName, "Tags": tags})
		}
	}
	dozetest.RefusedEveryTime(t, "TagUser, second key over 128", "tags.2.key",
		tagUser(tag("env", "dev"), tag(strings.Repeat("k", 129), "x")))
	// Outside the model's pattern for a tag key, which has no "!".
	dozetest.RefusedEveryTime(t, "TagUser, third key outside the pattern", "tags.3.key",
		tagUser(tag("env", "dev"), tag("team", "core"), tag("not!allowed", "x")))
	dozetest.RefusedEveryTime(t, "TagUser, second value over 256", "tags.2.value",
		tagUser(tag("env", "dev"), tag("team", strings.Repeat("v", 257))))
	dozetest.RefusedEveryTime(t, "TagUser, third tag has no value", "tags.3.value",
		tagUser(tag("env", "dev"), tag("team", "core"), map[string]any{"Key": "cost"}))
}
