package cloudformation

// Stack lifecycle: create, update, delete, describe — and the deploy flow that
// makes `sam deploy` and `cdk deploy` work.

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/provision"
)

var handlers = map[string]handler{
	// Stack lifecycle.
	"CreateStack":    hCreateStack,
	"UpdateStack":    hUpdateStack,
	"DeleteStack":    hDeleteStack,
	"DescribeStacks": hDescribeStacks,
	"ListStacks":     hListStacks,

	// Progress polling — what deploy tools block on.
	"DescribeStackEvents":    hDescribeStackEvents,
	"DescribeStackResource":  hDescribeStackResource,
	"DescribeStackResources": hDescribeStackResources,
	"ListStackResources":     hListStackResources,

	// Templates.
	"GetTemplate":        hGetTemplate,
	"GetTemplateSummary": hGetTemplateSummary,
	"ValidateTemplate":   hValidateTemplate,

	// Change sets (changesets.go).
	"CreateChangeSet":   hCreateChangeSet,
	"DescribeChangeSet": hDescribeChangeSet,
	"ExecuteChangeSet":  hExecuteChangeSet,
	"DeleteChangeSet":   hDeleteChangeSet,
	"ListChangeSets":    hListChangeSets,

	// Cross-stack exports.
	"ListExports": hListExports,
	"ListImports": hListImports,

	// Configuration round-trips.
	"SetStackPolicy":              hSetStackPolicy,
	"GetStackPolicy":              hGetStackPolicy,
	"UpdateTerminationProtection": hUpdateTerminationProtection,
	"CancelUpdateStack":           hCancelUpdateStack,
}

// ---- wire views ----

type outputView struct {
	OutputKey   string `xml:"OutputKey"`
	OutputValue string `xml:"OutputValue"`
	Description string `xml:"Description,omitempty"`
	ExportName  string `xml:"ExportName,omitempty"`
}

type parameterView struct {
	ParameterKey   string `xml:"ParameterKey"`
	ParameterValue string `xml:"ParameterValue"`
}

type tagView struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type stackView struct {
	StackId                     string          `xml:"StackId"`
	StackName                   string          `xml:"StackName"`
	Description                 string          `xml:"Description,omitempty"`
	CreationTime                string          `xml:"CreationTime"`
	LastUpdatedTime             string          `xml:"LastUpdatedTime,omitempty"`
	StackStatus                 string          `xml:"StackStatus"`
	StackStatusReason           string          `xml:"StackStatusReason,omitempty"`
	DisableRollback             bool            `xml:"DisableRollback"`
	EnableTerminationProtection bool            `xml:"EnableTerminationProtection"`
	Parameters                  []parameterView `xml:"Parameters>member,omitempty"`
	Outputs                     []outputView    `xml:"Outputs>member,omitempty"`
	Tags                        []tagView       `xml:"Tags>member,omitempty"`
	// DriftInformation is present in real responses and some SDK models
	// require the node, so it is emitted with the only honest value.
	DriftInformation driftInfo `xml:"DriftInformation"`
}

type driftInfo struct {
	StackDriftStatus string `xml:"StackDriftStatus"`
}

type resourceView struct {
	StackId              string `xml:"StackId"`
	StackName            string `xml:"StackName"`
	LogicalResourceId    string `xml:"LogicalResourceId"`
	PhysicalResourceId   string `xml:"PhysicalResourceId,omitempty"`
	ResourceType         string `xml:"ResourceType"`
	Timestamp            string `xml:"Timestamp"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
}

type eventView struct {
	StackId              string `xml:"StackId"`
	StackName            string `xml:"StackName"`
	EventId              string `xml:"EventId"`
	LogicalResourceId    string `xml:"LogicalResourceId"`
	PhysicalResourceId   string `xml:"PhysicalResourceId,omitempty"`
	ResourceType         string `xml:"ResourceType"`
	Timestamp            string `xml:"Timestamp"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
}

