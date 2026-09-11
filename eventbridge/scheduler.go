package eventbridge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awscron"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// idleScan is how often the scheduler looks for schedule rules once it has
// found none. A bus with no schedules is the normal state of a local stack, and
// checking every second meant opening a bbolt read transaction per bus per
// second, forever, to be told nothing again — the single largest source of
// wakeups on an idle process.
//
// The cost of backing off is arming latency, not a missed fire: a rule is armed
// the first time the scheduler sees it and its clock starts there, so a rule
// created during an idle stretch begins its interval up to idleScan late rather
// than firing late. The first tick of a rate(1 minute) rule can therefore land
// as much as five seconds after it otherwise would, and every tick after it is
// exact.
const idleScan = 5 * time.Second

// runScheduler fires any enabled schedule-expression rule that is due,
// delivering a "Scheduled Event" to its targets. rate(...) fires when its
// interval has elapsed since the last firing; cron(...) fires at the
// expression's next time after the last firing (internal/awscron). A rule is
// armed the first time the scheduler sees it and fires from then on: nothing is
// persisted across a restart and nothing missed during one is replayed, which
// is also what a rule that was disabled gets on AWS. Runs in a single
// goroutine, so the maps need no lock.
//
// It ticks once a second while any schedule rule exists and every idleScan
// while none does.
func (s *Server) runScheduler(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastFired := map[string]time.Time{}
	compiled := map[string]*awscron.Expression{}
	slow := false
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// A second is the resolution schedules need while any exist; it is
			// pure waste while none do.
			if armed := s.fireDueSchedules(lastFired, compiled); armed == 0 && !slow {
				ticker.Reset(idleScan)
				slow = true
			} else if armed > 0 && slow {
				ticker.Reset(time.Second)
				slow = false
			}
		}
	}
}

// nextFire is when a schedule fires next, given when it last did.
func nextFire(schedule string, last time.Time, compiled map[string]*awscron.Expression) (time.Time, bool) {
	if interval, ok := parseRate(schedule); ok {
		return last.Add(interval), true
	}
	e, ok := compiled[schedule]
	if !ok {
		var err error
		if e, err = awscron.Parse(schedule); err != nil {
			return time.Time{}, false
		}
		compiled[schedule] = e
	}
	return e.Next(last)
}

// fireDueSchedules fires every schedule rule the clock has passed and returns
// how many enabled schedule rules it saw, which is what tells the caller
// whether a one-second cadence is buying anything.
func (s *Server) fireDueSchedules(lastFired map[string]time.Time, compiled map[string]*awscron.Expression) (armed int) {
	buses, err := s.store.ListBuses()
	if err != nil {
		return 0
	}
	now := s.now()
	// A rule that is disabled, deleted or no longer scheduled loses its
	// clock, so re-enabling or re-creating it arms it afresh rather than
	// firing at once for the time it spent off.
	live := map[string]bool{}
	for _, bus := range buses {
		rules, err := s.store.Rules(bus.Name, "")
		if err != nil {
			continue
		}
		for _, rule := range rules {
			if rule.State != "ENABLED" || rule.Schedule == "" {
				continue
			}
			key := bus.Name + "\x00" + rule.Name
			live[key] = true
			armed++
			last, seen := lastFired[key]
			if !seen {
				// First sighting: start the clock, don't fire immediately.
				lastFired[key] = now
				continue
			}
			next, ok := nextFire(rule.Schedule, last, compiled)
			if !ok || next.After(now) {
				continue
			}
			lastFired[key] = now
			s.fireScheduled(rule)
		}
	}
	for key := range lastFired {
		if !live[key] {
			delete(lastFired, key)
		}
	}
	return armed
}

// fireScheduled delivers the canonical EventBridge "Scheduled Event" to a rule's
// targets (a scheduled rule fires on time, not by pattern match).
func (s *Server) fireScheduled(rule Rule) {
	doc := map[string]any{
		"version":     "0",
		"id":          awshttp.RequestID(),
		"detail-type": "Scheduled Event",
		"source":      "aws.events",
		"account":     awsident.AccountID,
		"time":        awshttp.ISO8601(s.now()),
		"region":      awsident.Region,
		"resources":   []string{awsident.ARN("events", "rule/"+rule.Name)},
		"detail":      json.RawMessage("{}"),
	}
	eventJSON, err := json.Marshal(doc)
	if err != nil {
		return
	}
	for _, target := range rule.Targets {
		// A scheduled rule has no caller: the timer fired, so there is nothing
		// to attribute this to and it belongs on the wire as a root.
		s.dispatch(context.Background(), rule, target, eventJSON)
	}
}

// parseRate parses a rate(value unit) schedule expression into an interval.
// Units: minute(s), hour(s), day(s). Returns ok=false for anything else
// (cron(...), malformed) so the caller skips it.
func parseRate(expr string) (time.Duration, bool) {
	inner, ok := strings.CutPrefix(strings.TrimSpace(expr), "rate(")
	if !ok || !strings.HasSuffix(inner, ")") {
		return 0, false
	}
	fields := strings.Fields(strings.TrimSuffix(inner, ")"))
	if len(fields) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 {
		return 0, false
	}
	unit := strings.TrimSuffix(strings.ToLower(fields[1]), "s")
	switch unit {
	case "minute":
		return time.Duration(n) * time.Minute, true
	case "hour":
		return time.Duration(n) * time.Hour, true
	case "day":
		return time.Duration(n) * 24 * time.Hour, true
	default:
		return 0, false
	}
}
