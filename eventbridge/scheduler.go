package eventbridge

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// runScheduler ticks once a second and fires any enabled schedule-expression
// rule whose rate interval has elapsed, delivering a "Scheduled Event" to its
// targets. Only rate(...) is fired locally; cron(...) rules are accepted and
// stored but not driven (documented in docs/api-support/eventbridge.md) — a
// wall-clock cron isn't useful in an ephemeral local stack. Runs in a single
// goroutine, so its lastFired map needs no lock.
func (s *Server) runScheduler() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastFired := map[string]time.Time{}
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.fireDueSchedules(lastFired)
		}
	}
}

func (s *Server) fireDueSchedules(lastFired map[string]time.Time) {
	buses, err := s.store.ListBuses()
	if err != nil {
		return
	}
	now := s.now()
	for _, bus := range buses {
		rules, err := s.store.Rules(bus.Name, "")
		if err != nil {
			continue
		}
		for _, rule := range rules {
			if rule.State != "ENABLED" || rule.Schedule == "" {
				continue
			}
			interval, ok := parseRate(rule.Schedule)
			if !ok {
				continue // cron(...) or malformed — not driven locally
			}
			key := bus.Name + "\x00" + rule.Name
			last, seen := lastFired[key]
			if !seen {
				// First sighting: start the clock, don't fire immediately.
				lastFired[key] = now
				continue
			}
			if now.Sub(last) < interval {
				continue
			}
			lastFired[key] = now
			s.fireScheduled(rule)
		}
	}
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
		s.dispatch(rule, target, eventJSON)
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
