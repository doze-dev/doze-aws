package provision

// Apply orchestration: the report shape and the dependency-ordered phase walk.
// Per-service apply helpers live in the svc_*.go files.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/doze-dev/doze-aws/awsident"
)

// Action is one thing Apply did (or decided not to do).
type Action struct {
	Op       string // created | updated | skipped
	Resource string // e.g. "queue/orders"
	Detail   string
}

// Report is the full apply outcome.
type Report struct {
	Actions []Action
}

func (r *Report) add(op, resource, detail string) {
	r.Actions = append(r.Actions, Action{Op: op, Resource: resource, Detail: detail})
}

// Counts summarizes the report as created/updated/skipped.
func (r *Report) Counts() (created, updated, skipped int) {
	for _, a := range r.Actions {
		switch a.Op {
		case "created":
			created++
		case "updated":
			updated++
		default:
			skipped++
		}
	}
	return
}

// Apply converges the running stack toward the file: resources are created if
// missing and cheaply updated if present; nothing is ever deleted. Phases run
// in dependency order so references by name always resolve.
func Apply(ctx context.Context, gateway http.Handler, s *Stack, id awsident.Identity) (*Report, error) {
	c := newClient(gateway, id)
	rep := &Report{}

	type phase struct {
		name string
		run  func() error
	}
	phases := []phase{
		{"queues", func() error { return applyQueues(ctx, c, s, rep) }},
		{"tables", func() error { return applyTables(ctx, c, s, rep) }},
		{"keys", func() error { return applyKeys(ctx, c, s, rep) }},
		{"buckets", func() error { return applyBuckets(ctx, c, s, rep) }},
		{"functions", func() error { return applyFunctions(ctx, c, s, rep) }},
		{"topics", func() error { return applyTopics(ctx, c, s, rep) }},
		{"connections", func() error { return applyConnections(ctx, c, s, rep) }},
		{"rules", func() error { return applyRules(ctx, c, s, rep) }},
		{"notifications", func() error { return applyNotifications(ctx, c, s, rep) }},
		{"loggroups", func() error { return applyLogGroups(ctx, c, s, rep) }},
		{"secrets", func() error { return applySecrets(ctx, c, s, rep) }},
		{"parameters", func() error { return applyParameters(ctx, c, s, rep) }},
		{"apis", func() error { return applyAPIs(ctx, c, s, rep) }},
		{"apikeys", func() error { return applyAPIKeys(ctx, c, s, rep) }},
		// State machines last: their definitions reference functions, queues
		// and topics by ARN, so everything they orchestrate already exists.
		{"statemachines", func() error { return applyStateMachines(ctx, c, s, rep) }},
		// Last: an alarm names the topics and functions it notifies, and
		// PutMetricAlarm refuses an action it cannot deliver.
		{"alarms", func() error { return applyAlarms(ctx, c, s, rep) }},
	}
	for _, p := range phases {
		if err := p.run(); err != nil {
			return rep, fmt.Errorf("stackfile: %s: %w", p.name, err)
		}
	}
	return rep, nil
}