func viewStack(st *StackRecord) stackView {
	v := stackView{
		StackId:                     st.ID,
		StackName:                   st.Name,
		CreationTime:                awshttp.ISO8601(unix(st.Created)),
		StackStatus:                 st.Status,
		StackStatusReason:           st.StatusReason,
		EnableTerminationProtection: st.TerminationProtection,
		DriftInformation:            driftInfo{StackDriftStatus: "NOT_CHECKED"},
	}
	if st.Updated != 0 && st.Updated != st.Created {
		v.LastUpdatedTime = awshttp.ISO8601(unix(st.Updated))
	}
	for _, k := range sortedKeys(st.Parameters) {
		v.Parameters = append(v.Parameters, parameterView{k, st.Parameters[k]})
	}
	for _, o := range st.Outputs {
		v.Outputs = append(v.Outputs, outputView{
			OutputKey: o.Key, OutputValue: o.Value,
			Description: o.Description, ExportName: o.ExportName,
		})
	}
	for _, k := range sortedKeys(st.Tags) {
		v.Tags = append(v.Tags, tagView{k, st.Tags[k]})
	}
	return v
}

// ---- create / update ----

func hCreateStack(s *Server, p params) (any, *awshttp.APIError) {
	name := p.str("StackName")
	if name == "" {
		return nil, errValidation("StackName is required")
	}
	// A stack sitting in REVIEW_IN_PROGRESS was materialised by a change set
	// and has no resources yet, so CreateStack may still claim it.
	if existing, err := s.store.GetStack(name); err == nil &&
		existing.Status != StatusReviewInProgress && existing.Status != StatusDeleteComplete {
		return nil, awshttp.Errf(400, "AlreadyExistsException",
			"Stack [%s] already exists", existing.Name)
	}
	body, aerr := s.templateOf(p)
	if aerr != nil {
		return nil, aerr
	}
	st, aerr := s.deploy(name, body, p.keyValues("Parameters", "ParameterKey", "ParameterValue"),
		p.keyValues("Tags", "Key", "Value"), false)
	if aerr != nil {
		return nil, aerr
	}
	return struct {
		StackId string `xml:"StackId"`
	}{st.ID}, nil
}

