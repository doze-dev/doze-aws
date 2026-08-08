package cloudformation

import (
	"strings"
	"testing"
)

// TestParseYAMLShortTags covers the forms that break hand-rolled parsers.
// Almost nobody writes Fn:: longhand in YAML, so getting these wrong means
// failing on essentially every real template.
func TestParseYAMLShortTags(t *testing.T) {
	tmpl := `
AWSTemplateFormatVersion: "2010-09-09"
Description: short tag coverage
Parameters:
  Env:
    Type: String
    Default: dev
Resources:
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "${Env}-orders"
      Tags:
        - Key: region
          Value: !Ref AWS::Region
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Join ["-", [!Ref Env, "uploads"]]
      Marker: !GetAtt Queue.Arn
      MarkerList: !GetAtt [Queue, Arn]
      Encoded: !Base64 "hello"
      Picked: !Select [1, !Split ["/", "a/b/c"]]
`
	tp, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if tp.Description != "short tag coverage" {
		t.Fatalf("Description = %q", tp.Description)
	}
	if len(tp.Resources) != 2 {
		t.Fatalf("got %d resources", len(tp.Resources))
	}

	q := tp.Resources["Queue"].Properties
	// !Sub normalises to the long form.
	sub, ok := q["QueueName"].(map[string]any)
	if !ok || sub["Fn::Sub"] != "${Env}-orders" {
		t.Fatalf("!Sub did not normalise: %#v", q["QueueName"])
	}
	// !Ref inside a nested sequence survives.
	tags := q["Tags"].([]any)
	tagRef := tags[0].(map[string]any)["Value"].(map[string]any)
	if tagRef["Ref"] != "AWS::Region" {
		t.Fatalf("nested !Ref = %#v", tagRef)
	}

	b := tp.Resources["Bucket"].Properties
	if _, ok := b["BucketName"].(map[string]any)["Fn::Join"]; !ok {
		t.Fatalf("!Join did not normalise: %#v", b["BucketName"])
	}
	// The dotted !GetAtt form must expand to a two-element list.
	att := b["Marker"].(map[string]any)["Fn::GetAtt"].([]any)
	if len(att) != 2 || att[0] != "Queue" || att[1] != "Arn" {
		t.Fatalf("dotted !GetAtt = %#v", att)
	}
	attList := b["MarkerList"].(map[string]any)["Fn::GetAtt"].([]any)
	if len(attList) != 2 || attList[1] != "Arn" {
		t.Fatalf("list !GetAtt = %#v", attList)
	}
	if b["Encoded"].(map[string]any)["Fn::Base64"] != "hello" {
		t.Fatalf("!Base64 = %#v", b["Encoded"])
	}
	// A short tag nested inside another short tag's argument.
	sel := b["Picked"].(map[string]any)["Fn::Select"].([]any)
	if _, ok := sel[1].(map[string]any)["Fn::Split"]; !ok {
		t.Fatalf("!Split nested in !Select = %#v", sel[1])
	}
}

func TestParseJSON(t *testing.T) {
	tmpl := `{
	  "AWSTemplateFormatVersion": "2010-09-09",
	  "Resources": {
	    "Table": {
	      "Type": "AWS::DynamoDB::Table",
	      "DependsOn": "Queue",
	      "Properties": {"TableName": {"Ref": "AWS::StackName"}}
	    },
	    "Queue": {"Type": "AWS::SQS::Queue"}
	  }
	}`
	tp, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tp.Resources) != 2 {
		t.Fatalf("got %d resources", len(tp.Resources))
	}
	// A scalar DependsOn becomes a one-element list.
	if deps := tp.Resources["Table"].DependsOn; len(deps) != 1 || deps[0] != "Queue" {
		t.Fatalf("DependsOn = %v", deps)
	}
	// A resource with no Properties still gets a usable empty map.
	if tp.Resources["Queue"].Properties == nil {
		t.Fatal("missing Properties should decode as an empty map, not nil")
	}
}

