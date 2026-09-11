package cloudformation

// Export of Step Functions state machines, their published versions and aliases, and activities.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitStateMachines(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.StateMachines) {
		sm := s.StateMachines[name]
		props := map[string]any{
			"StateMachineName": name,
			"DefinitionString": sm.Definition,
		}
		if sm.RoleARN != "" {
			props["RoleArn"] = sm.RoleARN
		}
		if sm.Type != "" && sm.Type != "STANDARD" {
			props["StateMachineType"] = sm.Type
		}
		if !sm.Logging.IsZero() {
			props["LoggingConfiguration"] = upperKeys(rawDoc(sm.Logging))
		}
		add("StateMachine", name, "AWS::StepFunctions::StateMachine", props)
		if sm.Publish || len(sm.Aliases) > 0 {
			// One version resource per machine; every alias routes to it.
			add("StateMachineVersion", name, "AWS::StepFunctions::StateMachineVersion", map[string]any{
				"StateMachineArn": map[string]any{"Ref": logicalID("StateMachine", name)},
			})
			for _, alias := range sortedNames(sm.Aliases) {
				props := map[string]any{
					"Name": alias,
					"RoutingConfiguration": []any{map[string]any{
						"StateMachineVersionArn": map[string]any{"Ref": logicalID("StateMachineVersion", name)},
						"Weight":                 100,
					}},
				}
				if d := sm.Aliases[alias].Description; d != "" {
					props["Description"] = d
				}
				add("StateMachineAlias", name+alias, "AWS::StepFunctions::StateMachineAlias", props)
			}
		}
	}
	for _, name := range sortedNames(s.Activities) {
		add("Activity", name, "AWS::StepFunctions::Activity", map[string]any{"Name": name})
	}
}