func hUpdateStack(s *Server, p params) (any, *awshttp.APIError) {
	name := p.str("StackName")
	existing, err := s.store.GetStack(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	body, aerr := s.templateOf(p)
	if aerr != nil {
		return nil, aerr
	}
	if body == "" || p.bool_("UsePreviousTemplate") {
		body = existing.TemplateBody
	}
	// UsePreviousValue parameters keep whatever the stack already had.
	merged := map[string]string{}
	for k, v := range existing.Parameters {
		merged[k] = v
	}
	for k, v := range p.keyValues("Parameters", "ParameterKey", "ParameterValue") {
		if v != "" {
			merged[k] = v
		}
	}
	st, aerr := s.deploy(name, body, merged, p.keyValues("Tags", "Key", "Value"), true)
	if aerr != nil {
		return nil, aerr
	}
	return struct {
		StackId string `xml:"StackId"`
	}{st.ID}, nil
}

// deploy is the whole flow: transpile, apply, record, synthesize events.
//
// It is synchronous on purpose. A deploy tool polls until the stack reaches a
// terminal status; doing the work before returning means the very first poll
// succeeds, which is both faster and more honest than reporting IN_PROGRESS
// for something that already finished.
func (s *Server) deploy(name, body string, params, tags map[string]string, isUpdate bool) (*StackRecord, *awshttp.APIError) {
	tmpl, err := Parse([]byte(body))
	if err != nil {
		return nil, errValidation("%v", err)
	}
	exports, _ := s.store.Exports()
	sf, rep, err := Transpile(tmpl, TranspileOptions{
		StackName:  name,
		Parameters: params,
		Exports:    exports,
	})
	if err != nil {
		// A template that cannot be transpiled records a failed stack rather
		// than vanishing, so `describe-stacks` explains what went wrong.
		s.recordFailure(name, body, params, tags, isUpdate, err.Error())
		return nil, errValidation("%v", err)
	}

	now := s.now().Unix()
	prev, _ := s.store.GetStack(name)
	st := &StackRecord{
		Name: name, TemplateBody: body, Parameters: params, Tags: tags,
		Created: now, Updated: now,
	}
	if prev != nil {
		st.ID, st.Created = prev.ID, prev.Created
		st.TerminationProtection, st.Policy = prev.TerminationProtection, prev.Policy
	} else {
		st.ID = StackARN(name, s.store.newID())
	}

	ctx, cancel := s.ctx()
	defer cancel()
	applyRep, applyErr := provision.Apply(ctx, s.gateway, sf)

	// Resources the stack now owns, from the transpile report.
	for _, e := range rep.Entries {
		if e.Kind != Mapped {
			continue
		}
		st.Resources = append(st.Resources, StackResource{
			LogicalID: e.LogicalID, Type: e.Type, PhysicalID: e.Name,
			Status: statusVerb(isUpdate) + "_COMPLETE",
		})
	}
	for name, value := range rep.Outputs {
		out := StackOutput{Key: name, Value: value}
		if decl, ok := tmpl.Outputs[name]; ok && decl.ExportName != nil {
			// The export name may itself be an intrinsic; it was evaluated
			// during transpile, so re-evaluate against the same scope.
			if ev, everr := exportNameOf(tmpl, name, params, exports, st.Name); everr == nil {
				out.ExportName = ev
			}
			out.Description = decl.Description
		}
		st.Outputs = append(st.Outputs, out)
	}
	sort.Slice(st.Outputs, func(i, j int) bool { return st.Outputs[i].Key < st.Outputs[j].Key })

	if applyErr != nil {
		st.Status = pick(isUpdate, StatusUpdateFailed, StatusCreateFailed)
		st.StatusReason = applyErr.Error()
	} else {
		st.Status = pick(isUpdate, StatusUpdateComplete, StatusCreateComplete)
	}
	st.Events = s.synthesizeEvents(st, applyRep, isUpdate)

	if err := s.store.PutStack(st); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if applyErr != nil {
		return nil, errValidation("apply failed: %v", applyErr)
	}
	s.logf("cloudformation: stack %s %s (%d resources)", name, st.Status, len(st.Resources))
	return st, nil
}

// recordFailure stores a stack that could not even be transpiled, so the
// failure is visible through the normal describe path.
func (s *Server) recordFailure(name, body string, params, tags map[string]string, isUpdate bool, reason string) {
	now := s.now().Unix()
	st, _ := s.store.GetStack(name)
	if st == nil {
		st = &StackRecord{Name: name, ID: StackARN(name, s.store.newID()), Created: now}
	}
	st.TemplateBody, st.Parameters, st.Tags, st.Updated = body, params, tags, now
	st.Status = pick(isUpdate, StatusUpdateFailed, StatusCreateFailed)
	st.StatusReason = reason
	st.Events = append(st.Events, StackEvent{
		ID: s.store.newID(), Timestamp: now, LogicalID: name,
		Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID,
		Status: st.Status, Reason: reason,
	})
	_ = s.store.PutStack(st)
}

func statusVerb(isUpdate bool) string { return pick(isUpdate, "UPDATE", "CREATE") }

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ---- delete ----

func hDeleteStack(s *Server, p params) (any, *awshttp.APIError) {
	name := p.str("StackName")
	st, err := s.store.GetStack(name)
	if err != nil {
		// AWS's DeleteStack is idempotent for a stack that is already gone.
		return nil, nil
	}
	if st.TerminationProtection {
		return nil, errValidation("Stack [%s] cannot be deleted while termination protection is enabled", name)
	}
	// Refuse while another stack imports one of this stack's exports, which is
	// the check that keeps a multi-stack app consistent.
	for _, o := range st.Outputs {
		if o.ExportName == "" {
			continue
		}
		if importer, ok := s.importerOf(o.ExportName, name); ok {
			return nil, errValidation(
				"Export %s cannot be deleted as it is in use by %s", o.ExportName, importer)
		}
	}

	ctx, cancel := s.ctx()
	defer cancel()
	// This is the capability the transpiler alone could not have: the stack
	// recorded its own template, so it can be re-transpiled into the exact IR
	// that created it and handed to Destroy.
	if sf, terr := s.stackIR(st); terr == nil {
		rep, derr := provision.Destroy(ctx, s.gateway, sf)
		if derr != nil {
			s.logf("cloudformation: stack %s delete left resources behind: %v", name, derr)
		}
		if d, a, f := rep.Counts(); f > 0 {
			s.logf("cloudformation: %s removed %d, already absent %d, failed %d", name, d, a, f)
		}
	} else {
		s.logf("cloudformation: stack %s template no longer transpiles, resources left in place: %v", name, terr)
	}
	_ = s.store.DeleteStackChangeSets(name)

	// The record is retained in DELETE_COMPLETE rather than removed: real
	// CloudFormation keeps a deleted stack queryable by id, and `cdk destroy`
	// polls for exactly that status before declaring success. A later
	// CreateStack with the same name overwrites it, as in AWS.
	now := s.now().Unix()
	st.Status = StatusDeleteComplete
	st.StatusReason = ""
	st.Updated = now
	st.Resources = nil
	st.Outputs = nil
	st.Events = append(st.Events, StackEvent{
		ID: s.store.newID(), Timestamp: now, LogicalID: st.Name,
		Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID,
		Status: StatusDeleteComplete,
	})
	if err := s.store.PutStack(st); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	s.logf("cloudformation: stack %s deleted", name)
	return nil, nil
}

// stackIR re-derives the resource graph a stack created, so Destroy can undo
// exactly what Apply did.
func (s *Server) stackIR(st *StackRecord) (*provision.Stack, error) {
	tmpl, err := Parse([]byte(st.TemplateBody))
	if err != nil {
		return nil, err
	}
	exports, _ := s.store.Exports()
	sf, _, err := Transpile(tmpl, TranspileOptions{
		StackName: st.Name, Parameters: st.Parameters, Exports: exports,
		AllowUnsupported: true,
	})
	return sf, err
}

// importerOf reports whether a stack other than `self` imports an export.
func (s *Server) importerOf(export, self string) (string, bool) {
	stacks, _ := s.store.ListStacks()
	for _, st := range stacks {
		if st.Name == self {
			continue
		}
		if strings.Contains(st.TemplateBody, export) &&
			(strings.Contains(st.TemplateBody, "ImportValue") || strings.Contains(st.TemplateBody, "!ImportValue")) {
			return st.Name, true
		}
	}
	return "", false
}

// ---- describe ----

func hDescribeStacks(s *Server, p params) (any, *awshttp.APIError) {
	name := p.str("StackName")
	var views []stackView
	if name != "" {
		st, err := s.store.GetStack(name)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		views = append(views, viewStack(st))
	} else {
		stacks, err := s.store.ListStacks()
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		for i := range stacks {
			views = append(views, viewStack(&stacks[i]))
		}
	}
	return struct {
		Stacks []stackView `xml:"Stacks>member"`
	}{views}, nil
}

func hListStacks(s *Server, p params) (any, *awshttp.APIError) {
	stacks, err := s.store.ListStacks()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	filter := p.members("StackStatusFilter")
	type summary struct {
		StackId             string `xml:"StackId"`
		StackName           string `xml:"StackName"`
		CreationTime        string `xml:"CreationTime"`
		LastUpdatedTime     string `xml:"LastUpdatedTime,omitempty"`
		StackStatus         string `xml:"StackStatus"`
		StackStatusReason   string `xml:"StackStatusReason,omitempty"`
		TemplateDescription string `xml:"TemplateDescription,omitempty"`
	}
	var out []summary
	for i := range stacks {
		st := &stacks[i]
		if len(filter) > 0 && !containsStr(filter, st.Status) {
			continue
		}
		out = append(out, summary{
			StackId: st.ID, StackName: st.Name,
			CreationTime: awshttp.ISO8601(unix(st.Created)),
			StackStatus:  st.Status, StackStatusReason: st.StatusReason,
		})
	}
	return struct {
		StackSummaries []summary `xml:"StackSummaries>member"`
	}{out}, nil
}

func hDescribeStackEvents(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]eventView, 0, len(st.Events))
	// Newest first, which is the order deploy tools expect.
	for i := len(st.Events) - 1; i >= 0; i-- {
		e := st.Events[i]
		views = append(views, eventView{
			StackId: st.ID, StackName: st.Name, EventId: e.ID,
			LogicalResourceId: e.LogicalID, PhysicalResourceId: e.PhysicalID,
			ResourceType: e.Type, Timestamp: awshttp.ISO8601(unix(e.Timestamp)),
			ResourceStatus: e.Status, ResourceStatusReason: e.Reason,
		})
	}
	return struct {
		StackEvents []eventView `xml:"StackEvents>member"`
	}{views}, nil
}

