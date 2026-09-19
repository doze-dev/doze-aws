package cloudformation

import (
	"fmt"
	"strings"
	"testing"
)

// A map-backed fetcher: TemplateURL → body.
func mapFetcher(m map[string]string) func(string) ([]byte, error) {
	return func(url string) ([]byte, error) {
		body, ok := m[url]
		if !ok {
			return nil, fmt.Errorf("no such template")
		}
		return []byte(body), nil
	}
}

const nestedParent = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Inbox:
    Type: AWS::SQS::Queue
  Queues:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: https://s3.us-east-1.amazonaws.com/staging/queues.yaml
      Parameters:
        InboxArn: !GetAtt Inbox.Arn
        Prefix: orders
  Fanout:
    Type: AWS::SNS::Topic
    Properties:
      Subscription:
        - Protocol: sqs
          Endpoint: !GetAtt Queues.Outputs.WorkArn
Outputs:
  Work:
    Value: !GetAtt Queues.Outputs.WorkArn
  ChildId:
    Value: !Ref Queues
`

const nestedChild = `
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  InboxArn:
    Type: String
  Prefix:
    Type: String
Resources:
  Work:
    Type: AWS::SQS::Queue
  Named:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub ${Prefix}-named
Outputs:
  WorkArn:
    Value: !GetAtt Work.Arn
  Upstream:
    Value: !Ref InboxArn
`

func TestNestedStackTranspiles(t *testing.T) {
	tmpl, err := Parse([]byte(nestedParent))
	if err != nil {
		t.Fatal(err)
	}
	fetch := mapFetcher(map[string]string{"https://s3.us-east-1.amazonaws.com/staging/queues.yaml": nestedChild})
	sf, rep, err := Transpile(tmpl, TranspileOptions{StackName: "app", FetchTemplate: fetch})
	if err != nil {
		t.Fatal(err)
	}
	// The child's derived name is prefixed; its explicit name is not.
	for _, want := range []string{"Inbox", "Queues-Work", "orders-named"} {
		if _, ok := sf.Queues[want]; !ok {
			t.Errorf("queue %q missing from the merged stack: %v", want, sortedNames(sf.Queues))
		}
	}
	// Outputs flow both ways: the parent read the child's, the child read the
	// parent's attribute through its parameter.
	if rep.Outputs["Work"] != "arn:aws:sqs:us-east-1:000000000000:Queues-Work" {
		t.Errorf("parent output from child: %q", rep.Outputs["Work"])
	}
	if !strings.Contains(rep.Outputs["ChildId"], "stack/app-Queues/") {
		t.Errorf("Ref on the child should be its stack ARN: %q", rep.Outputs["ChildId"])
	}
	if len(rep.Nested) != 1 || rep.Nested[0].Name != "app-Queues" || rep.Nested[0].Report.Outputs["Upstream"] != "arn:aws:sqs:us-east-1:000000000000:Inbox" {
		t.Errorf("nested report: %+v", rep.Nested)
	}
	if subs := sf.Topics["Fanout"].Subscriptions; len(subs) != 1 || subs[0].Queue != "Queues-Work" {
		t.Errorf("the parent topic should subscribe the child's queue: %+v", subs)
	}
	// The report names the child as a mapped resource.
	found := false
	for _, e := range rep.Entries {
		if e.LogicalID == "Queues" && e.Kind == Mapped && e.Name == "app-Queues" {
			found = true
		}
	}
	if !found {
		t.Errorf("the Stack resource should be a mapped entry: %+v", rep.Entries)
	}
}

func TestNestedStackRefusals(t *testing.T) {
	parse := func(body string) *Template {
		tmpl, err := Parse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return tmpl
	}
	// No fetcher.
	if _, _, err := Transpile(parse(nestedParent), TranspileOptions{StackName: "app"}); err == nil || !strings.Contains(err.Error(), "template fetcher") {
		t.Errorf("no fetcher: %v", err)
	}
	// A collision: the child declares a queue the parent also has.
	collide := strings.Replace(nestedChild, "QueueName: !Sub ${Prefix}-named", "QueueName: Inbox", 1)
	fetch := mapFetcher(map[string]string{"https://s3.us-east-1.amazonaws.com/staging/queues.yaml": collide})
	if _, _, err := Transpile(parse(nestedParent), TranspileOptions{StackName: "app", FetchTemplate: fetch}); err == nil || !strings.Contains(err.Error(), `queue "Inbox" is also declared`) {
		t.Errorf("collision: %v", err)
	}
	// A cycle: the child nests the parent's URL.
	self := `
Resources:
  Again:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: https://s3.us-east-1.amazonaws.com/staging/self.yaml
`
	fetch = mapFetcher(map[string]string{"https://s3.us-east-1.amazonaws.com/staging/self.yaml": self})
	if _, _, err := Transpile(parse(self), TranspileOptions{StackName: "loop", FetchTemplate: fetch}); err == nil || !strings.Contains(err.Error(), "nests itself") {
		t.Errorf("cycle: %v", err)
	}
	// Too deep: a chain of distinct URLs past the limit.
	chain := map[string]string{}
	for i := 0; i <= maxNestingDepth+1; i++ {
		chain[fmt.Sprintf("https://s3.us-east-1.amazonaws.com/staging/l%d.yaml", i)] = fmt.Sprintf(`
Resources:
  Next:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: https://s3.us-east-1.amazonaws.com/staging/l%d.yaml
`, i+1)
	}
	if _, _, err := Transpile(parse(chain["https://s3.us-east-1.amazonaws.com/staging/l0.yaml"]), TranspileOptions{StackName: "deep", FetchTemplate: mapFetcher(chain)}); err == nil || !strings.Contains(err.Error(), "nest more than") {
		t.Errorf("depth: %v", err)
	}
	// A CDK logical id loses its bookkeeping suffix.
	if got := nestedShortName("OrdersNestedStackOrdersNestedStackResource1A2B3C"); got != "Orders" {
		t.Errorf("CDK short name: %q", got)
	}
}

// A container image function is refused while the template is read, not after
// half a stack exists.
//
// The ImageUri used to fall through to firstNonEmpty(key, ImageUri) and become
// the function's local CODE PATH, so `cdk deploy` of an image function failed
// with "code path ... does not exist" naming an ECR URL — a message about the
// symptom, from a place that had already started provisioning.
func TestAContainerImageFunctionIsRefusedWhileReadingTheTemplate(t *testing.T) {
	const tmpl = `
Resources:
  Scorer:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: scorer
      PackageType: Image
      Role: arn:aws:iam::000000000000:role/r
      Code:
        ImageUri: 123.dkr.ecr.us-east-1.amazonaws.com/app:latest
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Transpile(parsed, TranspileOptions{StackName: "app"})
	if err == nil {
		t.Fatal("an image-based function transpiled; doze-aws cannot run one")
	}
	msg := err.Error()
	for _, want := range []string{"container image", "local processes", "app:latest", "scorer"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n  %s", want, msg)
		}
	}
	if strings.Contains(msg, "does not exist") {
		t.Errorf("the image URI is still being treated as a code path:\n  %s", msg)
	}
}
