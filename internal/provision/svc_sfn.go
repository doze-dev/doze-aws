package provision

// Step Functions apply: state machines. Create-or-update, like everything
// else here — CreateStateMachine answers StateMachineAlreadyExists on a
// second deploy, which converges into UpdateStateMachine.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func applyStateMachines(ctx context.Context, c *client, s *Stack, rep *Report) error {
	if err := applyActivities(ctx, c, s, rep); err != nil {
		return err
	}
	for _, name := range sortedNames(s.StateMachines) {
		sm := s.StateMachines[name]
		in := map[string]any{
			"name":       name,
			"definition": sm.Definition,
			"roleArn":    sm.RoleARN,
		}
		if sm.Type != "" {
			in["type"] = sm.Type
		}
		if !sm.Logging.IsZero() {
			in["loggingConfiguration"] = json.RawMessage(sm.Logging.JSON)
		}
		if len(sm.Tags) > 0 {
			var tags []map[string]string
			for _, k := range sortedNames(sm.Tags) {
				tags = append(tags, map[string]string{"key": k, "value": sm.Tags[k]})
			}
			in["tags"] = tags
		}
		_, err := c.sfn(ctx, "CreateStateMachine", in)
		if err == nil {
			rep.add("created", "statemachine/"+name, "")
		} else {
			var ae *apiErr
			if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "StateMachineAlreadyExists") {
				return fmt.Errorf("state machine %q: %w", name, err)
			}
			upd := map[string]any{
				"stateMachineArn": stateMachineARN(c.id, name),
				"definition":      sm.Definition,
			}
			if sm.RoleARN != "" {
				upd["roleArn"] = sm.RoleARN
			}
			// Logging is replaced with what the stack says, off included:
			// a template that dropped its Logging block means off.
			upd["loggingConfiguration"] = json.RawMessage(`{"level":"OFF","includeExecutionData":false}`)
			if !sm.Logging.IsZero() {
				upd["loggingConfiguration"] = json.RawMessage(sm.Logging.JSON)
			}
			if _, err := c.sfn(ctx, "UpdateStateMachine", upd); err != nil {
				return fmt.Errorf("state machine %q update: %w", name, err)
			}
			rep.add("updated", "statemachine/"+name, "definition")
		}
		if err := applyVersionAndAliases(ctx, c, name, sm, rep); err != nil {
			return err
		}
	}
	return nil
}

// applyVersionAndAliases publishes the machine's current revision when the
// stack asks for a version, then points every alias at it. Publishing is
// idempotent on an unchanged revision, and an alias converges through
// UpdateStateMachineAlias, so a repeated deploy changes nothing.
func applyVersionAndAliases(ctx context.Context, c *client, name string, sm StateMachine, rep *Report) error {
	if !sm.Publish && len(sm.Aliases) == 0 {
		return nil
	}
	out, err := c.sfn(ctx, "PublishStateMachineVersion", map[string]any{"stateMachineArn": stateMachineARN(c.id, name)})
	if err != nil {
		return fmt.Errorf("state machine %q publish: %w", name, err)
	}
	var published struct {
		ARN string `json:"stateMachineVersionArn"`
	}
	json.Unmarshal(out, &published)
	rep.add("published", "statemachine/"+name, published.ARN)

	for _, alias := range sortedNames(sm.Aliases) {
		routing := []map[string]any{{"stateMachineVersionArn": published.ARN, "weight": 100}}
		in := map[string]any{"name": alias, "routingConfiguration": routing}
		if d := sm.Aliases[alias].Description; d != "" {
			in["description"] = d
		}
		if _, err := c.sfn(ctx, "CreateStateMachineAlias", in); err == nil {
			rep.add("created", "statemachine/"+name+"/alias/"+alias, published.ARN)
			continue
		} else {
			var ae *apiErr
			if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "ConflictException") {
				return fmt.Errorf("state machine %q alias %q: %w", name, alias, err)
			}
		}
		upd := map[string]any{"stateMachineAliasArn": stateMachineARN(c.id, name) + ":" + alias, "routingConfiguration": routing}
		if d := sm.Aliases[alias].Description; d != "" {
			upd["description"] = d
		}
		if _, err := c.sfn(ctx, "UpdateStateMachineAlias", upd); err != nil {
			return fmt.Errorf("state machine %q alias %q update: %w", name, alias, err)
		}
		rep.add("updated", "statemachine/"+name+"/alias/"+alias, published.ARN)
	}
	return nil
}

// applyActivities creates each activity; CreateActivity on an existing name
// answers the existing activity, so this converges without a lookup.
func applyActivities(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Activities) {
		a := s.Activities[name]
		in := map[string]any{"name": name}
		if len(a.Tags) > 0 {
			in["tags"] = tagList(a.Tags, "key", "value")
		}
		if _, err := c.sfn(ctx, "CreateActivity", in); err != nil {
			return fmt.Errorf("activity %q: %w", name, err)
		}
		rep.add("created", "activity/"+name, "")
	}
	return nil
}

