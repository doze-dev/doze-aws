package lambda

// Function resource policies: AddPermission, RemovePermission, GetPolicy.
//
// This is the API behind `AWS::Lambda::Permission`, which appears in nearly
// every SAM or CloudFormation template that puts an event source in front of a
// function. Nothing locally consults the policy — doze-aws does not gate
// invocation on it — but the statements round-trip faithfully, which is what a
// template deployment and a subsequent describe both need.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// PolicyStatement is one statement of a function's resource policy, kept in the
// shape AWS returns rather than a normalised form, so GetPolicy echoes exactly
// what AddPermission was given.
type PolicyStatement struct {
	Sid       string          `json:"Sid"`
	Effect    string          `json:"Effect"`
	Principal any             `json:"Principal"`
	Action    string          `json:"Action"`
	Resource  string          `json:"Resource"`
	Condition json.RawMessage `json:"Condition,omitempty"`
}

// policyDoc is the document GetPolicy returns; AWS delivers it as a JSON string
// inside a JSON field, which is why it is marshalled twice.
type policyDoc struct {
	Version   string            `json:"Version"`
	Id        string            `json:"Id"`
	Statement []PolicyStatement `json:"Statement"`
}

func (s *Server) routePolicy(w http.ResponseWriter, r *http.Request, name string, segs []string) *awshttp.APIError {
	// /functions/{name}/policy/{statementId} is the remove form.
	statementID := ""
	if len(segs) >= 5 {
		statementID = segs[4]
	}
	switch r.Method {
	case http.MethodPost:
		return s.addPermission(w, r, name)
	case http.MethodGet:
		return s.getPolicy(w, name)
	case http.MethodDelete:
		if statementID == "" {
			statementID = r.URL.Query().Get("StatementId")
		}
		return s.removePermission(w, name, statementID)
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on function policy")
}

func (s *Server) addPermission(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	var req struct {
		StatementId      string          `json:"StatementId"`
		Action           string          `json:"Action"`
		Principal        string          `json:"Principal"`
		SourceArn        string          `json:"SourceArn"`
		SourceAccount    string          `json:"SourceAccount"`
		PrincipalOrgID   string          `json:"PrincipalOrgID"`
		EventSourceToken string          `json:"EventSourceToken"`
		Qualifier        string          `json:"Qualifier"`
		Condition        json.RawMessage `json:"Condition"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if req.StatementId == "" || req.Action == "" || req.Principal == "" {
		return awshttp.Errf(400, "InvalidParameterValueException",
			"StatementId, Action and Principal are all required")
	}

	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	resource := f.ARN()
	if q := r.URL.Query().Get("Qualifier"); q != "" {
		resource += ":" + q
	}

	// A service principal ("s3.amazonaws.com") and an account principal are
	// spelled differently in the document AWS stores.
	var principal any
	if strings.Contains(req.Principal, ".amazonaws.com") {
		principal = map[string]string{"Service": req.Principal}
	} else if req.Principal == "*" {
		principal = "*"
	} else {
		principal = map[string]string{"AWS": req.Principal}
	}

	// SourceArn / SourceAccount become the ArnLike / StringEquals conditions
	// AWS synthesizes, unless an explicit Condition was supplied.
	cond := req.Condition
	if len(cond) == 0 {
		parts := map[string]map[string]string{}
		if req.SourceArn != "" {
			parts["ArnLike"] = map[string]string{"AWS:SourceArn": req.SourceArn}
		}
		if req.SourceAccount != "" {
			parts["StringEquals"] = map[string]string{"AWS:SourceAccount": req.SourceAccount}
		}
		if req.PrincipalOrgID != "" {
			if parts["StringEquals"] == nil {
				parts["StringEquals"] = map[string]string{}
			}
			parts["StringEquals"]["AWS:PrincipalOrgID"] = req.PrincipalOrgID
		}
		if len(parts) > 0 {
			cond, _ = json.Marshal(parts)
		}
	}

	stmt := PolicyStatement{
		Sid: req.StatementId, Effect: "Allow", Principal: principal,
		Action: req.Action, Resource: resource, Condition: cond,
	}

	updated, err := s.store.Update(name, func(f *Function) error {
		for _, existing := range f.Policy {
			if existing.Sid == req.StatementId {
				return awshttp.Errf(409, "ResourceConflictException",
					"The statement id (%s) provided already exists. Please provide a new statement id, or remove the existing statement.",
					req.StatementId)
			}
		}
		f.Policy = append(f.Policy, stmt)
		return nil
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	_ = updated

	// AddPermission returns the single statement it added, as a JSON string.
	raw, _ := json.Marshal(stmt)
	writeJSON(w, 201, map[string]any{"Statement": string(raw)})
	return nil
}

func (s *Server) getPolicy(w http.ResponseWriter, name string) *awshttp.APIError {
	f, err := s.store.GetFunction(name)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if len(f.Policy) == 0 {
		return awshttp.Errf(404, "ResourceNotFoundException",
			"The resource you requested does not exist.")
	}
	doc := policyDoc{Version: "2012-10-17", Id: "default", Statement: f.Policy}
	raw, err := json.Marshal(doc)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, map[string]any{"Policy": string(raw), "RevisionId": f.Revision})
	return nil
}

func (s *Server) removePermission(w http.ResponseWriter, name, statementID string) *awshttp.APIError {
	if statementID == "" {
		return awshttp.Errf(400, "InvalidParameterValueException", "StatementId is required")
	}
	_, err := s.store.Update(name, func(f *Function) error {
		for i, stmt := range f.Policy {
			if stmt.Sid == statementID {
				f.Policy = append(f.Policy[:i], f.Policy[i+1:]...)
				return nil
			}
		}
		return awshttp.Errf(404, "ResourceNotFoundException",
			"The resource you requested does not exist.")
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(204)
	return nil
}
