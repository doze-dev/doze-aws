package sns

// The topic policy, evaluated on topic-scoped requests under IAM soft or
// enforce, and the two operations that write it. A topic is born with the
// policy AWS attaches — every identity in the account may use it — so the
// guard changes nothing until a policy narrows it; an S3 notification or an
// EventBridge target arriving as a service principal is admitted by the
// default policy's account condition, as on AWS.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awsquery"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
)

// effectivePolicy is the topic's access policy: the one set on it, else the
// default AWS attaches. GetTopicAttributes reports this one value, once.
func (srv *Server) effectivePolicy(arn string) string {
	if t, err := srv.store.GetTopic(arn); err == nil && t.Attrs["Policy"] != "" {
		return t.Attrs["Policy"]
	}
	return defaultTopicPolicy(arn)
}

// guardRequest runs the guard for a request that names a topic.
func (srv *Server) guardRequest(w http.ResponseWriter, r *http.Request, action string, form url.Values) *apiError {
	if srv.guard.Mode == "" && r.Header.Get(iamguard.HeaderMode) == "" {
		return nil
	}
	// A request naming no topic, or a topic that does not exist, has no
	// resource policy: the identity verdict alone decides, as on AWS.
	arn := form.Get("TopicArn")
	var docs []*iampolicy.Document
	if arn != "" && srv.store.TopicExists(arn) {
		if doc, err := iampolicy.Parse(srv.effectivePolicy(arn)); err == nil {
			docs = append(docs, doc)
		}
	}
	if aerr := srv.guard.Check(w, r, docs, "sns:"+action, arn); aerr != nil {
		return &apiError{Code: aerr.Code, Status: aerr.Status, Message: aerr.Message, SenderFault: true}
	}
	return nil
}

type topicPolicyDoc struct {
	Version   string           `json:"Version"`
	ID        string           `json:"Id,omitempty"`
	Statement []map[string]any `json:"Statement"`
}

// addPermission writes the statement AWS writes: Sid = Label, the AWS
// account ids as principals, the actions prefixed SNS:, on the topic.
func (srv *Server) addPermission(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	arn := form.Get("TopicArn")
	if !srv.store.TopicExists(arn) {
		return nil, errNotFound("topic does not exist: " + arn)
	}
	label := form.Get("Label")
	accounts := awsquery.Members(form, "AWSAccountId", false)
	actions := awsquery.Members(form, "ActionName", false)
	if label == "" || len(accounts) == 0 || len(actions) == 0 {
		return nil, errInvalid("Label, AWSAccountId and ActionName are required")
	}
	var doc topicPolicyDoc
	json.Unmarshal([]byte(srv.effectivePolicy(arn)), &doc)
	for _, st := range doc.Statement {
		if sid, _ := st["Sid"].(string); sid == label {
			return nil, errInvalid("Statement already exists with the same Label: " + label)
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
		acts = append(acts, "SNS:"+strings.TrimPrefix(strings.TrimPrefix(a, "SNS:"), "sns:"))
	}
	doc.Statement = append(doc.Statement, map[string]any{
		"Sid": label, "Effect": "Allow", "Principal": map[string]any{"AWS": principals},
		"Action": acts, "Resource": arn,
	})
	raw, _ := json.Marshal(doc)
	return nil, asErr(srv.store.UpdateTopic(arn, func(t *Topic) {
		if t.Attrs == nil {
			t.Attrs = map[string]string{}
		}
		t.Attrs["Policy"] = string(raw)
	}))
}

func (srv *Server) removePermission(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	arn := form.Get("TopicArn")
	if !srv.store.TopicExists(arn) {
		return nil, errNotFound("topic does not exist: " + arn)
	}
	label := form.Get("Label")
	var doc topicPolicyDoc
	json.Unmarshal([]byte(srv.effectivePolicy(arn)), &doc)
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
		return nil, errNotFound("Statement " + label + " does not exist")
	}
	doc.Statement = kept
	raw, _ := json.Marshal(doc)
	return nil, asErr(srv.store.UpdateTopic(arn, func(t *Topic) {
		if t.Attrs == nil {
			t.Attrs = map[string]string{}
		}
		t.Attrs["Policy"] = string(raw)
	}))
}
