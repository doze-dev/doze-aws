package awsquery

import (
	"encoding/xml"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func TestParamsMergesQueryAndForm(t *testing.T) {
	body := strings.NewReader("Action=Publish&Message=hi+there")
	r := httptest.NewRequest("POST", "/?Version=2010-03-31", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	vals, err := Params(r)
	if err != nil {
		t.Fatal(err)
	}
	if vals.Get("Action") != "Publish" || vals.Get("Message") != "hi there" || vals.Get("Version") != "2010-03-31" {
		t.Errorf("vals = %v", vals)
	}
}

func TestParamsGetOnly(t *testing.T) {
	r := httptest.NewRequest("GET", "/?Action=ListTopics", nil)
	vals, err := Params(r)
	if err != nil {
		t.Fatal(err)
	}
	if vals.Get("Action") != "ListTopics" {
		t.Errorf("vals = %v", vals)
	}
}

func TestMembers(t *testing.T) {
	vals := url.Values{
		"AttributeName.member.1": {"All"},
		"AttributeName.member.2": {"Policy"},
		"Plain.1":                {"a"},
		"Plain.2":                {"b"},
		"Plain.4":                {"skipped — numbering must be contiguous"},
	}
	if got := Members(vals, "AttributeName", false); len(got) != 2 || got[0] != "All" || got[1] != "Policy" {
		t.Errorf("member list = %v", got)
	}
	if got := Members(vals, "Plain", true); len(got) != 2 || got[1] != "b" {
		t.Errorf("memberless list = %v", got)
	}
	if got := Members(vals, "Missing", false); got != nil {
		t.Errorf("missing list = %v", got)
	}
}

func TestPairMap(t *testing.T) {
	vals := url.Values{
		"Tag.1.Key":   {"env"},
		"Tag.1.Value": {"dev"},
		"Tag.2.Key":   {"team"},
		"Tag.2.Value": {"core"},
	}
	got := PairMap(vals, "Tag", "Key", "Value")
	if len(got) != 2 || got["env"] != "dev" || got["team"] != "core" {
		t.Errorf("pair map = %v", got)
	}
	if got := PairMap(vals, "Attribute", "Name", "Value"); got != nil {
		t.Errorf("missing list = %v", got)
	}
}

func TestMessageAttrs(t *testing.T) {
	vals := url.Values{
		"MessageAttribute.1.Name":              {"color"},
		"MessageAttribute.1.Value.DataType":    {"String"},
		"MessageAttribute.1.Value.StringValue": {"red"},
		"MessageAttribute.2.Name":              {"blob"},
		"MessageAttribute.2.Value.DataType":    {"Binary"},
		"MessageAttribute.2.Value.BinaryValue": {"aGk="},
	}
	got := MessageAttrs(vals, "MessageAttribute")
	if len(got) != 2 {
		t.Fatalf("attrs = %v", got)
	}
	if a := got["color"]; a.DataType != "String" || a.StringValue != "red" {
		t.Errorf("color = %+v", a)
	}
	if a := got["blob"]; a.DataType != "Binary" || string(a.BinaryValue) != "hi" {
		t.Errorf("blob = %+v", a)
	}
	if got := MessageAttrs(vals, "MessageAttributes.entry"); got != nil {
		t.Errorf("missing attrs = %v", got)
	}
}

func TestWriteResultEmptyResultElement(t *testing.T) {
	api := API{XMLNS: "ns", EmptyResult: true}
	rec := httptest.NewRecorder()
	api.WriteResult(rec, "TagResource", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "<TagResourceResult></TagResourceResult>") {
		t.Errorf("nil result must render an empty Result element:\n%s", body)
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), new(any)); err != nil {
		t.Errorf("response is not well-formed XML: %v\n%s", err, body)
	}
}

type testResult struct {
	Name  string
	Count int
}

func TestWriteResultEnvelope(t *testing.T) {
	api := API{XMLNS: "https://example.amazonaws.com/doc/2011-06-15/"}
	rec := httptest.NewRecorder()
	api.WriteResult(rec, "DescribeThing", testResult{Name: "a<b", Count: 3})

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/xml" {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<DescribeThingResponse xmlns="https://example.amazonaws.com/doc/2011-06-15/">`,
		"<DescribeThingResult>",
		"<Name>a&lt;b</Name>",
		"<Count>3</Count>",
		"</DescribeThingResult>",
		"<ResponseMetadata><RequestId>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	// The whole document must be well-formed XML.
	if err := xml.Unmarshal(rec.Body.Bytes(), new(any)); err != nil {
		t.Errorf("response is not well-formed XML: %v\n%s", err, body)
	}
}

func TestWriteResultNoResult(t *testing.T) {
	api := API{XMLNS: "ns"}
	rec := httptest.NewRecorder()
	api.WriteResult(rec, "DeleteThing", nil)
	body := rec.Body.String()
	if strings.Contains(body, "DeleteThingResult") {
		t.Errorf("nil result must omit the Result element:\n%s", body)
	}
	if !strings.Contains(body, "<DeleteThingResponse") || !strings.Contains(body, "<RequestId>") {
		t.Errorf("envelope incomplete:\n%s", body)
	}
}

func TestWriteError(t *testing.T) {
	api := API{XMLNS: "ns"}
	rec := httptest.NewRecorder()
	api.WriteError(rec, awshttp.Errf(400, "ValidationError", "bad <thing>"))

	if rec.Code != 400 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<ErrorResponse", "<Type>Sender</Type>", "<Code>ValidationError</Code>",
		"<Message>bad &lt;thing&gt;</Message>", "<RequestId>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}
