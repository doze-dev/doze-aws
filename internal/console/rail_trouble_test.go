package console

import (
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

// What the ring says about a service in the last minute, for the rail's dots:
// a call a policy denied warns, a failure (5xx) is bad, a refusal (a 4xx the
// service gave for the input) and a 404 say nothing, and a call older than the
// window has been forgiven.
func TestTheRingsTroubleInTheLastMinute(t *testing.T) {
	rec := NewRecorder(nil, awsident.Default())
	now := time.Now()
	add := func(seq int64, ago time.Duration, svc string, status int, denied ...string) {
		e := TrafficEntry{At: now.Add(-ago), Service: svc, Action: "X", Status: status}
		if len(denied) > 0 {
			e.Denied = denied[0]
		}
		rec.addAt(seq, e)
	}
	add(1, 5*time.Minute, "kms", 500)        // too old
	add(2, 10*time.Second, "sqs", 400)       // refused: nothing
	add(3, 8*time.Second, "s3", 404)         // not found: nothing
	add(4, 6*time.Second, "lambda", 502)     // failed
	add(5, 4*time.Second, "lambda", 400)     // a refusal after the failure: the failure still wins
	add(6, 3*time.Second, "sns", 403, "iam") // denied
	add(7, 2*time.Second, "ddb", 200)        // fine

	got := rec.trouble(now, time.Minute)
	want := map[string]string{"lambda": "bad", "sns": "warn"}
	if len(got) != len(want) {
		t.Fatalf("trouble = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("trouble[%s] = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}
