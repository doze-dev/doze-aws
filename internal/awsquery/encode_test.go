package awsquery

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// The encoder is held to the decoders in this package and in modelcheck: a
// document that flattens must un-flatten to itself, because the services on
// the other end of a Step Functions aws-sdk call read it with exactly those.

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEncodeScalarsAndStructures(t *testing.T) {
	form := Encode("Publish", decodeJSON(t, `{"TopicArn":"arn:t","Message":"hi","Subject":"s",
	  "Nested":{"Deep":{"Leaf":"x"},"Flag":true,"Count":3}}`), EncodeOptions{})
	want := map[string]string{
		"Action": "Publish", "TopicArn": "arn:t", "Message": "hi", "Subject": "s",
		"Nested.Deep.Leaf": "x", "Nested.Flag": "true", "Nested.Count": "3",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("%s = %q, want %q (form %v)", k, form.Get(k), v, form)
		}
	}
	if len(form) != len(want) {
		t.Errorf("form has extra keys: %v", form)
	}
}

func TestEncodeListsRoundTripThroughMembers(t *testing.T) {
	form := Encode("UntagResource", decodeJSON(t, `{"TagKeys":["a","b"]}`), EncodeOptions{})
	if got := Members(form, "TagKeys", false); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Members = %v", got)
	}
	form = Encode("X", decodeJSON(t, `{"AttributeName":["All"]}`), EncodeOptions{Memberless: true})
	if got := Members(form, "AttributeName", true); !reflect.DeepEqual(got, []string{"All"}) {
		t.Errorf("memberless Members = %v", got)
	}
}

func TestEncodeListOfStructsRoundTripsThroughPairMap(t *testing.T) {
	form := Encode("TagResource", decodeJSON(t,
		`{"Tags":[{"Key":"env","Value":"dev"},{"Key":"team","Value":"a"}]}`), EncodeOptions{})
	got := PairMap(form, "Tags.member", "Key", "Value")
	if !reflect.DeepEqual(got, map[string]string{"env": "dev", "team": "a"}) {
		t.Errorf("PairMap = %v (form %v)", got, form)
	}
}

func TestEncodeMapsRoundTripThroughMessageAttrsAndPairMap(t *testing.T) {
	form := Encode("Publish", decodeJSON(t, `{"TopicArn":"arn:t","Message":"m",
	  "MessageAttributes":{"colour":{"DataType":"String","StringValue":"red"}},
	  "Attributes":{"DisplayName":"Orders"}}`), EncodeOptions{})
	attrs := MessageAttrs(form, "MessageAttributes.entry")
	if attrs["colour"].DataType != "String" || attrs["colour"].StringValue != "red" {
		t.Errorf("MessageAttrs = %+v (form %v)", attrs, form)
	}
	if got := PairMap(form, "Attributes.entry", "key", "value"); got["DisplayName"] != "Orders" {
		t.Errorf("PairMap = %v", got)
	}
	// And through modelcheck's rebuild, which is what the services validate
	// against: the map must come back as a map.
	tree := modelcheck.FromQuery(form)
	ma, _ := tree["MessageAttributes"].(map[string]any)
	colour, _ := ma["colour"].(map[string]any)
	if colour["DataType"] != "String" {
		t.Errorf("FromQuery rebuilt %v", tree)
	}
}

func TestResultLiftsXMLToJSON(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::000000000000:user/test</Arn>
    <UserId>AIDATEST</UserId>
    <Account>000000000000</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>r</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`)
	out, err := Result("GetCallerIdentity", body)
	if err != nil {
		t.Fatal(err)
	}
	if out["Account"] != "000000000000" || out["UserId"] != "AIDATEST" || len(out) != 3 {
		t.Errorf("Result = %v", out)
	}

	// Lists, maps and nested structures.
	body = []byte(`<ListTagsForResourceResponse><ListTagsForResourceResult>
	  <Tags><member><Key>a</Key><Value>1</Value></member><member><Key>b</Key><Value>2</Value></member></Tags>
	  <Attributes><entry><key>Owner</key><value>me</value></entry></Attributes>
	  <Subscription><Endpoint>e</Endpoint></Subscription>
	</ListTagsForResourceResult></ListTagsForResourceResponse>`)
	out, err = Result("ListTagsForResource", body)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"Tags":         []any{map[string]any{"Key": "a", "Value": "1"}, map[string]any{"Key": "b", "Value": "2"}},
		"Attributes":   map[string]any{"Owner": "me"},
		"Subscription": map[string]any{"Endpoint": "e"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("Result = %#v\nwant %#v", out, want)
	}

	// Result-less action: empty object, not an error.
	if out, err = Result("DeleteTopic", []byte(`<DeleteTopicResponse><ResponseMetadata/></DeleteTopicResponse>`)); err != nil || len(out) != 0 {
		t.Errorf("result-less = %v, %v", out, err)
	}
}

func TestErrorDecodesTheEnvelope(t *testing.T) {
	code, msg, ok := Error([]byte(`<ErrorResponse xmlns="x"><Error><Type>Sender</Type><Code>NotFound</Code><Message>Topic does not exist</Message></Error><RequestId>r</RequestId></ErrorResponse>`))
	if !ok || code != "NotFound" || msg != "Topic does not exist" {
		t.Errorf("Error = %q %q %v", code, msg, ok)
	}
	if _, _, ok := Error([]byte(`<PublishResponse/>`)); ok {
		t.Error("a success envelope is not an error")
	}
}
