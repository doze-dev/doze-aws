package sqs

// Where the service model is wrong about the service.
//
// validate.go is generated from AWS's model and is right about nearly
// everything. This file is for the places where AWS does not do what its own
// model says, kept apart so that regenerating the tables cannot quietly put a
// refusal back.

import "github.com/doze-dev/doze-aws/internal/modelcheck"

func init() {
	// ReceiveMessage.AttributeNames is typed in the model as a list of
	// QueueAttributeName — the attributes of a QUEUE. It has only ever carried
	// the system attributes of a MESSAGE: SentTimestamp, ApproximateReceiveCount
	// and the rest. Not one of those is in the enum the model gives it.
	//
	// AWS accepts them, because that is what the parameter is for; it was
	// superseded by MessageSystemAttributeNames, which is typed correctly, and
	// kept for every consumer written before 2023. Enforcing the model here
	// refused the call boto3's own documentation uses as its example.
	//
	// So the old spelling accepts what the new one does, on top of what the
	// model lists. A name that is in neither is still refused.
	table := constraintTables["ReceiveMessage"]
	var system []string
	for _, c := range table {
		if c.Path == "MessageSystemAttributeNames[]" {
			system = c.Enum
		}
	}
	for i, c := range table {
		if c.Path == "AttributeNames[]" && c.Kind == modelcheck.KindEnum {
			table[i].Enum = append(append([]string{}, c.Enum...), system...)
		}
	}
}
