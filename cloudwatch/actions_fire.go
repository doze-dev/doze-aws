package cloudwatch

// Firing an alarm's actions.
//
// AWS supports a long list of action targets — SNS, Lambda, EC2 actions, Auto
// Scaling policies, Systems Manager OpsItems. Two of those exist locally, and
// the rest are refused at PutMetricAlarm rather than accepted and silently
// never fired, which would be the worse failure: an alarm that looks wired up
// and does nothing.
//
// The payload is AWS's alarm JSON, unchanged, because the whole point of
// testing an alarm locally is that the handler on the other end sees what it
// will see in the cloud.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/internal/trace"
	"github.com/doze-dev/doze-aws/peers"
)

// actionTarget is what an action ARN points at.
type actionTarget int

const (
	targetUnknown actionTarget = iota
	targetSNS
	targetLambda
)

// classifyAction reads an action ARN. Anything that is not a local SNS topic
// or Lambda function is refused when the alarm is created, so this only has
// to tell the two apart at fire time.
func classifyAction(arn string) (actionTarget, string) {
	switch {
	case strings.Contains(arn, ":sns:"):
		return targetSNS, arn[strings.LastIndex(arn, ":")+1:]
	case strings.Contains(arn, ":lambda:"):
		// arn:aws:lambda:region:account:function:name[:qualifier]
		parts := strings.Split(arn, ":")
		if len(parts) >= 7 {
			return targetLambda, parts[6]
		}
	}
	return targetUnknown, ""
}

// alarmPayload is AWS's alarm notification, which is what a subscriber
// parses. The field names are AWS's, not doze-aws's.
func (s *Server) alarmPayload(a *alarm, prev, now string, reason string, at time.Time) map[string]any {
	dims := make([]map[string]string, 0, len(a.Dimensions))
	for _, d := range dimensionViews(a.Dimensions) {
		dims = append(dims, map[string]string{"name": d.Name, "value": d.Value})
	}
	return map[string]any{
		"AlarmName":                          a.Name,
		"AlarmDescription":                   a.Description,
		"AWSAccountId":                       s.id.Account(),
		"AlarmConfigurationUpdatedTimestamp": time.UnixMilli(a.UpdatedMs).UTC().Format(time.RFC3339),
		"NewStateValue":                      now,
		"NewStateReason":                     reason,
		"StateChangeTime":                    at.UTC().Format(time.RFC3339),
		"Region":                             s.id.RegionName(),
		"AlarmArn":                           a.ARN(),
		"OldStateValue":                      prev,
		"OKActions":                          a.OKActions,
		"AlarmActions":                       a.AlarmActions,
		"InsufficientDataActions":            a.InsufficientData,
		"Trigger": map[string]any{
			"MetricName":         a.MetricName,
			"Namespace":          a.Namespace,
			"StatisticType":      "Statistic",
			"Statistic":          strings.ToUpper(a.Statistic),
			"Unit":               a.Unit,
			"Dimensions":         dims,
			"Period":             a.Period,
			"EvaluationPeriods":  a.EvaluationPeriods,
			"DatapointsToAlarm":  a.DatapointsToAlarm,
			"ComparisonOperator": a.ComparisonOp,
			"Threshold":          a.Threshold,
			"TreatMissingData":   a.TreatMissingData,
		},
	}
}

// fireActions delivers the notifications for a transition.
//
// Delivery is off the evaluator's path: an unreachable topic must not stall
// the next alarm's evaluation, and the evaluator has no request context to
// inherit — which is also why the trace sink is set separately (dozeaws.go).
func (s *Server) fireActions(a *alarm, prev, now, reason string, at time.Time) {
	if !a.ActionsEnabled {
		return
	}
	targets := a.actionsFor(now)
	if len(targets) == 0 {
		return
	}
	payload, err := json.Marshal(s.alarmPayload(a, prev, now, reason, at))
	if err != nil {
		s.logf("cloudwatch: building the alarm payload for %s: %v", a.Name, err)
		return
	}
	// The evaluator has no request context, so the sink is attached here.
	ctx := trace.With(context.Background(), s.sink, 0)
	// The principal is the service on behalf of the alarm, so a topic policy
	// conditioned on aws:SourceArn evaluates the way it would on AWS.
	ctx = peers.WithPrincipal(ctx, "cloudwatch", a.ARN())
	subject := fmt.Sprintf("%s: %q in %s", now, a.Name, s.id.RegionName())

	for _, arn := range targets {
		kind, name := classifyAction(arn)
		bg.Go(s.logf, "cloudwatch: alarm action", func() {
			s.deliver(ctx, a, kind, arn, name, subject, payload)
		})
	}
}

func (s *Server) deliver(ctx context.Context, a *alarm, kind actionTarget,
	arn, name, subject string, payload []byte) {
	ev := trace.Event{Service: "cloudwatch", Action: "AlarmAction",
		Resource: arn, Via: "cloudwatch:" + a.Name}
	err := trace.Step(ctx, ev, func(ctx context.Context) error {
		switch kind {
		case targetSNS:
			// Detailed, for the Subject line: an alarm notification in an inbox
			// is much less useful without one.
			_, err := peercall.SNSPublishDetailed(ctx, s.peers, arn, string(payload), subject)
			return err
		case targetLambda:
			return peercall.LambdaInvokeAsync(ctx, s.peers, name, payload)
		}
		return fmt.Errorf("unsupported action target %s", arn)
	})
	if err != nil {
		// A failed notification is logged, not retried. AWS retries for a
		// day; a local emulator that did the same would keep a broken target
		// busy for the rest of the session with no way to see why.
		s.logf("cloudwatch: alarm %s action %s: %v", a.Name, arn, err)
		return
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return appendHistory(tx, historyEntry{
			AlarmName: a.Name, Type: historyAction, AtMs: s.now().UnixMilli(),
			Summary: "Successfully executed action " + arn,
		})
	}); err != nil {
		s.logf("cloudwatch: recording the action for %s: %v", a.Name, err)
	}
}
