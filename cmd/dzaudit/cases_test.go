package main

import (
	"regexp"
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

// TestPatternViolationsActuallyViolate is the gap that let a fake gap through.
//
// The generator used to return a fixed "!! not valid !!" for every @pattern and
// hope it fell outside. Secrets Manager's filter-value charset allows spaces and
// exclamation marks, so the service accepted the case, correctly, and the audit
// reported a missing check that was not missing. The property test covered range
// and length and not this.
func TestPatternViolationsActuallyViolate(t *testing.T) {
	patterns := []string{
		`^[a-zA-Z0-9_.-]+$`,                 // the common AWS name charset
		`^\!?[a-zA-Z0-9 :_@\/\+\=\.\-\!]*$`, // Secrets Manager filter values
		`^0|([1-9]\d{0,38})$`,               // Kinesis explicit hash key
		`^arn:aws[a-zA-Z-]*:[a-z0-9-]+:.*$`, // an ARN shape
		`[\s\S]*`,                           // matches everything
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		got := violations(constraint{Kind: "pattern", Pattern: p})
		if len(got) == 0 {
			// Legitimate only when nothing can violate it.
			if !re.MatchString("\x00\x01") {
				t.Errorf("pattern %q: emitted no case, but a violator exists", p)
			}
			continue
		}
		s, ok := got[0].Value.(string)
		if !ok {
			t.Errorf("pattern %q: value is %T, want string", p, got[0].Value)
			continue
		}
		if re.MatchString(s) {
			t.Errorf("pattern %q: emitted %q, which MATCHES it — that case reports a "+
				"gap the service does not have", p, s)
		}
	}
}
