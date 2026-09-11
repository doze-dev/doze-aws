package eventbridge

// Nothing asserted that a scheduled rule delivers anything.
//
// The three scheduler tests drive the due-logic and assert the lastFired
// bookkeeping; TestFireDueSchedules says so in its own comment — "a scheduled
// rule with no targets: firing is a no-op delivery". The loop in
// fireScheduled that walks rule.Targets was uncovered, and the string
// "Scheduled Event" appeared nowhere outside scheduler.go. So the whole point
// of a cron rule — that at the appointed minute a document with a particular
// shape arrives at a target — was untested at every level.
//
// The envelope is a contract. A function triggered on a schedule reads
// detail-type to tell a scheduled invocation from a pattern-matched one, and
// resources[0] to tell which rule woke it.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awscron"
	"github.com/doze-dev/doze-aws/peers"
)

// TestScheduledRuleDeliversTheScheduledEvent fires a rate rule at a fake
// Lambda peer and reads the document off the wire.
func TestScheduledRuleDeliversTheScheduledEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	var mu sync.Mutex
	var bodies [][]byte
	var paths []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer fake.Close()

	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC) // a Monday
	s, err := New(Options{
		DataDir: t.TempDir(), Logf: t.Logf,
		Clock: func() time.Time { return now },
		Peers: peers.Static{"lambda": peers.Endpoint{Client: fake.Client(), BaseURL: fake.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rule := Rule{Bus: DefaultBus, Name: "nightly", Schedule: "rate(1 hour)", State: "ENABLED",
		Targets: []Target{{ID: "t1", ARN: awsident.ARN("lambda", "function:sweeper")}}}
	if err := s.store.PutRule(rule); err != nil {
		t.Fatal(err)
	}

	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}
	s.fireDueSchedules(lastFired, compiled) // first sighting arms, does not fire

	mu.Lock()
	n := len(bodies)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the first sighting must arm, not fire: %d deliveries", n)
	}

	// Move the clock past the interval and fire.
	now = now.Add(90 * time.Minute)
	s.fireDueSchedules(lastFired, compiled)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n = len(bodies)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("want exactly one delivery, got %d", len(bodies))
	}

	var doc map[string]any
	if err := json.Unmarshal(bodies[0], &doc); err != nil {
		t.Fatalf("the delivered event is not JSON: %v\n%s", err, bodies[0])
	}
	if doc["detail-type"] != "Scheduled Event" {
		t.Errorf("detail-type = %v, want %q — a function tells a scheduled invocation from a matched one by this", doc["detail-type"], "Scheduled Event")
	}
	if doc["source"] != "aws.events" {
		t.Errorf("source = %v, want aws.events", doc["source"])
	}
	if doc["account"] != awsident.AccountID || doc["region"] != awsident.Region {
		t.Errorf("account/region = %v/%v", doc["account"], doc["region"])
	}
	if doc["version"] != "0" {
		t.Errorf("version = %v, want \"0\"", doc["version"])
	}
	res, _ := doc["resources"].([]any)
	wantARN := awsident.ARN("events", "rule/nightly")
	if len(res) != 1 || res[0] != wantARN {
		t.Errorf("resources = %v, want [%s] — this is how a target knows which rule woke it", res, wantARN)
	}
	detail, ok := doc["detail"].(map[string]any)
	if !ok || len(detail) != 0 {
		t.Errorf("detail = %v, want an empty object", doc["detail"])
	}
	if doc["id"] == nil || doc["id"] == "" {
		t.Error("the event needs an id")
	}
	// The clock drives the timestamp, not wall time.
	if doc["time"] != now.UTC().Format("2006-01-02T15:04:05Z") {
		t.Errorf("time = %v, want the server clock's %v", doc["time"], now.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if paths[0] == "" {
		t.Error("the delivery must name the function in its path")
	}
}

// TestScheduledRuleDeliversToEveryTarget: a rule fans out, and one target
// failing must not stop the rest. Both halves were uncovered.
func TestScheduledRuleDeliversToEveryTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	var mu sync.Mutex
	var hits []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer fake.Close()

	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	s, err := New(Options{
		DataDir: t.TempDir(), Logf: t.Logf,
		Clock: func() time.Time { return now },
		// Only lambda resolves: the SQS target below has nowhere to go, and
		// must be logged rather than aborting the fan-out.
		Peers: peers.Static{"lambda": peers.Endpoint{Client: fake.Client(), BaseURL: fake.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "fan", Schedule: "rate(1 minute)", State: "ENABLED",
		Targets: []Target{
			{ID: "gone", ARN: awsident.ARN("sqs", "no-such-queue")},
			{ID: "a", ARN: awsident.ARN("lambda", "function:one")},
			{ID: "b", ARN: awsident.ARN("lambda", "function:two")},
		}}); err != nil {
		t.Fatal(err)
	}

	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}
	s.fireDueSchedules(lastFired, compiled)
	now = now.Add(2 * time.Minute)
	s.fireDueSchedules(lastFired, compiled)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(hits)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("both reachable targets must be delivered to despite the unreachable one: got %v", hits)
	}
}

// TestDisabledScheduleDeliversNothing: the state check is what a developer
// relies on when they disable a rule to stop it paging them.
func TestDisabledScheduleDeliversNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a store")
	}
	var mu sync.Mutex
	delivered := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		delivered++
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer fake.Close()

	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	s, err := New(Options{
		DataDir: t.TempDir(), Logf: t.Logf,
		Clock: func() time.Time { return now },
		Peers: peers.Static{"lambda": peers.Endpoint{Client: fake.Client(), BaseURL: fake.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "off", Schedule: "rate(1 minute)", State: "DISABLED",
		Targets: []Target{{ID: "t", ARN: awsident.ARN("lambda", "function:sweeper")}}}); err != nil {
		t.Fatal(err)
	}
	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}
	s.fireDueSchedules(lastFired, compiled)
	now = now.Add(10 * time.Minute)
	s.fireDueSchedules(lastFired, compiled)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if delivered != 0 {
		t.Errorf("a disabled rule delivered %d events", delivered)
	}
}