func (s *Server) resourceViews(st *StackRecord) []resourceView {
	out := make([]resourceView, 0, len(st.Resources))
	for _, r := range st.Resources {
		out = append(out, resourceView{
			StackId: st.ID, StackName: st.Name,
			LogicalResourceId: r.LogicalID, PhysicalResourceId: r.PhysicalID,
			ResourceType: r.Type, Timestamp: awshttp.ISO8601(unix(st.Updated)),
			ResourceStatus: r.Status, ResourceStatusReason: r.Reason,
		})
	}
	return out
}

func hDescribeStackResources(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := s.resourceViews(st)
	if logical := p.str("LogicalResourceId"); logical != "" {
		views = filterResources(views, logical)
	}
	return struct {
		StackResources []resourceView `xml:"StackResources>member"`
	}{views}, nil
}

func hDescribeStackResource(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	logical := p.str("LogicalResourceId")
	views := filterResources(s.resourceViews(st), logical)
	if len(views) == 0 {
		return nil, errValidation("Resource %s does not exist for stack %s", logical, st.Name)
	}
	return struct {
		StackResourceDetail resourceView `xml:"StackResourceDetail"`
	}{views[0]}, nil
}

func hListStackResources(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type summary struct {
		LogicalResourceId    string `xml:"LogicalResourceId"`
		PhysicalResourceId   string `xml:"PhysicalResourceId,omitempty"`
		ResourceType         string `xml:"ResourceType"`
		LastUpdatedTimestamp string `xml:"LastUpdatedTimestamp"`
		ResourceStatus       string `xml:"ResourceStatus"`
	}
	out := make([]summary, 0, len(st.Resources))
	for _, r := range st.Resources {
		out = append(out, summary{
			LogicalResourceId: r.LogicalID, PhysicalResourceId: r.PhysicalID,
			ResourceType: r.Type, LastUpdatedTimestamp: awshttp.ISO8601(unix(st.Updated)),
			ResourceStatus: r.Status,
		})
	}
	return struct {
		StackResourceSummaries []summary `xml:"StackResourceSummaries>member"`
	}{out}, nil
}

