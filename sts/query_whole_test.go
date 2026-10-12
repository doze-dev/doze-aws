package sts

// A request is validated in every element of its lists (B13). See
// internal/modelcheck/query_whole_test.go for what was wrong; this is the same
// thing through STS's handler, with the violation never in element 1.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestSTSValidatesEveryElementOfARequest(t *testing.T) {
	ts := stsServer(t)
	tag := func(k, v string) map[string]any { return map[string]any{"Key": k, "Value": v} }
	arn := func(a string) map[string]any { return map[string]any{"arn": a} }
	const readOnly, viewOnly = "arn:aws:iam::aws:policy/ReadOnlyAccess", "arn:aws:iam::aws:policy/job-function/ViewOnlyAccess"
	assume := func(extra map[string]any) func(int) (int, string) {
		return func(int) (int, string) {
			body := map[string]any{"RoleArn": roleARN, "RoleSessionName": "audit"}
			for k, v := range extra {
				body[k] = v
			}
			return call(t, ts, "AssumeRole", body)
		}
	}

	// The control: several valid elements of each list are accepted.
	if code, resp := assume(map[string]any{
		"Tags":              []any{tag("env", "dev"), tag("team", "core"), tag("cost", "x")},
		"PolicyArns":        []any{arn(readOnly), arn(viewOnly)},
		"TransitiveTagKeys": []any{"env", "team"},
	})(0); code != http.StatusOK {
		t.Fatalf("a valid AssumeRole with three tags and two policies: %d %s", code, resp)
	}

	dozetest.RefusedEveryTime(t, "AssumeRole, second tag key over 128", "tags.2.key",
		assume(map[string]any{"Tags": []any{tag("env", "dev"), tag(strings.Repeat("k", 129), "x")}}))
	dozetest.RefusedEveryTime(t, "AssumeRole, third tag key outside the pattern", "tags.3.key",
		assume(map[string]any{"Tags": []any{tag("env", "dev"), tag("team", "core"), tag("not!allowed", "x")}}))
	// A policy ARN is at least 20 characters.
	dozetest.RefusedEveryTime(t, "AssumeRole, second policy ARN under 20", "policyArns.2.arn",
		assume(map[string]any{"PolicyArns": []any{arn(readOnly), arn("arn:aws:iam::short")}}))
	dozetest.RefusedEveryTime(t, "AssumeRole, third policy ARN under 20", "policyArns.3.arn",
		assume(map[string]any{"PolicyArns": []any{arn(readOnly), arn(viewOnly), arn("arn:aws:iam::short")}}))
	// A list of plain strings, which is the other shape a Query list takes.
	dozetest.RefusedEveryTime(t, "AssumeRole, second transitive tag key outside the pattern", "transitiveTagKeys.2",
		assume(map[string]any{"Tags": []any{tag("env", "dev")}, "TransitiveTagKeys": []any{"env", "not!allowed"}}))
}