// sfn speaks the Step Functions wire: awsJson 1.0 under the AWSStepFunctions
// target — the one service here on 1.0, with lowercase-initial members.
func (c *client) sfn(ctx context.Context, action string, in any) ([]byte, error) {
	return c.jsonTarget(ctx, "AWSStepFunctions."+action, "application/x-amz-json-1.0", in)
}

func exportStateMachines(ctx context.Context, c *client, s *Stack) error {
	out, err := c.sfn(ctx, "ListStateMachines", map[string]any{})
	if err != nil {
		return fmt.Errorf("list state machines: %w", err)
	}
	var list struct {
		StateMachines []struct {
			Arn  string `json:"stateMachineArn"`
			Name string `json:"name"`
		} `json:"stateMachines"`
	}
	json.Unmarshal(out, &list)
	for _, m := range list.StateMachines {
		desc, err := c.sfn(ctx, "DescribeStateMachine", map[string]any{"stateMachineArn": m.Arn})
		if err != nil {
			return fmt.Errorf("describe state machine %q: %w", m.Name, err)
		}
		var d struct {
			Definition string          `json:"definition"`
			RoleArn    string          `json:"roleArn"`
			Type       string          `json:"type"`
			Logging    json.RawMessage `json:"loggingConfiguration"`
		}
		json.Unmarshal(desc, &d)
		if s.StateMachines == nil {
			s.StateMachines = map[string]StateMachine{}
		}
		sm := StateMachine{Definition: d.Definition, RoleARN: d.RoleArn, Type: d.Type}
		var lvl struct {
			Level string `json:"level"`
		}
		if json.Unmarshal(d.Logging, &lvl) == nil && lvl.Level != "" && lvl.Level != "OFF" {
			sm.Logging = Doc{JSON: string(d.Logging)}
		}
		if err := exportVersionAndAliases(ctx, c, m.Arn, &sm); err != nil {
			return fmt.Errorf("state machine %q: %w", m.Name, err)
		}
		s.StateMachines[m.Name] = sm
	}
	return exportActivities(ctx, c, s)
}

// exportVersionAndAliases marks a machine with published versions as one to
// publish, and carries its aliases by name — their routing is not exported,
// because the apply side always points an alias at the version it publishes.
func exportVersionAndAliases(ctx context.Context, c *client, arn string, sm *StateMachine) error {
	out, err := c.sfn(ctx, "ListStateMachineVersions", map[string]any{"stateMachineArn": arn})
	if err != nil {
		return fmt.Errorf("list versions: %w", err)
	}
	var versions struct {
		Versions []json.RawMessage `json:"stateMachineVersions"`
	}
	json.Unmarshal(out, &versions)
	sm.Publish = len(versions.Versions) > 0

	out, err = c.sfn(ctx, "ListStateMachineAliases", map[string]any{"stateMachineArn": arn})
	if err != nil {
		return fmt.Errorf("list aliases: %w", err)
	}
	var aliases struct {
		Aliases []struct {
			ARN string `json:"stateMachineAliasArn"`
		} `json:"stateMachineAliases"`
	}
	json.Unmarshal(out, &aliases)
	for _, a := range aliases.Aliases {
		desc, err := c.sfn(ctx, "DescribeStateMachineAlias", map[string]any{"stateMachineAliasArn": a.ARN})
		if err != nil {
			return fmt.Errorf("describe alias %q: %w", a.ARN, err)
		}
		var d struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		json.Unmarshal(desc, &d)
		if sm.Aliases == nil {
			sm.Aliases = map[string]StateMachineAlias{}
		}
		sm.Aliases[d.Name] = StateMachineAlias{Description: d.Description}
	}
	return nil
}

func exportActivities(ctx context.Context, c *client, s *Stack) error {
	out, err := c.sfn(ctx, "ListActivities", map[string]any{})
	if err != nil {
		return fmt.Errorf("list activities: %w", err)
	}
	var list struct {
		Activities []struct {
			Name string `json:"name"`
		} `json:"activities"`
	}
	json.Unmarshal(out, &list)
	for _, a := range list.Activities {
		if s.Activities == nil {
			s.Activities = map[string]Activity{}
		}
		s.Activities[a.Name] = Activity{}
	}
	return nil
}

// destroyStateMachines deletes each machine — the service deletes its
// versions and aliases with it, as AWS does — and then the activities.
func destroyStateMachines(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.StateMachines) {
		_, err := c.sfn(ctx, "DeleteStateMachine", map[string]any{
			"stateMachineArn": stateMachineARN(c.id, name),
		})
		record(rep, "statemachine/"+name, err)
	}
	for _, name := range sortedNames(s.Activities) {
		_, err := c.sfn(ctx, "DeleteActivity", map[string]any{
			"activityArn": c.id.ARN("states", "activity:"+name),
		})
		record(rep, "activity/"+name, err)
	}
	return nil
}