func filterResources(in []resourceView, logical string) []resourceView {
	var out []resourceView
	for _, v := range in {
		if v.LogicalResourceId == logical {
			out = append(out, v)
		}
	}
	return out
}

// ---- templates ----

func hGetTemplate(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		TemplateBody    string   `xml:"TemplateBody"`
		StagesAvailable []string `xml:"StagesAvailable>member,omitempty"`
	}{st.TemplateBody, []string{"Original", "Processed"}}, nil
}

func hGetTemplateSummary(s *Server, p params) (any, *awshttp.APIError) {
	body, aerr := s.templateOf(p)
	if aerr != nil {
		return nil, aerr
	}
	if body == "" {
		st, err := s.store.GetStack(p.str("StackName"))
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		body = st.TemplateBody
	}
	tmpl, err := Parse([]byte(body))
	if err != nil {
		return nil, errValidation("%v", err)
	}
	type paramDecl struct {
		ParameterKey  string `xml:"ParameterKey"`
		DefaultValue  string `xml:"DefaultValue,omitempty"`
		ParameterType string `xml:"ParameterType"`
		NoEcho        bool   `xml:"NoEcho"`
		Description   string `xml:"Description,omitempty"`
	}
	var decls []paramDecl
	for _, name := range sortedAnyKeys(toAnyMap(tmpl.Parameters)) {
		decl := tmpl.Parameters[name]
		d := paramDecl{
			ParameterKey: name, ParameterType: decl.Type,
			NoEcho: decl.NoEcho, Description: decl.Description,
		}
		if decl.Default != nil {
			d.DefaultValue = fmt.Sprint(decl.Default)
		}
		decls = append(decls, d)
	}
	types := map[string]bool{}
	for _, r := range tmpl.Resources {
		types[r.Type] = true
	}
	var typeList []string
	for t := range types {
		typeList = append(typeList, t)
	}
	sort.Strings(typeList)
	return struct {
		Parameters         []paramDecl `xml:"Parameters>member,omitempty"`
		Description        string      `xml:"Description,omitempty"`
		ResourceTypes      []string    `xml:"ResourceTypes>member,omitempty"`
		Version            string      `xml:"Version,omitempty"`
		DeclaredTransforms []string    `xml:"DeclaredTransforms>member,omitempty"`
	}{decls, tmpl.Description, typeList, tmpl.FormatVersion, tmpl.Transform}, nil
}

