package main

import (
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestViolationsBreakTheConstraintTheyName(t *testing.T) {
	// A generated value that does NOT violate its constraint produces a case
	// the service correctly accepts, which a runner then reports as a missing
	// check. Wrong in the direction that manufactures fake gaps.
	t.Run("range gives one below and one above", func(t *testing.T) {
		got := violations(constraint{Kind: "range", Min: f(128), Max: f(32768)})
		if len(got) != 2 {
			t.Fatalf("got %d cases, want 2", len(got))
		}
		if got[0].Value != float64(127) {
			t.Errorf("low case = %v, want 127", got[0].Value)
		}
		if got[1].Value != float64(32769) {
			t.Errorf("high case = %v, want 32769", got[1].Value)
		}
	})

	t.Run("length over the max is actually longer", func(t *testing.T) {
		got := violations(constraint{Kind: "length", Min: f(1), Max: f(255)})
		var long string
		for _, c := range got {
			if s, ok := c.Value.(string); ok && len(s) > 0 {
				long = s
			}
		}
		if len(long) != 256 {
			t.Errorf("long value is %d chars, want 256", len(long))
		}
	})

	t.Run("a zero minimum yields no short case", func(t *testing.T) {
		// "" does not violate length 0..N, so emitting it would be a false gap.
		for _, c := range violations(constraint{Kind: "length", Min: f(0), Max: f(10)}) {
			if s, ok := c.Value.(string); ok && s == "" {
				t.Error("emitted an empty string against a minimum of 0")
			}
		}
	})

	t.Run("enum value is not a member", func(t *testing.T) {
		vals := []string{"PROVISIONED", "PAY_PER_REQUEST"}
		got := violations(constraint{Kind: "enum", Values: vals})
		for _, v := range vals {
			if got[0].Value == v {
				t.Fatalf("emitted %q, which IS a member", v)
			}
		}
	})

	t.Run("required is expressed as omission", func(t *testing.T) {
		got := violations(constraint{Kind: "required"})
		if len(got) != 1 || got[0].Value != nil {
			t.Errorf("required case = %+v, want a single nil value", got)
		}
	})
}

func TestTargetOnlyForAwsJson(t *testing.T) {
	// The target header is the whole request only for the awsJson protocols.
	// Emitting one for restXml would invite a runner to send something S3 has
	// never understood, and read the resulting error as a validation pass.
	if got := targetFor("com.amazonaws.dynamodb#DynamoDB_20120810", "awsJson1_0", "CreateTable"); got != "DynamoDB_20120810.CreateTable" {
		t.Errorf("target = %q", got)
	}
	if got := targetFor("com.amazonaws.s3#AmazonS3", "restXml", "CreateBucket"); got != "" {
		t.Errorf("restXml got a target %q, want none", got)
	}
}

func TestBaselineListsOnlyTopLevelRequireds(t *testing.T) {
	// A baseline supplies the operation's own required inputs. A required
	// member nested inside an optional structure only matters once that
	// structure is being sent, so listing it would make baselines impossible.
	found := []finding{
		{Op: "CreateStream", Path: "StreamName", Constraint: constraint{Kind: "required"}},
		{Op: "CreateStream", Path: "StreamModeDetails.StreamMode", Constraint: constraint{Kind: "required"}},
		{Op: "CreateStream", Path: "ShardCount", Constraint: constraint{Kind: "range", Min: f(1)}},
	}
	got := requiredByOp(found)["CreateStream"]
	if len(got) != 1 || got[0] != "StreamName" {
		t.Errorf("baseline = %v, want [StreamName]", got)
	}
	for _, g := range got {
		if strings.ContainsAny(g, ".[{") {
			t.Errorf("%q is nested and cannot be a baseline member", g)
		}
	}
}
