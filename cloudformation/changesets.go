package cloudformation

// Change sets — the path `aws cloudformation deploy` and `sam deploy` take.
//
// Their flow is: CreateChangeSet, poll DescribeChangeSet until it leaves
// CREATE_PENDING, then ExecuteChangeSet. The subtlety that trips up naive
// implementations is the empty case: when nothing changed, the real API puts
// the change set in FAILED with a StatusReason containing "didn't contain
// changes", and the CLI treats that specific failure as success. Getting that
// string wrong turns a no-op redeploy into a hard error.

import (
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func hCreateChangeSet(s *Server, p params) (any, *awshttp.APIError) {
	stackName := p.str("StackName")
	csName := p.str("ChangeSetName")
	if stackName == "" || csName == "" {
		return nil, errValidation("StackName and ChangeSetName are both required")
	}
	body, aerr := s.templateOf(p)
	if aerr != nil {
		return nil, aerr
	}

	existing, _ := s.store.GetStack(stackName)
	csType := p.str("ChangeSetType")
	if csType == "" {
		csType = pick(existing != nil, "UPDATE", "CREATE")
	}
	if csType == "UPDATE" && existing == nil {
		return nil, awshttp.Errf(400, "ValidationError",
			"Stack [%s] does not exist", stackName)
	}
	if body == "" && existing != nil {
		body = existing.TemplateBody
	}

	params := map[string]string{}
	if existing != nil {
		for k, v := range existing.Parameters {
			params[k] = v
		}
	}
	for k, v := range p.keyValues("Parameters", "ParameterKey", "ParameterValue") {
		if v != "" {
			params[k] = v
		}
	}

	// Transpile now so a broken template fails at change-set creation, which is
	// where the deploy tool expects to see it.
	tmpl, err := Parse([]byte(body))
	if err != nil {
		return nil, errValidation("%v", err)
	}
	exports, _ := s.store.Exports()
	_, rep, err := Transpile(tmpl, TranspileOptions{
		StackName: stackName, Parameters: params, Exports: exports,
	})
	if err != nil {
		return nil, errValidation("%v", err)
	}

	now := s.now().Unix()
	// A CREATE change set materialises the stack immediately, in
	// REVIEW_IN_PROGRESS. Deploy tools describe it and poll its events between
	// CreateChangeSet and ExecuteChangeSet, so it has to be there.
	stackID := ""
	if existing != nil {
		stackID = existing.ID
	} else {
		review := &StackRecord{
			Name: stackName, ID: StackARN(stackName, s.store.newID()),
			Status: StatusReviewInProgress, TemplateBody: body,
			Parameters: params, Created: now, Updated: now,
		}
		review.Events = []StackEvent{{
			ID: s.store.newID(), Timestamp: now, LogicalID: stackName,
			Type: "AWS::CloudFormation::Stack", PhysicalID: review.ID,
			Status: StatusReviewInProgress, Reason: "User Initiated",
		}}
		if err := s.store.PutStack(review); err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		stackID = review.ID
	}

	cs := &ChangeSetRecord{
		Name:      csName,
		ID:        stackID + "/changeSet/" + csName,
		StackName: stackName,
		Status:    "CREATE_COMPLETE",
		// AVAILABLE is what tells a deploy tool the set may be executed.
		ExecutionStatus: "AVAILABLE",
		TemplateBody:    body,
		Parameters:      params,
		Type:            csType,
		Tags:            p.keyValues("Tags", "Key", "Value"),
		Created:         now,
		Changes:         diffChanges(existing, rep),
	}
	// The empty-change case the CLI special-cases by message.
	if len(cs.Changes) == 0 {
		cs.Status = "FAILED"
		cs.ExecutionStatus = "UNAVAILABLE"
		cs.StatusReason = "The submitted information didn't contain changes. " +
			"Submit different information to create a change set."
	}
	if err := s.store.PutChangeSet(cs); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Id      string `xml:"Id"`
		StackId string `xml:"StackId"`
	}{cs.ID, stackID}, nil
}

// diffChanges compares what the template would produce against what the stack
// already owns. It is resource-level, which is all a change set displays.
func diffChanges(existing *StackRecord, rep *Report) []Change {
	current := map[string]StackResource{}
	if existing != nil {
		for _, r := range existing.Resources {
			current[r.LogicalID] = r
		}
	}
	var out []Change
	seen := map[string]bool{}
	for _, e := range rep.Entries {
		if e.Kind != Mapped {
			continue
		}
		seen[e.LogicalID] = true
		prev, had := current[e.LogicalID]
		switch {
		case !had:
			out = append(out, Change{Action: "Add", LogicalID: e.LogicalID, Type: e.Type, PhysicalID: e.Name})
		case prev.PhysicalID != e.Name:
			// A renamed physical resource is a replacement in AWS's model.
			out = append(out, Change{
				Action: "Modify", LogicalID: e.LogicalID, Type: e.Type,
				PhysicalID: e.Name, Replacement: "True",
			})
		}
	}
	for id, r := range current {
		if !seen[id] {
			out = append(out, Change{Action: "Remove", LogicalID: id, Type: r.Type, PhysicalID: r.PhysicalID})
		}
	}
	return out
}

