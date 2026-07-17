package sns

// Handlers and codec helpers added in the doze-aws port: tags, topic-attribute
// round-trips, data-protection policy, permission no-ops, and honest stubs for
// the mobile-push/SMS surface that is physically meaningless locally.

import (
	"fmt"
	"net/url"
	"sort"

	"github.com/doze-dev/doze-aws/internal/awsquery"
)

func init() {
	extra := map[string]func(*Server, url.Values, string) (any, *apiError){
		"SetTopicAttributes":      (*Server).setTopicAttributes,
		"TagResource":             (*Server).tagResource,
		"UntagResource":           (*Server).untagResource,
		"ListTagsForResource":     (*Server).listTagsForResource,
		"AddPermission":           (*Server).addPermission,
		"RemovePermission":        (*Server).removePermission,
		"PutDataProtectionPolicy": (*Server).putDataProtectionPolicy,
		"GetDataProtectionPolicy": (*Server).getDataProtectionPolicy,
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

func stubHandler(name string) func(*Server, url.Values, string) (any, *apiError) {
	return func(*Server, url.Values, string) (any, *apiError) {
		return nil, &apiError{
			Code:    "InvalidAction",
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
func entryMessageAttributes(form url.Values, base string) map[string]Attr {
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

func (srv *Server) setTopicAttributes(form url.Values, _ string) (any, *apiError) {
	name, value := form.Get("AttributeName"), form.Get("AttributeValue")
	if name == "" {
		return nil, errInvalid("AttributeName is required")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("TopicArn"), func(t *Topic) {
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

func (srv *Server) tagResource(form url.Values, _ string) (any, *apiError) {
	tags := memberTags(form)
	if len(tags) == 0 {
		return nil, errInvalid("at least one tag is required")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *Topic) {
		if t.Tags == nil {
			t.Tags = map[string]string{}
		}
		for k, v := range tags {
			t.Tags[k] = v
		}
	}))
}

func (srv *Server) untagResource(form url.Values, _ string) (any, *apiError) {
	keys := awsquery.Members(form, "TagKeys", false)
	if len(keys) == 0 {
		return nil, errInvalid("at least one tag key is required")
	}
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *Topic) {
		for _, k := range keys {
			delete(t.Tags, k)
		}
	}))
}

func (srv *Server) listTagsForResource(form url.Values, _ string) (any, *apiError) {
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
func (srv *Server) addPermission(form url.Values, _ string) (any, *apiError) {
	if !srv.store.TopicExists(form.Get("TopicArn")) {
		return nil, errNotFound("topic does not exist: " + form.Get("TopicArn"))
	}
	return nil, nil
}

func (srv *Server) removePermission(form url.Values, _ string) (any, *apiError) {
	if !srv.store.TopicExists(form.Get("TopicArn")) {
		return nil, errNotFound("topic does not exist: " + form.Get("TopicArn"))
	}
	return nil, nil
}

func (srv *Server) putDataProtectionPolicy(form url.Values, _ string) (any, *apiError) {
	return nil, asErr(srv.store.UpdateTopic(form.Get("ResourceArn"), func(t *Topic) {
		t.DataProtectionPolicy = form.Get("DataProtectionPolicy")
	}))
}

type dataProtectionResult struct {
	DataProtectionPolicy string `xml:"DataProtectionPolicy"`
}

func (srv *Server) getDataProtectionPolicy(form url.Values, _ string) (any, *apiError) {
	t, err := srv.store.GetTopic(form.Get("ResourceArn"))
	if err != nil {
		return nil, asErr(err)
	}
	return dataProtectionResult{DataProtectionPolicy: t.DataProtectionPolicy}, nil
}