func hValidateTemplate(s *Server, p params) (any, *awshttp.APIError) {
	body, aerr := s.templateOf(p)
	if aerr != nil {
		return nil, aerr
	}
	tmpl, err := Parse([]byte(body))
	if err != nil {
		return nil, errValidation("%v", err)
	}
	type paramDecl struct {
		ParameterKey string `xml:"ParameterKey"`
		DefaultValue string `xml:"DefaultValue,omitempty"`
		NoEcho       bool   `xml:"NoEcho"`
		Description  string `xml:"Description,omitempty"`
	}
	var decls []paramDecl
	for _, name := range sortedAnyKeys(toAnyMap(tmpl.Parameters)) {
		decl := tmpl.Parameters[name]
		d := paramDecl{ParameterKey: name, NoEcho: decl.NoEcho, Description: decl.Description}
		if decl.Default != nil {
			d.DefaultValue = fmt.Sprint(decl.Default)
		}
		decls = append(decls, d)
	}
	return struct {
		Parameters         []paramDecl `xml:"Parameters>member,omitempty"`
		Description        string      `xml:"Description,omitempty"`
		DeclaredTransforms []string    `xml:"DeclaredTransforms>member,omitempty"`
	}{decls, tmpl.Description, tmpl.Transform}, nil
}

// ---- exports ----

func hListExports(s *Server, _ params) (any, *awshttp.APIError) {
	stacks, err := s.store.ListStacks()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type export struct {
		ExportingStackId string `xml:"ExportingStackId"`
		Name             string `xml:"Name"`
		Value            string `xml:"Value"`
	}
	var out []export
	for i := range stacks {
		st := &stacks[i]
		for _, o := range st.Outputs {
			if o.ExportName != "" {
				out = append(out, export{st.ID, o.ExportName, o.Value})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return struct {
		Exports []export `xml:"Exports>member"`
	}{out}, nil
}

func hListImports(s *Server, p params) (any, *awshttp.APIError) {
	export := p.str("ExportName")
	stacks, _ := s.store.ListStacks()
	var importers []string
	for _, st := range stacks {
		if strings.Contains(st.TemplateBody, export) && strings.Contains(st.TemplateBody, "ImportValue") {
			importers = append(importers, st.Name)
		}
	}
	sort.Strings(importers)
	return struct {
		Imports []string `xml:"Imports>member,omitempty"`
	}{importers}, nil
}

// ---- configuration round-trips ----

func hSetStackPolicy(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	st.Policy = p.str("StackPolicyBody")
	return nil, awshttp.AsAPIErrorOrNil(s.store.PutStack(st))
}

func hGetStackPolicy(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		StackPolicyBody string `xml:"StackPolicyBody,omitempty"`
	}{st.Policy}, nil
}

func hUpdateTerminationProtection(s *Server, p params) (any, *awshttp.APIError) {
	st, err := s.store.GetStack(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	st.TerminationProtection = p.bool_("EnableTerminationProtection")
	if err := s.store.PutStack(st); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		StackId string `xml:"StackId"`
	}{st.ID}, nil
}

// hCancelUpdateStack always answers "nothing in flight": apply is synchronous,
// so by the time a caller could cancel, the update has already finished.
func hCancelUpdateStack(s *Server, p params) (any, *awshttp.APIError) {
	return nil, errValidation(
		"Stack [%s] has no update in progress — doze-aws applies synchronously", p.str("StackName"))
}

// ---- helpers ----

func containsStr(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func toAnyMap[T any](m map[string]T) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// exportNameOf re-evaluates an output's Export.Name, which may be an intrinsic.
func exportNameOf(t *Template, output string, params map[string]string, exports map[string]string, stackName string) (string, error) {
	decl := t.Outputs[output]
	if decl.ExportName == nil {
		return "", nil
	}
	scope := &Scope{StackName: stackName, Exports: exports, Parameters: map[string]any{}}
	for k, v := range params {
		scope.Parameters[k] = v
	}
	for name, p := range t.Parameters {
		if _, set := scope.Parameters[name]; !set && p.Default != nil {
			scope.Parameters[name] = p.Default
		}
	}
	v, err := scope.Eval(decl.ExportName)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(v), nil
}

var _ = xml.Name{} // the view structs are rendered by awsquery's encoder
