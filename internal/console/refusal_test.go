package console

import "testing"

// A refusal's code can sit in four places on the wire, and the console has to
// read all four. The first two worked; S3's bare <Error> root and the
// code-in-a-header shape of Lambda and API Gateway did not, so every refusal
// from those services rendered as raw XML or as a message with no code.
func TestParseRefusalReadsEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body, header string
		code, msg    string
	}{
		{"JSON with __type", 400, `{"__type":"com.amazon.coral.service#ValidationException","message":"bad value"}`, "",
			"ValidationException", "bad value"},
		{"JSON with a capital Message", 400, `{"__type":"InvalidRequestException","Message":"nope"}`, "",
			"InvalidRequestException", "nope"},
		{"JSON, code in the header (Lambda)", 404,
			`{"Type":"User","message":"Function not found: arn:aws:lambda:::function:x"}`, "ResourceNotFoundException",
			"ResourceNotFoundException", "Function not found: arn:aws:lambda:::function:x"},
		{"JSON, code in the header with a URL after it", 400, `{"message":"too long"}`,
			"BadRequestException:http://internal.amazon.com/coral/com.amazonaws.apigateway/", "BadRequestException", "too long"},
		{"XML wrapped in ErrorResponse (Query)", 400,
			`<ErrorResponse><Error><Type>Sender</Type><Code>InvalidParameterValue</Code><Message>bad</Message></Error></ErrorResponse>`, "",
			"InvalidParameterValue", "bad"},
		{"XML with Error as the root (S3)", 409,
			`<?xml version="1.0"?><Error><Code>BucketNotEmpty</Code><Message>The bucket you tried to delete is not empty</Message><RequestId>r</RequestId></Error>`, "",
			"BucketNotEmpty", "The bucket you tried to delete is not empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRefusal(tc.status, tc.body, tc.header)
			if got == nil {
				t.Fatal("not read at all")
			}
			if got.Code != tc.code || got.Message != tc.msg {
				t.Errorf("got code %q message %q, want %q %q", got.Code, got.Message, tc.code, tc.msg)
			}
		})
	}
	if parseRefusal(200, `{"__type":"X"}`, "") != nil {
		t.Error("a 200 is not a refusal")
	}
	if parseRefusal(500, "", "X") != nil {
		t.Error("an empty body is not a refusal, whatever the header says")
	}
}