// The scheduler ticks once a second while any schedule rule exists and backs
// off to idleScan while none does, which is what keeps an idle stack from
// waking up every second to re-read a bus that has nothing scheduled on it.
// fireDueSchedules reports the count that decision is made from, so the count
// has to mean "rules worth ticking for" and nothing else.
func TestArmedCountDrivesTheCadence(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}

	if armed := s.fireDueSchedules(lastFired, compiled); armed != 0 {
		t.Fatalf("a bus with no rules reported %d armed; the scheduler would tick every second for nothing", armed)
	}

	// A pattern rule is not a schedule: it fires on PutEvents, never on a clock.
	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "onpattern", State: "ENABLED",
		Pattern: `{"source":["shop"]}`}); err != nil {
		t.Fatal(err)
	}
	if armed := s.fireDueSchedules(lastFired, compiled); armed != 0 {
		t.Errorf("a pattern rule counted as armed (%d); only schedules need a clock", armed)
	}

	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "hourly", State: "ENABLED",
		Schedule: "rate(1 hour)"}); err != nil {
		t.Fatal(err)
	}
	if armed := s.fireDueSchedules(lastFired, compiled); armed != 1 {
		t.Errorf("armed = %d, want 1 — the scheduler would stay slow with a live schedule", armed)
	}

	// Disabling it must hand the clock back, or the stack keeps ticking for a
	// rule that can never fire.
	if err := s.store.PutRule(Rule{Bus: DefaultBus, Name: "hourly", State: "DISABLED",
		Schedule: "rate(1 hour)"}); err != nil {
		t.Fatal(err)
	}
	if armed := s.fireDueSchedules(lastFired, compiled); armed != 0 {
		t.Errorf("a disabled schedule still counted as armed (%d)", armed)
	}
}
