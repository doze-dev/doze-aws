package cfn

import (
	"strings"
	"testing"
)

func TestCheckReferences(t *testing.T) {
	ok := []string{
		`{"Parameters":{"P":{"Type":"String"}},"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":{"Ref":"P"}}},
		  "T":{"Type":"AWS::SNS::Topic","DependsOn":"Q","Properties":{"DisplayName":{"Fn::GetAtt":["Q","Arn"]},"TopicName":{"Ref":"AWS::StackName"}}}},
		  "Outputs":{"A":{"Value":{"Fn::GetAtt":"Q.Arn"}},"B":{"Value":{"Ref":"T"}}}}`,
		// A SAM template may name resources only the transform creates.
		`{"Transform":"AWS::Serverless-2016-10-31","Resources":{"F":{"Type":"AWS::Serverless::Function","Properties":{"Handler":"a.b","Runtime":"python3.12","InlineCode":"x","Role":{"Fn::GetAtt":["Implicit","Arn"]}}}}}`,
	}
	for _, body := range ok {
		tmpl, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := tmpl.CheckReferences(); err != nil {
			t.Errorf("refused a template whose references resolve: %v\n%s", err, body)
		}
	}
	bad := map[string]string{
		`{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":{"Ref":"Ghost"}}}}}`:                                         "Unresolved resource dependencies [Ghost] in the Resources block",
		`{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}},"Outputs":{"O":{"Value":{"Ref":"Ghost"}}}}`:                                          "Unresolved resource dependencies [Ghost] in the Outputs block",
		`{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}},"Outputs":{"O":{"Value":{"Fn::GetAtt":["Ghost","Arn"]}}}}`:                           "Fn::GetAtt references undefined resource Ghost",
		`{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"Tags":[{"Key":"k","Value":{"Fn::GetAtt":"Ghost.Arn"}}]}}}}`:             "Fn::GetAtt references undefined resource Ghost",
		`{"Resources":{"Q":{"Type":"AWS::SQS::Queue","DependsOn":"Ghost"}}}`:                                                                "Unresolved resource dependencies [Ghost]",
		`{"Parameters":{"P":{"Type":"String"}},"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"X":{"Fn::GetAtt":["P","Arn"]}}}}}`: "Fn::GetAtt references undefined resource P",
	}
	for body, want := range bad {
		tmpl, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("parse: %v\n%s", err, body)
		}
		if err := tmpl.CheckReferences(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q\n%s", err, want, body)
		}
	}
}