func TestParseRejectsBadTemplates(t *testing.T) {
	cases := []struct{ name, tmpl, want string }{
		{"empty", ``, "empty"},
		{"no resources", `{"Description": "x"}`, "no Resources"},
		{"resource without type", `{"Resources":{"A":{"Properties":{}}}}`, "no Type"},
		{"not a mapping", `- a
- b`, "mapping at the top level"},
		{"malformed json", `{"Resources": {`, "not valid JSON"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.tmpl))
		if err == nil {
			t.Errorf("%s: should have failed", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q should mention %q", c.name, err, c.want)
		}
	}
}

func TestParseTransformAndSAM(t *testing.T) {
	sam := `
Transform: AWS::Serverless-2016-10-31
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      Handler: bootstrap
`
	tp, err := Parse([]byte(sam))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !tp.IsSAM() {
		t.Fatal("SAM transform not detected")
	}

	plain, err := Parse([]byte(`{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain.IsSAM() {
		t.Fatal("a plain template is not SAM")
	}
}

func TestParseParametersConditionsOutputs(t *testing.T) {
	tmpl := `
Parameters:
  Stage:
    Type: String
    Default: dev
    AllowedValues: [dev, prod]
  Secret:
    Type: String
    NoEcho: true
Conditions:
  IsProd: !Equals [!Ref Stage, prod]
Mappings:
  Sizes:
    dev: {memory: 128}
Resources:
  Queue:
    Type: AWS::SQS::Queue
    Condition: IsProd
Outputs:
  QueueName:
    Description: the queue
    Value: !Ref Queue
    Export:
      Name: !Sub "${AWS::StackName}-queue"
`
	tp, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p := tp.Parameters["Stage"]; p.Default != "dev" || len(p.AllowedValues) != 2 {
		t.Fatalf("Stage parameter = %+v", p)
	}
	if !tp.Parameters["Secret"].NoEcho {
		t.Fatal("NoEcho not parsed")
	}
	if _, ok := tp.Conditions["IsProd"]; !ok {
		t.Fatal("condition not parsed")
	}
	if tp.Resources["Queue"].Condition != "IsProd" {
		t.Fatal("resource condition not parsed")
	}
	if _, ok := tp.Mappings["Sizes"]; !ok {
		t.Fatal("mappings not parsed")
	}
	out := tp.Outputs["QueueName"]
	if out.Description != "the queue" || out.Value == nil || out.ExportName == nil {
		t.Fatalf("output = %+v", out)
	}
}

func TestParsePreservesResourceOrder(t *testing.T) {
	tmpl := `
Resources:
  Zebra:
    Type: AWS::SQS::Queue
  Alpha:
    Type: AWS::SQS::Queue
  Middle:
    Type: AWS::SQS::Queue
`
	tp, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	// Declaration order, not alphabetical — reports must be stable and match
	// what the author wrote.
	want := []string{"Zebra", "Alpha", "Middle"}
	got := tp.Order()
	if len(got) != len(want) {
		t.Fatalf("Order() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order() = %v, want %v", got, want)
		}
	}
}

func TestParseScalarTypes(t *testing.T) {
	// Numbers and booleans must survive as such, so Fn::Equals and numeric
	// properties behave the same as in a JSON template.
	tp, err := Parse([]byte(`
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      VisibilityTimeout: 60
      FifoQueue: true
      Name: "60"
`))
	if err != nil {
		t.Fatal(err)
	}
	p := tp.Resources["Q"].Properties
	if v, ok := p["VisibilityTimeout"].(float64); !ok || v != 60 {
		t.Fatalf("int scalar = %#v", p["VisibilityTimeout"])
	}
	if v, ok := p["FifoQueue"].(bool); !ok || !v {
		t.Fatalf("bool scalar = %#v", p["FifoQueue"])
	}
	if v, ok := p["Name"].(string); !ok || v != "60" {
		t.Fatalf("quoted scalar should stay a string, got %#v", p["Name"])
	}
}
