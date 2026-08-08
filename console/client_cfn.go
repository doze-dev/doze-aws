package console

// CloudFormation console client (Query protocol, XML responses).
//
// A stack is the one resource here that is not a thing but a record of how
// other things were made. So the page is built around that: what the stack
// produced, in what order it happened, and what the template asked for — the
// three questions someone has when a deploy did not do what they expected.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Stack is one CloudFormation stack.
type Stack struct {
	Name         string
	ID           string
	Status       string
	StatusReason string
	Description  string
	Created      string
	Updated      string
	Params       []KeyVal
	Outputs      []StackOutput
	Tags         map[string]string
	Capabilities []string
	NotifyARNs   []string
	// Rollback and TerminationProtection are declared on the deploy; nothing
	// local acts on either.
	DisableRollback       bool
	TerminationProtection bool
	Resources             int
}

// Failed reports whether the stack ended in a state worth drawing attention to.
func (s Stack) Failed() bool {
	return strings.Contains(s.Status, "FAILED") || strings.Contains(s.Status, "ROLLBACK")
}

// KeyVal is a parameter or tag pair.
type KeyVal struct{ Key, Value string }

// StackOutput is one stack output, with the export name when it declares one.
type StackOutput struct {
	Key, Value, Description, Export string
}

// StackResource is one resource the stack manages.
type StackResource struct {
	LogicalID  string
	PhysicalID string
	Type       string
	Status     string
	Reason     string
	Updated    string
	// Svc and Href point at the console page for the resource itself, so a
	// stack is a way into what it built rather than a dead inventory.
	Svc  string
	Href string
}

// StackEvent is one line of the deploy's history.
type StackEvent struct {
	Time      string
	LogicalID string
	Type      string
	Status    string
	Reason    string
	Failed    bool
}

func (b *backend) cfn(ctx context.Context, action string, extra url.Values) ([]byte, error) {
	v := url.Values{"Action": {action}, "Version": {"2010-05-15"}}
	for k, vals := range extra {
		v[k] = vals
	}
	return b.queryXML(ctx, v)
}

// stackWire is the XML shape a stack arrives in.
type stackWire struct {
	StackID           string `xml:"StackId"`
	StackName         string `xml:"StackName"`
	Description       string `xml:"Description"`
	CreationTime      string `xml:"CreationTime"`
	LastUpdatedTime   string `xml:"LastUpdatedTime"`
	StackStatus       string `xml:"StackStatus"`
	StackStatusReason string `xml:"StackStatusReason"`
	DisableRollback   bool   `xml:"DisableRollback"`
	TerminationProt   bool   `xml:"EnableTerminationProtection"`
	Parameters        []struct {
		Key   string `xml:"ParameterKey"`
		Value string `xml:"ParameterValue"`
	} `xml:"Parameters>member"`
	Outputs []struct {
		Key         string `xml:"OutputKey"`
		Value       string `xml:"OutputValue"`
		Description string `xml:"Description"`
		Export      string `xml:"ExportName"`
	} `xml:"Outputs>member"`
	Tags []struct {
		Key   string `xml:"Key"`
		Value string `xml:"Value"`
	} `xml:"Tags>member"`
	Capabilities []string `xml:"Capabilities>member"`
	NotifyARNs   []string `xml:"NotificationARNs>member"`
}

func (w stackWire) toStack() Stack {
	s := Stack{
		Name: w.StackName, ID: w.StackID, Status: w.StackStatus,
		StatusReason: w.StackStatusReason, Description: w.Description,
		Created: shortTime(w.CreationTime), Updated: shortTime(w.LastUpdatedTime),
		Capabilities: w.Capabilities, NotifyARNs: w.NotifyARNs,
		DisableRollback: w.DisableRollback, TerminationProtection: w.TerminationProt,
	}
	for _, p := range w.Parameters {
		s.Params = append(s.Params, KeyVal{p.Key, p.Value})
	}
	for _, o := range w.Outputs {
		s.Outputs = append(s.Outputs, StackOutput{o.Key, o.Value, o.Description, o.Export})
	}
	if len(w.Tags) > 0 {
		s.Tags = map[string]string{}
		for _, t := range w.Tags {
			s.Tags[t.Key] = t.Value
		}
	}
	return s
}

