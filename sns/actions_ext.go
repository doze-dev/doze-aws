package sns

// Handlers and codec helpers added in the doze-aws port: tags, topic-attribute
// round-trips, data-protection policy, permission no-ops, and honest stubs for
// the mobile-push/SMS surface that is physically meaningless locally.

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awsquery"
)

func init() {
	extra := map[string]func(*Server, context.Context, url.Values, string) (any, *apiError){
		"SetTopicAttributes":      (*Server).setTopicAttributes,
		"TagResource":             (*Server).tagResource,
		"UntagResource":           (*Server).untagResource,
		"ListTagsForResource":     (*Server).listTagsForResource,
		"AddPermission":           (*Server).addPermission,
		"RemovePermission":        (*Server).removePermission,
		"PutDataProtectionPolicy": (*Server).putDataProtectionPolicy,
		"GetDataProtectionPolicy": (*Server).getDataProtectionPolicy,
		// doze-only: who would actually receive this, and why not.
		"DozeMatchSubscriptions": (*Server).dozeMatchSubscriptions,
	}
	for name, h := range extra {
		dispatch[name] = h
	}
	// Tier S: the phone/SMS/mobile-push surface needs carrier and platform
	// infrastructure that cannot exist locally. Each answers with a clean,
	// honest error instead of pretending.
	for _, name := range []string{
		"CheckIfPhoneNumberIsOptedOut", "OptInPhoneNumber", "ListPhoneNumbersOptedOut",
		"GetSMSAttributes", "SetSMSAttributes", "GetSMSSandboxAccountStatus",
		"CreateSMSSandboxPhoneNumber", "DeleteSMSSandboxPhoneNumber", "ListSMSSandboxPhoneNumbers",
		"VerifySMSSandboxPhoneNumber", "ListOriginationNumbers",
		"CreatePlatformApplication", "DeletePlatformApplication", "ListPlatformApplications",
		"GetPlatformApplicationAttributes", "SetPlatformApplicationAttributes",
		"CreatePlatformEndpoint", "DeleteEndpoint", "GetEndpointAttributes",
		"SetEndpointAttributes", "ListEndpointsByPlatformApplication",
	} {
		dispatch[name] = stubHandler(name)
	}
}

func stubHandler(name string) func(*Server, context.Context, url.Values, string) (any, *apiError) {
	return func(*Server, context.Context, url.Values, string) (any, *apiError) {
		return nil, &apiError{
			Code:    "UnsupportedOperationException",
			Status:  400,
			Message: fmt.Sprintf("%s is not supported by doze-aws: SMS and mobile-push delivery need carrier/platform infrastructure that does not exist locally", name),
		}
	}
}

// memberTags parses Tags.member.N.Key/Value (CreateTopic, TagResource).
func memberTags(form url.Values) map[string]string {
	return awsquery.PairMap(form, "Tags.member", "Key", "Value")
}

// entryMessageAttributes parses a PublishBatch entry's MessageAttributes.
func entryMessageAttributes(form url.Values, base string) map[string]attr {
	return awsquery.MessageAttrs(form, base+"MessageAttributes.entry")
}

func sortedAttrKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// topicAttributes is what SetTopicAttributes may write. Hand-derived from the
// API reference; the model types AttributeName as a bare string.
//
// It used to store anything under any name, and GetTopicAttributes handed it
// back — so a misspelt "DisplyName" was accepted, kept, and reported as an
// attribute the topic has. FifoTopic is deliberately absent: it is decided at
// CreateTopic and cannot be changed afterwards.
var topicAttributes = map[string]bool{
	"DeliveryPolicy": true, "DisplayName": true, "Policy": true,
	"TracingConfig": true, "KmsMasterKeyId": true, "SignatureVersion": true,
	"ContentBasedDeduplication": true, "ArchivePolicy": true, "FifoThroughputScope": true,
}

// settableTopicAttribute also admits the delivery-status attributes, which
// are a family rather than a list: one role and sample rate per protocol.
func settableTopicAttribute(name string) bool {
	if topicAttributes[name] {
		return true
	}
	for _, protocol := range []string{"HTTP", "Firehose", "Lambda", "Application", "SQS"} {
		switch strings.TrimPrefix(name, protocol) {
		case "SuccessFeedbackRoleArn", "SuccessFeedbackSampleRate", "FailureFeedbackRoleArn":
			return strings.HasPrefix(name, protocol)
		}
	}
	return false
}

func (srv *Server) setTopicAttributes(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	name, value := form.Get("AttributeName"), form.Get("AttributeValue")
	if name == "" {
		return nil, errInvalid("AttributeName is required")
	}
	if !settableTopicAttribute(name) {
		return nil, errInvalid("Invalid parameter: AttributeName")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("TopicArn"), func(t *topic) {
		if t.Attrs == nil {
			t.Attrs = map[string]string{}
		}
		t.Attrs[name] = value
	}))
}

type listResourceTagsResult struct {
	Tags struct {
		Member []tagMember `xml:"member"`
	} `xml:"Tags"`
}

type tagMember struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func (srv *Server) tagResource(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	tags := memberTags(form)
	if len(tags) == 0 {
		return nil, errInvalid("at least one tag is required")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *topic) {
		if t.Tags == nil {
			t.Tags = map[string]string{}
		}
		for k, v := range tags {
			t.Tags[k] = v
		}
	}))
}

func (srv *Server) untagResource(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	keys := awsquery.Members(form, "TagKeys", false)
	if len(keys) == 0 {
		return nil, errInvalid("at least one tag key is required")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *topic) {
		for _, k := range keys {
			delete(t.Tags, k)
		}
	}))
}

func (srv *Server) listTagsForResource(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	t, err := srv.store.GetTopic(form.Get("ResourceArn"))
	if err != nil {
		return nil, asErr(err)
	}
	var res listResourceTagsResult
	for _, k := range sortedAttrKeys(t.Tags) {
		res.Tags.Member = append(res.Tags.Member, tagMember{Key: k, Value: t.Tags[k]})
	}
	return res, nil
}

// addPermission / removePermission are Tier C: no IAM locally, so the calls
// succeed and change nothing.

func (srv *Server) putDataProtectionPolicy(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *topic) {
		t.DataProtectionPolicy = form.Get("DataProtectionPolicy")
	}))
}

type dataProtectionResult struct {
	DataProtectionPolicy string `xml:"DataProtectionPolicy"`
}

func (srv *Server) getDataProtectionPolicy(ctx context.Context, form url.Values, _ string) (any, *apiError) {
	t, err := srv.store.GetTopic(form.Get("ResourceArn"))
	if err != nil {
		return nil, asErr(err)
	}
	return dataProtectionResult{DataProtectionPolicy: t.DataProtectionPolicy}, nil
}