func hDescribeChangeSet(s *Server, p params) (any, *awshttp.APIError) {
	cs, err := s.store.GetChangeSet(p.str("StackName"), p.str("ChangeSetName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type resourceChange struct {
		Action             string `xml:"Action"`
		LogicalResourceId  string `xml:"LogicalResourceId"`
		PhysicalResourceId string `xml:"PhysicalResourceId,omitempty"`
		ResourceType       string `xml:"ResourceType"`
		Replacement        string `xml:"Replacement,omitempty"`
	}
	type change struct {
		Type           string         `xml:"Type"`
		ResourceChange resourceChange `xml:"ResourceChange"`
	}
	changes := make([]change, 0, len(cs.Changes))
	for _, c := range cs.Changes {
		changes = append(changes, change{
			Type: "Resource",
			ResourceChange: resourceChange{
				Action: c.Action, LogicalResourceId: c.LogicalID,
				PhysicalResourceId: c.PhysicalID, ResourceType: c.Type,
				Replacement: c.Replacement,
			},
		})
	}
	var paramViews []parameterView
	for _, k := range sortedKeys(cs.Parameters) {
		paramViews = append(paramViews, parameterView{k, cs.Parameters[k]})
	}
	return struct {
		ChangeSetName   string          `xml:"ChangeSetName"`
		ChangeSetId     string          `xml:"ChangeSetId"`
		StackId         string          `xml:"StackId"`
		StackName       string          `xml:"StackName"`
		Status          string          `xml:"Status"`
		StatusReason    string          `xml:"StatusReason,omitempty"`
		ExecutionStatus string          `xml:"ExecutionStatus"`
		CreationTime    string          `xml:"CreationTime"`
		Parameters      []parameterView `xml:"Parameters>member,omitempty"`
		Changes         []change        `xml:"Changes>member,omitempty"`
	}{
		cs.Name, cs.ID, StackARN(cs.StackName, ""), cs.StackName,
		cs.Status, cs.StatusReason, cs.ExecutionStatus,
		awshttp.ISO8601(unix(cs.Created)), paramViews, changes,
	}, nil
}

func hExecuteChangeSet(s *Server, p params) (any, *awshttp.APIError) {
	cs, err := s.store.GetChangeSet(p.str("StackName"), p.str("ChangeSetName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if cs.ExecutionStatus != "AVAILABLE" {
		return nil, awshttp.Errf(400, "InvalidChangeSetStatus",
			"ChangeSet [%s] cannot be executed in its current status of [%s]", cs.Name, cs.Status)
	}
	if _, aerr := s.deploy(cs.StackName, cs.TemplateBody, cs.Parameters, cs.Tags,
		cs.Type == "UPDATE"); aerr != nil {
		cs.ExecutionStatus = "EXECUTE_FAILED"
		_ = s.store.PutChangeSet(cs)
		return nil, aerr
	}
	cs.ExecutionStatus = "EXECUTE_COMPLETE"
	_ = s.store.PutChangeSet(cs)
	return nil, nil
}

func hDeleteChangeSet(s *Server, p params) (any, *awshttp.APIError) {
	stack := p.str("StackName")
	name := p.str("ChangeSetName")
	if cs, err := s.store.GetChangeSet(stack, name); err == nil {
		stack, name = cs.StackName, cs.Name
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteChangeSet(stack, name))
}

func hListChangeSets(s *Server, p params) (any, *awshttp.APIError) {
	sets, err := s.store.ListChangeSets(p.str("StackName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type summary struct {
		ChangeSetId     string `xml:"ChangeSetId"`
		ChangeSetName   string `xml:"ChangeSetName"`
		StackId         string `xml:"StackId"`
		StackName       string `xml:"StackName"`
		Status          string `xml:"Status"`
		StatusReason    string `xml:"StatusReason,omitempty"`
		ExecutionStatus string `xml:"ExecutionStatus"`
		CreationTime    string `xml:"CreationTime"`
	}
	out := make([]summary, 0, len(sets))
	for _, cs := range sets {
		out = append(out, summary{
			cs.ID, cs.Name, StackARN(cs.StackName, ""), cs.StackName,
			cs.Status, cs.StatusReason, cs.ExecutionStatus,
			awshttp.ISO8601(unix(cs.Created)),
		})
	}
	return struct {
		Summaries []summary `xml:"Summaries>member"`
	}{out}, nil
}

// hasChangeSetSuffix reports whether a name is really a change-set ARN, which
// deploy tools sometimes pass where a name is expected.
func hasChangeSetSuffix(v string) bool { return strings.Contains(v, "/changeSet/") }
