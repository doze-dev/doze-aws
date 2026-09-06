package stepfunctions_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

// The JSONata dialect through the real SDK: a machine that uses Assign,
// Output, Condition, a Map with Items and a Parallel is created, validated
// and run to SUCCEEDED, and its output is what AWS's documented semantics
// produce. Everything below the wire — $states, variables, branch scoping —
// is covered by the interpreter's own tests; this is the contract that the
// service accepts and runs the dialect at all.

const jsonataMachine = `{
  "Comment": "Order pipeline in JSONata",
  "QueryLanguage": "JSONata",
  "StartAt": "Remember",
  "States": {
    "Remember": {
      "Type": "Pass",
      "Assign": {"customer": "{% $states.input.customer.lastName %}", "threshold": 10},
      "Output": {"items": "{% $states.input.order.items %}"},
      "Next": "Price"
    },
    "Price": {
      "Type": "Map",
      "Items": "{% $states.input.items %}",
      "ItemSelector": {"line": "{% $states.context.Map.Item.Value %}", "n": "{% $states.context.Map.Item.Index %}"},
      "ItemProcessor": {
        "StartAt": "Line",
        "States": {"Line": {"Type": "Pass", "Output": "{% $states.input.line.price * $states.input.line.qty %}", "End": true}}
      },
      "Output": {"total": "{% $sum($states.result) %}", "lines": "{% $states.result %}"},
      "Assign": {"total": "{% $sum($states.result) %}"},
      "Next": "Big?"
    },
    "Big?": {
      "Type": "Choice",
      "Choices": [{"Condition": "{% $total > $threshold %}", "Next": "Both"}],
      "Default": "Done"
    },
    "Both": {
      "Type": "Parallel",
      "Arguments": {"total": "{% $total %}"},
      "Branches": [
        {"StartAt": "Tag", "States": {"Tag": {"Type": "Pass", "Output": {"tag": "{% 'big:' & $customer %}"}, "End": true}}},
        {"StartAt": "Wait", "States": {"Wait": {"Type": "Wait", "Seconds": "{% 0 %}", "Output": {"waited": "{% $states.input.total %}"}, "End": true}}}
      ],
      "Output": "{% $merge($states.result) %}",
      "Next": "Done"
    },
    "Done": {
      "Type": "Succeed",
      "Output": {"summary": "{% $states.input %}", "customer": "{% $customer %}", "chunks": "{% $partition([1,2,3], 2) %}"}
    }
  }
}`

func TestSDKJSONataMachineRuns(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	ok, err := c.ValidateStateMachineDefinition(ctx, &awssfn.ValidateStateMachineDefinitionInput{
		Definition: aws.String(jsonataMachine),
	})
	if err != nil {
		t.Fatalf("ValidateStateMachineDefinition: %v", err)
	}
	if ok.Result != sfntypes.ValidateStateMachineDefinitionResultCodeOk {
		t.Fatalf("validation refused a JSONata definition AWS accepts: %+v", ok.Diagnostics)
	}

	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("jsonata"),
		Definition: aws.String(jsonataMachine),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn,
		Input: aws.String(`{"customer": {"firstName": "Martha", "lastName": "Rivera"},
			"order": {"items": [{"price": 4, "qty": 3}, {"price": 2.5, "qty": 2}]}}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	desc := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)

	var got, want any
	if err := json.Unmarshal([]byte(aws.ToString(desc.Output)), &got); err != nil {
		t.Fatalf("output is not JSON: %s", aws.ToString(desc.Output))
	}
	json.Unmarshal([]byte(`{"summary": {"tag": "big:Rivera", "waited": 17}, "customer": "Rivera", "chunks": [[1, 2], [3]]}`), &want)
	gotRaw, _ := json.Marshal(got)
	wantRaw, _ := json.Marshal(want)
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("output = %s, want %s", gotRaw, wantRaw)
	}
}

// TestSDKJSONataRefusals: what AWS refuses at create time, this refuses the
// same way — a JSONPath field on a JSONata state, and an expression that does
// not compile — with the SDK's InvalidDefinition.
func TestSDKJSONataRefusals(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)

	for _, tc := range []struct{ name, def, want string }{
		{"mixed fields",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Parameters":{"a":1},"End":true}}}`,
			"Field 'Parameters' is not supported"},
		{"compile error",
			`{"QueryLanguage":"JSONata","StartAt":"P","States":{"P":{"Type":"Pass","Output":"{% $states.input[ %}","End":true}}}`,
			"JSONata expression"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
				Name:       aws.String("refused"),
				Definition: aws.String(tc.def),
				RoleArn:    aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
			})
			if err == nil {
				t.Fatal("accepted a definition AWS refuses")
			}
			if !strings.Contains(err.Error(), "InvalidDefinition") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want InvalidDefinition mentioning %q", err, tc.want)
			}
		})
	}
}
