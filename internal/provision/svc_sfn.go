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
			continue
		}
		var ae *apiErr
		if !asAPIErr(err, &ae) || !strings.Contains(ae.body, "StateMachineAlreadyExists") {
			return fmt.Errorf("state machine %q: %w", name, err)
		}
		upd := map[string]any{
			"stateMachineArn": stateMachineARN(name),
			"definition":      sm.Definition,
		}
		if sm.RoleARN != "" {
			upd["roleArn"] = sm.RoleARN
		}
		if _, err := c.sfn(ctx, "UpdateStateMachine", upd); err != nil {
			return fmt.Errorf("state machine %q update: %w", name, err)
		}
		rep.add("updated", "statemachine/"+name, "definition")
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
			Definition string `json:"definition"`
			RoleArn    string `json:"roleArn"`
			Type       string `json:"type"`
		}
		json.Unmarshal(desc, &d)
		if s.StateMachines == nil {
			s.StateMachines = map[string]StateMachine{}
		}
		s.StateMachines[m.Name] = StateMachine{
			Definition: d.Definition, RoleARN: d.RoleArn, Type: d.Type,
		}
	}
	return nil
}

func destroyStateMachines(ctx context.Context, c *client, s *Stack, rep *DestroyReport) error {
	for _, name := range sortedNames(s.StateMachines) {
		_, err := c.sfn(ctx, "DeleteStateMachine", map[string]any{
			"stateMachineArn": stateMachineARN(name),
		})
		record(rep, "statemachine/"+name, err)
	}
	return nil
}
