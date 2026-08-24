package console

import "testing"

// The wire's whole claim is that a refusal, a denial and a fault are different
// events. If callState blurs them the claim is false, so it is worth a test of
// its own — especially the 403-with-no-parsed-body case, which is how a denial
// most often arrives.
func TestCallStateSeparatesTheThreeFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		ref    *Refusal
		want   string
	}{
		{"served", 200, nil, "served"},
		{"served with a body that parses to nothing", 201, nil, "served"},
		{"input constraint refused it", 400, &Refusal{Code: "InvalidParameterValue"}, "refused"},
		{"refused with no parsed reason", 400, nil, "refused"},
		{"403 is a denial even unparsed", 403, nil, "denied"},
		{"AccessDenied code is a denial", 400, &Refusal{Code: "AccessDeniedException"}, "denied"},
		{"NotAuthorized code is a denial", 400, &Refusal{Code: "NotAuthorized"}, "denied"},
		{"AuthorizationError code is a denial", 400, &Refusal{Code: "AuthorizationError"}, "denied"},
		{"the emulator broke", 500, nil, "error"},
		{"a 503 is still the emulator", 503, &Refusal{Code: "Whatever"}, "error"},
	} {
		if got := callState(c.status, c.ref); got != c.want {
			t.Errorf("%s: callState(%d, %v) = %q, want %q", c.name, c.status, c.ref, got, c.want)
		}
	}
}
