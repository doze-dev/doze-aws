package stackfile

// EventBridge apply + export: buses, rules, and targets.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ---- rules ----

func applyRules(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Rules) {
		r := s.Rules[name]
		bus := r.Bus
		if bus == "" {
			bus = "default"
		}
		if bus != "default" {
			// Idempotent-ish: create the bus, tolerating "already exists".
			if _, err := c.json11(ctx, "AWSEvents", "CreateEventBus", map[string]any{"Name": bus}); err != nil {
				var ae *apiErr
				if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "Exists") {
					return fmt.Errorf("rule %q bus: %w", name, err)
				}
			}
		}
		in := map[string]any{"Name": name}
		if bus != "default" {
			in["EventBusName"] = bus
		}
		if !r.Pattern.IsZero() {
			in["EventPattern"] = r.Pattern.JSON
		}
		if r.Schedule != "" {
			in["ScheduleExpression"] = r.Schedule
		}
		state := "ENABLED"
		if r.Enabled != nil && !*r.Enabled {
			state = "DISABLED"
		}
		in["State"] = state
		if _, err := c.json11(ctx, "AWSEvents", "PutRule", in); err != nil { // PutRule is an upsert
			return fmt.Errorf("rule %q: %w", name, err)
		}
		if len(r.Targets) > 0 {
			var targets []map[string]any
			for i, t := range r.Targets {
				arn := ""
				switch {
				case t.Queue != "":
					arn = queueARN(t.Queue)
				case t.Topic != "":
					arn = topicARN(t.Topic)
				case t.Lambda != "":
					arn = lambdaARN(t.Lambda)
				}
				tw := map[string]any{"Id": fmt.Sprintf("t%d", i+1), "Arn": arn}
				if !t.Input.IsZero() {
					tw["Input"] = t.Input.JSON
				}
				if t.InputPath != "" {
					tw["InputPath"] = t.InputPath
				}
				if t.Template != "" {
					it := map[string]any{"InputTemplate": t.Template}
					if len(t.Paths) > 0 {
						it["InputPathsMap"] = t.Paths
					}
					tw["InputTransformer"] = it
				}
				targets = append(targets, tw)
			}
			tin := map[string]any{"Rule": name, "Targets": targets}
			if bus != "default" {
				tin["EventBusName"] = bus
			}
			if _, err := c.json11(ctx, "AWSEvents", "PutTargets", tin); err != nil { // upsert by Id
				return fmt.Errorf("rule %q targets: %w", name, err)
			}
		}
		rep.add("updated", "rule/"+bus+"/"+name, "put (upsert)")
	}
	return nil
}

func exportRules(ctx context.Context, c *client, s *Stack) error {
	busOut, err := c.json11(ctx, "AWSEvents", "ListEventBuses", map[string]any{})
	if err != nil {
		return err
	}
	var buses struct {
		EventBuses []struct{ Name string }
	}
	json.Unmarshal(busOut, &buses)
	for _, bus := range buses.EventBuses {
		in := map[string]any{}
		if bus.Name != "default" {
			in["EventBusName"] = bus.Name
		}
		out, err := c.json11(ctx, "AWSEvents", "ListRules", in)
		if err != nil {
			continue
		}
		var rules struct {
			Rules []struct {
				Name               string
				EventPattern       string
				ScheduleExpression string
				State              string
			}
		}
		json.Unmarshal(out, &rules)
		for _, r := range rules.Rules {
			if s.Rules == nil {
				s.Rules = map[string]Rule{}
			}
			rule := Rule{Schedule: r.ScheduleExpression}
			if bus.Name != "default" {
				rule.Bus = bus.Name
			}
			if r.EventPattern != "" {
				rule.Pattern = Doc{JSON: r.EventPattern}
			}
			if r.State == "DISABLED" {
				v := false
				rule.Enabled = &v
			}
			tin := map[string]any{"Rule": r.Name}
			if bus.Name != "default" {
				tin["EventBusName"] = bus.Name
			}
			if out, err := c.json11(ctx, "AWSEvents", "ListTargetsByRule", tin); err == nil {
				var ts struct {
					Targets []struct {
						Arn              string
						Input            string
						InputPath        string
						InputTransformer *struct {
							InputTemplate string
							InputPathsMap map[string]string
						}
					}
				}
				json.Unmarshal(out, &ts)
				for _, t := range ts.Targets {
					leaf := arnLeaf(t.Arn)
					tgt := Target{}
					switch {
					case strings.Contains(t.Arn, ":sqs:"):
						tgt.Queue = leaf
					case strings.Contains(t.Arn, ":sns:"):
						tgt.Topic = leaf
					case strings.Contains(t.Arn, ":lambda:"):
						tgt.Lambda = strings.TrimPrefix(leaf, "function:")
					default:
						continue
					}
					if t.Input != "" {
						tgt.Input = Doc{JSON: t.Input}
					}
					tgt.InputPath = t.InputPath
					if t.InputTransformer != nil {
						tgt.Template = t.InputTransformer.InputTemplate
						if len(t.InputTransformer.InputPathsMap) > 0 {
							tgt.Paths = t.InputTransformer.InputPathsMap
						}
					}
					rule.Targets = append(rule.Targets, tgt)
				}
			}
			s.Rules[r.Name] = rule
		}
	}
	return nil
}