func (b *backend) ListStacks(ctx context.Context) ([]Stack, error) {
	body, err := b.cfn(ctx, "DescribeStacks", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Stacks []stackWire `xml:"DescribeStacksResult>Stacks>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	stacks := make([]Stack, 0, len(out.Stacks))
	for _, w := range out.Stacks {
		stacks = append(stacks, w.toStack())
	}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Name < stacks[j].Name })
	return stacks, nil
}

// CountStacks is the cheap probe for the nav badge.
func (b *backend) CountStacks(ctx context.Context) (int, error) {
	stacks, err := b.ListStacks(ctx)
	return len(stacks), err
}

func (b *backend) StackDetail(ctx context.Context, name string) (*Stack, error) {
	body, err := b.cfn(ctx, "DescribeStacks", url.Values{"StackName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Stacks []stackWire `xml:"DescribeStacksResult>Stacks>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Stacks) == 0 {
		return nil, fmt.Errorf("stack %s does not exist", name)
	}
	s := out.Stacks[0].toStack()
	return &s, nil
}

func (b *backend) StackResources(ctx context.Context, name string) ([]StackResource, error) {
	body, err := b.cfn(ctx, "DescribeStackResources", url.Values{"StackName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Resources []struct {
			LogicalID  string `xml:"LogicalResourceId"`
			PhysicalID string `xml:"PhysicalResourceId"`
			Type       string `xml:"ResourceType"`
			Status     string `xml:"ResourceStatus"`
			Reason     string `xml:"ResourceStatusReason"`
			Timestamp  string `xml:"Timestamp"`
		} `xml:"DescribeStackResourcesResult>StackResources>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]StackResource, 0, len(out.Resources))
	for _, r := range out.Resources {
		sr := StackResource{
			LogicalID: r.LogicalID, PhysicalID: r.PhysicalID, Type: r.Type,
			Status: r.Status, Reason: r.Reason, Updated: shortTime(r.Timestamp),
		}
		sr.Svc, sr.Href = resourceLink(r.Type, r.PhysicalID)
		res = append(res, sr)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].LogicalID < res[j].LogicalID })
	return res, nil
}

// resourceLink maps a CloudFormation resource type to the console page for the
// thing it created. A stack is most useful as a way into its resources, so a
// row that can be followed is worth more than one that only names an id.
func resourceLink(cfnType, physicalID string) (svc, href string) {
	if physicalID == "" {
		return "", ""
	}
	switch cfnType {
	case "AWS::S3::Bucket":
		return "s3", "/s3/" + physicalID
	case "AWS::DynamoDB::Table", "AWS::DynamoDB::GlobalTable":
		return "ddb", "/ddb/" + physicalID
	case "AWS::SQS::Queue":
		return "sqs", "/sqs/" + lastSegment(physicalID)
	case "AWS::SNS::Topic":
		return "sns", "/sns/" + lastSegment(physicalID)
	case "AWS::Kinesis::Stream":
		return "kinesis", "/kinesis/" + physicalID
	case "AWS::Lambda::Function", "AWS::Serverless::Function":
		return "lambda", "/lambda/" + physicalID
	case "AWS::KMS::Key":
		return "kms", "/kms/" + physicalID
	case "AWS::SecretsManager::Secret":
		return "sm", "/sm/" + lastSegment(physicalID)
	case "AWS::SSM::Parameter":
		return "ssm", "/ssm/" + strings.TrimPrefix(physicalID, "/")
	case "AWS::Events::EventBus":
		return "eb", "/eb/" + lastSegment(physicalID)
	}
	return "", ""
}

// lastSegment takes the resource name out of an ARN or queue URL.
func lastSegment(s string) string {
	if i := strings.LastIndexAny(s, ":/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (b *backend) StackEvents(ctx context.Context, name string) ([]StackEvent, error) {
	body, err := b.cfn(ctx, "DescribeStackEvents", url.Values{"StackName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []struct {
			LogicalID string `xml:"LogicalResourceId"`
			Type      string `xml:"ResourceType"`
			Status    string `xml:"ResourceStatus"`
			Reason    string `xml:"ResourceStatusReason"`
			Timestamp string `xml:"Timestamp"`
		} `xml:"DescribeStackEventsResult>StackEvents>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	evs := make([]StackEvent, 0, len(out.Events))
	for _, e := range out.Events {
		evs = append(evs, StackEvent{
			Time: shortTime(e.Timestamp), LogicalID: e.LogicalID, Type: e.Type,
			Status: e.Status, Reason: e.Reason,
			Failed: strings.Contains(e.Status, "FAILED"),
		})
	}
	return evs, nil
}

func (b *backend) StackTemplate(ctx context.Context, name string) (string, error) {
	body, err := b.cfn(ctx, "GetTemplate", url.Values{"StackName": {name}})
	if err != nil {
		return "", err
	}
	var out struct {
		Body string `xml:"GetTemplateResult>TemplateBody"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Body, nil
}

func (b *backend) DeleteStack(ctx context.Context, name string) error {
	_, err := b.cfn(ctx, "DeleteStack", url.Values{"StackName": {name}})
	return err
}
