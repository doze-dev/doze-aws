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
	Parent       string // the parent stack's name when this is a nested stack
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
	ParentID     string   `xml:"ParentId"`
}

func (w stackWire) toStack() Stack {
	s := Stack{
		Name: w.StackName, ID: w.StackID, Status: w.StackStatus,
		StatusReason: w.StackStatusReason, Description: w.Description,
		Created: shortTime(w.CreationTime), Updated: shortTime(w.LastUpdatedTime),
		Capabilities: w.Capabilities, NotifyARNs: w.NotifyARNs,
		DisableRollback: w.DisableRollback, TerminationProtection: w.TerminationProt,
		Parent: stackNameOfARN(w.ParentID),
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
// resourceLink turns a CloudFormation resource into a console link. The
// CFN-type knowledge stays here — it is CloudFormation's vocabulary, not the
// console's — but the path comes from the shared resolver, so a stack's
// resource list gains every service the resolver knows and cannot drift from
// the links the rest of the console renders.
func resourceLink(cfnType, physicalID string) (svc, href string) {
	if physicalID == "" {
		return "", ""
	}
	key, ok := cfnTypeService[cfnType]
	if !ok {
		return "", ""
	}
	ref := resourceURL(key, physicalID)
	return ref.Svc, ref.Path
}

var cfnTypeService = map[string]string{
	"AWS::S3::Bucket":             "s3",
	"AWS::DynamoDB::Table":        "ddb",
	"AWS::DynamoDB::GlobalTable":  "ddb",
	"AWS::SQS::Queue":             "sqs",
	"AWS::SNS::Topic":             "sns",
	"AWS::Kinesis::Stream":        "kinesis",
	"AWS::Lambda::Function":       "lambda",
	"AWS::Serverless::Function":   "lambda",
	"AWS::KMS::Key":               "kms",
	"AWS::SecretsManager::Secret": "sm",
	"AWS::SSM::Parameter":         "ssm",
	"AWS::Events::EventBus":       "eb",
	"AWS::Events::Rule":           "eb",
	"AWS::ApiGateway::RestApi":    "apigw",
	"AWS::Serverless::Api":        "apigw",
	"AWS::IAM::Role":              "iam",
	"AWS::IAM::User":              "iam",
	"AWS::CloudFormation::Stack":  "cfn",
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

// ---- deploys, change sets, exports: the control plane ----

// TemplateParam is one parameter a template declares, as GetTemplateSummary
// or ValidateTemplate reports it.
type TemplateParam struct {
	Key, Type, Default, Description string
	NoEcho                          bool
}

// TemplateSummaryInfo is what GetTemplateSummary extracts from a template
// body before anything is deployed: the questions the deploy will ask.
type TemplateSummaryInfo struct {
	Description   string
	Params        []TemplateParam
	ResourceTypes []string
}

type templateParamWire struct {
	ParameterKey  string `xml:"ParameterKey"`
	ParameterType string `xml:"ParameterType"`
	DefaultValue  string `xml:"DefaultValue"`
	Description   string `xml:"Description"`
	NoEcho        bool   `xml:"NoEcho"`
}

func (w templateParamWire) toParam() TemplateParam {
	return TemplateParam{Key: w.ParameterKey, Type: w.ParameterType,
		Default: w.DefaultValue, Description: w.Description, NoEcho: w.NoEcho}
}

// ValidateStackTemplate runs the template through the emulator's real parser
// without deploying anything. The error IS the product here: it is the same
// rejection a deploy would hit, seen before the deploy.
func (b *backend) ValidateStackTemplate(ctx context.Context, body string) (string, []TemplateParam, error) {
	res, err := b.cfn(ctx, "ValidateTemplate", url.Values{"TemplateBody": {body}})
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Description string              `xml:"ValidateTemplateResult>Description"`
		Params      []templateParamWire `xml:"ValidateTemplateResult>Parameters>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return "", nil, err
	}
	params := make([]TemplateParam, 0, len(out.Params))
	for _, p := range out.Params {
		params = append(params, p.toParam())
	}
	return out.Description, params, nil
}

// TemplateSummary asks a template body (or, with body empty, a deployed
// stack's stored template) what it declares — the parameter list drives the
// create and update forms.
func (b *backend) TemplateSummary(ctx context.Context, body, stack string) (*TemplateSummaryInfo, error) {
	v := url.Values{}
	if body != "" {
		v.Set("TemplateBody", body)
	} else {
		v.Set("StackName", stack)
	}
	res, err := b.cfn(ctx, "GetTemplateSummary", v)
	if err != nil {
		return nil, err
	}
	var out struct {
		Description   string              `xml:"GetTemplateSummaryResult>Description"`
		Params        []templateParamWire `xml:"GetTemplateSummaryResult>Parameters>member"`
		ResourceTypes []string            `xml:"GetTemplateSummaryResult>ResourceTypes>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	info := &TemplateSummaryInfo{Description: out.Description, ResourceTypes: out.ResourceTypes}
	for _, p := range out.Params {
		info.Params = append(info.Params, p.toParam())
	}
	return info, nil
}

// paramValues encodes a parameter map in the Query protocol's member shape.
func paramValues(v url.Values, params map[string]string) {
	i := 0
	for _, k := range sortedKeysOf(params) {
		i++
		v.Set(fmt.Sprintf("Parameters.member.%d.ParameterKey", i), k)
		v.Set(fmt.Sprintf("Parameters.member.%d.ParameterValue", i), params[k])
	}
}

func sortedKeysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CreateStack deploys a template. The emulator provisions synchronously, so
// the stack comes back terminal — no polling loop.
func (b *backend) CreateStack(ctx context.Context, name, body string, params map[string]string) error {
	v := url.Values{"StackName": {name}, "TemplateBody": {body}}
	paramValues(v, params)
	_, err := b.cfn(ctx, "CreateStack", v)
	return err
}

// UpdateStack redeploys. An empty body means UsePreviousTemplate — change
// only the parameters, keep the template that is already there.
func (b *backend) UpdateStack(ctx context.Context, name, body string, params map[string]string) error {
	v := url.Values{"StackName": {name}}
	if body == "" {
		v.Set("UsePreviousTemplate", "true")
	} else {
		v.Set("TemplateBody", body)
	}
	paramValues(v, params)
	_, err := b.cfn(ctx, "UpdateStack", v)
	return err
}

// ChangeSet is one change set, summary or detail (the change list and
// parameters only arrive on a describe).
type ChangeSet struct {
	Name, ID, Status, StatusReason, ExecutionStatus, Created string
	Changes                                                  []StackChange
	Params                                                   []KeyVal
}

// StackChange is one resource-level line of a change set's diff.
type StackChange struct {
	Action, LogicalID, Type, PhysicalID, Replacement string
}

// Executable reports whether this set can still be executed.
func (c ChangeSet) Executable() bool { return c.ExecutionStatus == "AVAILABLE" }

// CreateChangeSet computes the diff without applying it. The change-set type
// (CREATE vs UPDATE) is inferred by the emulator from whether the stack
// exists, exactly as the deploy tools rely on.
func (b *backend) CreateChangeSet(ctx context.Context, stack, name, body string, params map[string]string) error {
	v := url.Values{"StackName": {stack}, "ChangeSetName": {name}}
	if body != "" {
		v.Set("TemplateBody", body)
	}
	paramValues(v, params)
	_, err := b.cfn(ctx, "CreateChangeSet", v)
	return err
}

// ListChangeSets lists a stack's change sets, newest first.
func (b *backend) ListChangeSets(ctx context.Context, stack string) ([]ChangeSet, error) {
	res, err := b.cfn(ctx, "ListChangeSets", url.Values{"StackName": {stack}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Sets []struct {
			ChangeSetName   string `xml:"ChangeSetName"`
			ChangeSetId     string `xml:"ChangeSetId"`
			Status          string `xml:"Status"`
			StatusReason    string `xml:"StatusReason"`
			ExecutionStatus string `xml:"ExecutionStatus"`
			CreationTime    string `xml:"CreationTime"`
		} `xml:"ListChangeSetsResult>Summaries>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	sets := make([]ChangeSet, 0, len(out.Sets))
	for _, s := range out.Sets {
		sets = append(sets, ChangeSet{
			Name: s.ChangeSetName, ID: s.ChangeSetId, Status: s.Status,
			StatusReason: s.StatusReason, ExecutionStatus: s.ExecutionStatus,
			Created: shortTime(s.CreationTime),
		})
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Created > sets[j].Created })
	return sets, nil
}

// ChangeSetDetail describes one change set: its status pair and the
// resource-level Add/Modify/Remove list.
func (b *backend) ChangeSetDetail(ctx context.Context, stack, name string) (*ChangeSet, error) {
	res, err := b.cfn(ctx, "DescribeChangeSet",
		url.Values{"StackName": {stack}, "ChangeSetName": {name}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Name            string `xml:"DescribeChangeSetResult>ChangeSetName"`
		ID              string `xml:"DescribeChangeSetResult>ChangeSetId"`
		Status          string `xml:"DescribeChangeSetResult>Status"`
		StatusReason    string `xml:"DescribeChangeSetResult>StatusReason"`
		ExecutionStatus string `xml:"DescribeChangeSetResult>ExecutionStatus"`
		CreationTime    string `xml:"DescribeChangeSetResult>CreationTime"`
		Params          []struct {
			Key   string `xml:"ParameterKey"`
			Value string `xml:"ParameterValue"`
		} `xml:"DescribeChangeSetResult>Parameters>member"`
		Changes []struct {
			RC struct {
				Action      string `xml:"Action"`
				LogicalID   string `xml:"LogicalResourceId"`
				PhysicalID  string `xml:"PhysicalResourceId"`
				Type        string `xml:"ResourceType"`
				Replacement string `xml:"Replacement"`
			} `xml:"ResourceChange"`
		} `xml:"DescribeChangeSetResult>Changes>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	cs := &ChangeSet{
		Name: out.Name, ID: out.ID, Status: out.Status,
		StatusReason: out.StatusReason, ExecutionStatus: out.ExecutionStatus,
		Created: shortTime(out.CreationTime),
	}
	for _, p := range out.Params {
		cs.Params = append(cs.Params, KeyVal{p.Key, p.Value})
	}
	for _, ch := range out.Changes {
		cs.Changes = append(cs.Changes, StackChange{
			Action: ch.RC.Action, LogicalID: ch.RC.LogicalID,
			Type: ch.RC.Type, PhysicalID: ch.RC.PhysicalID, Replacement: ch.RC.Replacement,
		})
	}
	return cs, nil
}

// ExecuteChangeSet applies the reviewed diff.
func (b *backend) ExecuteChangeSet(ctx context.Context, stack, name string) error {
	_, err := b.cfn(ctx, "ExecuteChangeSet",
		url.Values{"StackName": {stack}, "ChangeSetName": {name}})
	return err
}

// DeleteChangeSet discards a reviewed-and-rejected diff.
func (b *backend) DeleteChangeSet(ctx context.Context, stack, name string) error {
	_, err := b.cfn(ctx, "DeleteChangeSet",
		url.Values{"StackName": {stack}, "ChangeSetName": {name}})
	return err
}

// StackExport is one row of the cross-stack export registry.
type StackExport struct {
	Name, Value, Stack string
}

// ListExports reads the export registry Fn::ImportValue resolves against.
func (b *backend) ListExports(ctx context.Context) ([]StackExport, error) {
	res, err := b.cfn(ctx, "ListExports", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Exports []struct {
			Name             string `xml:"Name"`
			Value            string `xml:"Value"`
			ExportingStackId string `xml:"ExportingStackId"`
		} `xml:"ListExportsResult>Exports>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	exports := make([]StackExport, 0, len(out.Exports))
	for _, e := range out.Exports {
		// The ARN is arn:…:stack/<name>/<id>; the name is the useful part.
		stack := e.ExportingStackId
		if i := strings.Index(stack, ":stack/"); i >= 0 {
			stack = strings.SplitN(stack[i+len(":stack/"):], "/", 2)[0]
		}
		exports = append(exports, StackExport{Name: e.Name, Value: e.Value, Stack: stack})
	}
	return exports, nil
}

// ListImports names the stacks whose templates import an export — the
// blast-radius question before changing or removing it.
func (b *backend) ListImports(ctx context.Context, export string) ([]string, error) {
	res, err := b.cfn(ctx, "ListImports", url.Values{"ExportName": {export}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Imports []string `xml:"ListImportsResult>Imports>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out.Imports, nil
}

// DeletedStacks is the record DescribeStacks no longer shows: stacks in
// DELETE_COMPLETE, reachable through ListStacks' status filter.
func (b *backend) DeletedStacks(ctx context.Context) ([]Stack, error) {
	res, err := b.cfn(ctx, "ListStacks",
		url.Values{"StackStatusFilter.member.1": {"DELETE_COMPLETE"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Stacks []struct {
			StackName    string `xml:"StackName"`
			StackId      string `xml:"StackId"`
			StackStatus  string `xml:"StackStatus"`
			CreationTime string `xml:"CreationTime"`
		} `xml:"ListStacksResult>StackSummaries>member"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	stacks := make([]Stack, 0, len(out.Stacks))
	for _, s := range out.Stacks {
		stacks = append(stacks, Stack{Name: s.StackName, ID: s.StackId,
			Status: s.StackStatus, Created: shortTime(s.CreationTime)})
	}
	return stacks, nil
}

// StackResourceInfo is the single-resource drill-down: what the resources
// table shows, plus the status reason and timestamp it does not.
type StackResourceInfo struct {
	LogicalID, PhysicalID, Type, Status, Reason, Updated string
}

// StackResource1 describes one resource by logical id (DescribeStackResource,
// the singular describe).
func (b *backend) StackResource1(ctx context.Context, stack, logicalID string) (*StackResourceInfo, error) {
	res, err := b.cfn(ctx, "DescribeStackResource",
		url.Values{"StackName": {stack}, "LogicalResourceId": {logicalID}})
	if err != nil {
		return nil, err
	}
	var out struct {
		D struct {
			LogicalResourceId    string `xml:"LogicalResourceId"`
			PhysicalResourceId   string `xml:"PhysicalResourceId"`
			ResourceType         string `xml:"ResourceType"`
			ResourceStatus       string `xml:"ResourceStatus"`
			ResourceStatusReason string `xml:"ResourceStatusReason"`
			Timestamp            string `xml:"Timestamp"`
		} `xml:"DescribeStackResourceResult>StackResourceDetail"`
	}
	if err := xml.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &StackResourceInfo{
		LogicalID: out.D.LogicalResourceId, PhysicalID: out.D.PhysicalResourceId,
		Type: out.D.ResourceType, Status: out.D.ResourceStatus,
		Reason: out.D.ResourceStatusReason, Updated: shortTime(out.D.Timestamp),
	}, nil
}

// stackNameOfARN reads the name out of arn:...:stack/<name>/<id>.
func stackNameOfARN(arn string) string {
	_, rest, ok := strings.Cut(arn, ":stack/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}
