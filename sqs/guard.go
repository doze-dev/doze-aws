package sqs

// The queue policy, evaluated on the request under IAM soft or enforce, and
// the two operations that write it: AddPermission synthesizes the statement
// AWS synthesizes (an Allow for the named accounts on the named actions, by
// label), RemovePermission drops it by label. SNS deliveries and EventBridge
// targets arrive as peer calls stamped with a service principal, so a queue
// that a topic fans out to needs a policy admitting sns.amazonaws.com under
// enforce, exactly as on AWS.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// guardRequest runs the guard for a queue-scoped request.
func (s *Server) guardRequest(w http.ResponseWriter, r *http.Request, req *request) *apiError {
	if s.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	// A request naming no queue, or a queue that does not exist, has no
	// resource policy: the identity verdict alone decides, as on AWS.
	queue := targetQueue(req)
	resource := ""
	var docs []*iampolicy.Document
	if queue != "" {
		resource = s.id.ARN("sqs", queue)
		if attrs, err := s.store.Attributes(queue); err == nil && attrs["Policy"] != "" {
			if doc, err := iampolicy.Parse(attrs["Policy"]); err == nil {
				docs = append(docs, doc)
			}
		}
	}
	if aerr := s.guard.Check(w, r, docs, iamguard.Action("sqs", req.action), resource); aerr != nil {
		return &apiError{Code: aerr.Code, Status: aerr.Status, Message: aerr.Message, SenderFault: true, Denied: aerr.Denied}
	}
	return nil
}

// queuePolicyDoc is the queue policy as a document the permission calls edit.
type queuePolicyDoc struct {
	Version   string           `json:"Version"`
	ID        string           `json:"Id,omitempty"`
	Statement []map[string]any `json:"Statement"`
}

func loadQueuePolicy(attrs map[string]string) queuePolicyDoc {
	doc := queuePolicyDoc{Version: "2012-10-17"}
	if raw := attrs["Policy"]; raw != "" {
		json.Unmarshal([]byte(raw), &doc)
	}
	if doc.Version == "" {
		doc.Version = "2012-10-17"
	}
	return doc
}

// hAddPermission writes the statement AWS writes: Sid = Label, the AWS
// account ids as principals, the actions prefixed sqs:, on the queue.
func hAddPermission(s *store, req *request) (any, *apiError) {
	name := targetQueue(req)
	attrs, err := s.Attributes(name)
	if err != nil {
		return nil, asAPIError(err)
	}
	label := req.p.str("Label")
	accounts := req.p.stringList("AWSAccountIds")
	actions := req.p.stringList("Actions")
	if label == "" || len(accounts) == 0 || len(actions) == 0 {
		return nil, &apiError{Code: "MissingParameter", Status: 400, SenderFault: true,
			Message: "Label, AWSAccountIds and Actions are required"}
	}
	if aerr := validPermission(label, accounts, actions); aerr != nil {
		return nil, aerr
	}
	if raw := attrs["Policy"]; raw != "" && !json.Valid([]byte(raw)) {
		// A stored policy this cannot read is not silently replaced.
		return nil, &apiError{Code: "InvalidParameterValue", Status: 400, SenderFault: true,
			Message: "The queue's Policy attribute is not a JSON document; set it before adding a permission"}
	}
	doc := loadQueuePolicy(attrs)
	for _, st := range doc.Statement {
		if sid, _ := st["Sid"].(string); sid == label {
			return nil, &apiError{Code: "InvalidParameterValue", Status: 400, SenderFault: true,
				Message: "Value " + label + " for parameter Label is invalid. Reason: Already exists."}
		}
	}
	var principals []string
	for _, a := range accounts {
		if strings.HasPrefix(a, "arn:") {
			principals = append(principals, a)
		} else {
			principals = append(principals, "arn:aws:iam::"+a+":root")
		}
	}
	var acts []string
	for _, a := range actions {
		if a == "*" {
			acts = append(acts, "SQS:*")
		} else {
			acts = append(acts, "SQS:"+strings.TrimPrefix(strings.TrimPrefix(a, "SQS:"), "sqs:"))
		}
	}
	doc.Statement = append(doc.Statement, map[string]any{
		"Sid": label, "Effect": "Allow", "Principal": map[string]any{"AWS": principals},
		"Action": acts, "Resource": s.queueARN(name),
	})
	raw, _ := json.Marshal(doc)
	if err := s.SetAttributes(name, map[string]string{"Policy": string(raw)}); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

// permissionActions are the values AddPermission takes for Actions: "the name
// of any action or *" (doc: the model's AddPermission), which is any of SQS's
// own operations.
var permissionActions = map[string]bool{
	"*": true, "AddPermission": true, "CancelMessageMoveTask": true, "ChangeMessageVisibility": true,
	"ChangeMessageVisibilityBatch": true, "CreateQueue": true, "DeleteMessage": true,
	"DeleteMessageBatch": true, "DeleteQueue": true, "GetQueueAttributes": true, "GetQueueUrl": true,
	"ListDeadLetterSourceQueues": true, "ListMessageMoveTasks": true, "ListQueueTags": true,
	"ListQueues": true, "PurgeQueue": true, "ReceiveMessage": true, "RemovePermission": true,
	"SendMessage": true, "SendMessageBatch": true, "SetQueueAttributes": true,
	"StartMessageMoveTask": true, "TagQueue": true, "UntagQueue": true,
}

// validPermission applies what the AddPermission page states: a label of at
// most 80 characters of letters, digits, hyphens and underscores; actions from
// the list above; principals that are twelve-digit account numbers (an ARN is
// still accepted, which the page does not rule out). The model states none of
// it.
func validPermission(label string, accounts, actions []string) *apiError {
	bad := func(param, why string) *apiError {
		return &apiError{Code: "InvalidParameterValue", Status: 400, SenderFault: true,
			Message: "Value for parameter " + param + " is invalid. Reason: " + why}
	}
	if len(label) > 80 {
		return bad("Label", "a label is at most 80 characters.")
	}
	for _, r := range label {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return bad("Label", "a label has only letters, digits, hyphens and underscores.")
		}
	}
	for _, a := range accounts {
		if strings.HasPrefix(a, "arn:") {
			continue
		}
		if len(a) != 12 || strings.Trim(a, "0123456789") != "" {
			return bad("AWSAccountIds", "an account number is twelve digits.")
		}
	}
	for _, a := range actions {
		if !permissionActions[strings.TrimPrefix(strings.TrimPrefix(a, "SQS:"), "sqs:")] {
			return bad("Actions", "\""+a+"\" is not an action AddPermission accepts.")
		}
	}
	return nil
}

func hRemovePermission(s *store, req *request) (any, *apiError) {
	name := targetQueue(req)
	attrs, err := s.Attributes(name)
	if err != nil {
		return nil, asAPIError(err)
	}
	label := req.p.str("Label")
	doc := loadQueuePolicy(attrs)
	kept := doc.Statement[:0]
	found := false
	for _, st := range doc.Statement {
		if sid, _ := st["Sid"].(string); sid == label {
			found = true
			continue
		}
		kept = append(kept, st)
	}
	if !found {
		return nil, &apiError{Code: "InvalidParameterValue", Status: 400, SenderFault: true,
			Message: "Value " + label + " for parameter Label is invalid. Reason: Label does not exist."}
	}
	doc.Statement = kept
	value := ""
	if len(kept) > 0 {
		raw, _ := json.Marshal(doc)
		value = string(raw)
	}
	if err := s.SetAttributes(name, map[string]string{"Policy": value}); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}
