package console

import "testing"

// The wire's whole claim is that a refusal, a denial, a not-built operation and
// a fault are different events. If callState blurs them the claim is false, so
// it is worth a test of its own.
//
// It classifies from facts the server stamped on the answer, never from the
// spelling of a status or a code. A 403 is a denial only when IAM said so: a
// signature mismatch is also a 403 and is a refusal, and a code that merely
// contains "AccessDenied" is whatever the service chose to call it.
func TestCallStateClassifiesFromFacts(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		ref    *Refusal
		facts  wireFacts
		want   string
	}{
		{"served", 200, nil, wireFacts{}, "served"},
		{"served with a body that parses to nothing", 201, nil, wireFacts{}, "served"},
		{"input constraint refused it", 400, &Refusal{Code: "InvalidParameterValue"}, wireFacts{}, "refused"},
		{"refused with no parsed reason", 400, nil, wireFacts{}, "refused"},

		{"IAM stamped it: denied", 403, &Refusal{Code: "AccessDeniedException"}, wireFacts{Denied: "iam"}, "denied"},
		{"IAM stamped a 400-shaped denial: still denied", 400, &Refusal{Code: "AccessDenied"}, wireFacts{Denied: "iam"}, "denied"},
		{"a 403 with no stamp is not a denial", 403, nil, wireFacts{}, "refused"},
		{"AccessDenied spelled in a code is not a stamp", 400, &Refusal{Code: "AccessDeniedException"}, wireFacts{}, "refused"},
		{"NotAuthorized spelled in a code is not a stamp", 400, &Refusal{Code: "NotAuthorized"}, wireFacts{}, "refused"},

		{"501 is not built, stamped or not", 501, nil, wireFacts{}, "notbuilt"},
		{"declined, stamped", 501, &Refusal{Code: "NotImplemented"}, wireFacts{NotBuilt: "declined"}, "notbuilt"},
		{"deferred, stamped", 501, nil, wireFacts{NotBuilt: "deferred"}, "notbuilt"},
		{"not built outranks a 5xx reading", 501, nil, wireFacts{NotBuilt: "deferred"}, "notbuilt"},
		{"an older stub table's answer, by its stamp",
			400, &Refusal{Code: "ValidationError", Message: "doze-aws does not implement ListStackSets"}, wireFacts{Stub: "1"}, "notbuilt"},
		{"UnsupportedOperationException with no stamp is the service's own refusal",
			400, &Refusal{Code: "UnsupportedOperationException", Message: "rotation applies to symmetric keys only"}, wireFacts{}, "refused"},

		{"a limit of doze-aws's own", 400, &Refusal{Code: "LimitExceededException"}, wireFacts{Local: "1"}, "refusedhere"},
		{"a local-limit stamp on a 2xx is not a failure", 200, nil, wireFacts{Local: "1"}, "served"},

		{"the emulator broke", 500, nil, wireFacts{}, "error"},
		{"a 503 is still the emulator", 503, &Refusal{Code: "Whatever"}, wireFacts{}, "error"},
	} {
		if got := callState(c.status, c.ref, c.facts); got != c.want {
			t.Errorf("%s: callState(%d, %v, %+v) = %q, want %q", c.name, c.status, c.ref, c.facts, got, c.want)
		}
	}
}

// Every state the classifier can return has words, and the words differ. A
// state that fell through to the generic copy would read as "refused" while
// being coloured as something else.
func TestEveryStateHasItsOwnWords(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{"refused", "refusedhere", "denied", "notbuilt", "error"} {
		v := stateCopyFor(s, "")
		if v.Label == "" || v.Why == "" {
			t.Errorf("%s: has no label or no explanation: %+v", s, v)
		}
		if other, dup := seen[v.Label]; dup {
			t.Errorf("%s and %s share the label %q: the state must read without its colour", s, other, v.Label)
		}
		seen[v.Label] = s
	}
	if a, b := stateCopyFor("notbuilt", "declined").Why, stateCopyFor("notbuilt", "deferred").Why; a == b {
		t.Error("declined and deferred read the same, but one is a decision and the other is a todo")
	}
}

// A service the stack was started without answers 503 from the console's own
// transport. That is a choice about this stack, not the emulator breaking, so
// the stamp outranks the 5xx reading.
func TestAServiceThatIsNotRunningIsNotAFault(t *testing.T) {
	if got := callState(503, &Refusal{Code: "ServiceUnavailable"}, wireFacts{Local: "service-not-running"}); got != "refusedhere" {
		t.Errorf("a stamped 503 reads %q, want refusedhere", got)
	}
	if got := callState(503, &Refusal{Code: "ServiceUnavailable"}, wireFacts{}); got != "error" {
		t.Errorf("an unstamped 503 reads %q, want error", got)
	}
}
