package stepfunctions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

// The optimized DynamoDB and EventBridge integrations and the generic
// aws-sdk family, against stub peers that answer on the wire the way the
// real services do. Pinned here: the request each protocol produces (the
// JSON target, the Query form, the S3 path), the API-shaped result, and the
// error NAMES — DynamoDB.ConditionalCheckFailedException for the optimized
// spelling, Ssm.ParameterNotFoundException with the aws-sdk suffix rule.

// stubPeers is one httptest server standing in for every service, keyed
// by the gateway's own names, with a tiny in-memory DynamoDB and S3.
type stubPeers struct {
	mu    sync.Mutex
	items map[string]json.RawMessage // DynamoDB: pk → item
	blobs map[string][]byte          // S3: bucket/key → body
	forms []url.Values               // every Query-protocol form received
	seen  []string                   // every X-Amz-Target received
}

func newStubPeers(t *testing.T) (peers.Directory, *stubPeers) {
	t.Helper()
	st := &stubPeers{items: map[string]json.RawMessage{}, blobs: map[string][]byte{}}
	ts := httptest.NewServer(http.HandlerFunc(st.serve))
	t.Cleanup(ts.Close)
	ep := peers.Endpoint{Client: ts.Client(), BaseURL: ts.URL}
	dir := peers.Static{}
	for _, svc := range []string{"dynamodb", "eventbridge", "ssm", "sns", "sts", "s3"} {
		dir[svc] = ep
	}
	return dir, st
}

func jsonErr(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("x-amzn-ErrorType", typ)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"__type": typ, "message": msg})
}

func (st *stubPeers) serve(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	defer st.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		st.seen = append(st.seen, target)
		var p map[string]any
		json.Unmarshal(body, &p)
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		switch target {
		case "DynamoDB_20120810.PutItem":
			item, _ := p["Item"].(map[string]any)
			pk, _ := item["pk"].(map[string]any)
			key, _ := pk["S"].(string)
			if _, exists := st.items[key]; exists && p["ConditionExpression"] == "attribute_not_exists(pk)" {
				// The real service qualifies __type with its namespace;
				// the Catch must still see the bare code.
				w.WriteHeader(400)
				w.Write([]byte(`{"__type":"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException","message":"The conditional request failed"}`))
				return
			}
			st.items[key], _ = json.Marshal(item)
			w.Write([]byte(`{}`))
		case "DynamoDB_20120810.GetItem":
			k, _ := p["Key"].(map[string]any)
			pk, _ := k["pk"].(map[string]any)
			key, _ := pk["S"].(string)
			if p["TableName"] != "orders" {
				jsonErr(w, 400, "ResourceNotFoundException", "Requested resource not found")
				return
			}
			if item, ok := st.items[key]; ok {
				w.Write([]byte(`{"Item":` + string(item) + `}`))
				return
			}
			w.Write([]byte(`{}`))
		case "AWSEvents.PutEvents":
			entries, _ := p["Entries"].([]any)
			var out []any
			for i, e := range entries {
				em, _ := e.(map[string]any)
				if _, isString := em["Detail"].(string); !isString {
					jsonErr(w, 400, "ValidationException", "Detail must be a string")
					return
				}
				out = append(out, map[string]any{"EventId": "evt-" + string(rune('1'+i))})
			}
			json.NewEncoder(w).Encode(map[string]any{"Entries": out, "FailedEntryCount": 0})
		case "AmazonSSM.GetParameter":
			if p["Name"] != "/app/colour" {
				jsonErr(w, 400, "ParameterNotFound", "")
				return
			}
			w.Write([]byte(`{"Parameter":{"Name":"/app/colour","Type":"String","Value":"red","Version":1}}`))
		default:
			jsonErr(w, 400, "UnknownOperationException", target)
		}
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		form, _ := url.ParseQuery(string(body))
		st.forms = append(st.forms, form)
		w.Header().Set("Content-Type", "text/xml")
		switch form.Get("Action") {
		case "Publish":
			if !strings.HasSuffix(form.Get("TopicArn"), ":orders") {
				w.WriteHeader(404)
				w.Write([]byte(`<ErrorResponse><Error><Type>Sender</Type><Code>NotFound</Code><Message>Topic does not exist</Message></Error></ErrorResponse>`))
				return
			}
			w.Write([]byte(`<PublishResponse><PublishResult><MessageId>msg-7</MessageId></PublishResult></PublishResponse>`))
		case "GetCallerIdentity":
			w.Write([]byte(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>arn:aws:iam::000000000000:user/test</Arn><UserId>AIDATEST</UserId><Account>000000000000</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`))
		default:
			w.WriteHeader(400)
			w.Write([]byte(`<ErrorResponse><Error><Code>InvalidAction</Code><Message>no</Message></Error></ErrorResponse>`))
		}
		return
	}
	// S3, path-style: /bucket/key or /bucket?list-type=2.
	st.serveS3(w, r, body)
}

func (st *stubPeers) serveS3(w http.ResponseWriter, r *http.Request, body []byte) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket != "docs" {
		w.WriteHeader(404)
		w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`))
		return
	}
	switch {
	case r.Method == "PUT":
		st.blobs[bucket+"/"+key] = body
		w.Header().Set("ETag", `"etag-1"`)
		w.WriteHeader(200)
	case r.Method == "GET" && key == "" && r.URL.Query().Get("list-type") == "2":
		prefix := r.URL.Query().Get("prefix")
		var xml strings.Builder
		xml.WriteString(`<ListBucketResult><Name>docs</Name><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated>`)
		n := 0
		for k, b := range st.blobs {
			k = strings.TrimPrefix(k, bucket+"/")
			if strings.HasPrefix(k, prefix) {
				n++
				xml.WriteString(`<Contents><Key>` + k + `</Key><Size>` + itoa(len(b)) + `</Size><ETag>"etag-1"</ETag><LastModified>2026-01-01T00:00:00.000Z</LastModified></Contents>`)
			}
		}
		xml.WriteString(`</ListBucketResult>`)
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(strings.Replace(xml.String(), "<KeyCount>0</KeyCount>", "<KeyCount>"+itoa(n)+"</KeyCount>", 1)))
	case r.Method == "GET" || r.Method == "HEAD":
		b, ok := st.blobs[bucket+"/"+key]
		if !ok {
			w.WriteHeader(404)
			if r.Method == "GET" {
				w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
			}
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"etag-1"`)
		w.Header().Set("x-amz-meta-owner", "sfn")
		w.Header().Set("Content-Length", itoa(len(b)))
		if r.Method == "GET" {
			w.Write(b)
		}
	case r.Method == "DELETE":
		delete(st.blobs, bucket+"/"+key)
		w.WriteHeader(204)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func sdkServer(t *testing.T) (*Server, *stubPeers) {
	t.Helper()
	dir, st := newStubPeers(t)
	s := newTestServerPeers(t, t.TempDir(), &testClock{now: time.Now()}, dir)
	t.Cleanup(func() { s.Close() })
	return s, st
}

func TestOptimizedDynamoDBRoundTripAndConditionalFailure(t *testing.T) {
	s, st := sdkServer(t)
	createMachine(t, s, "ddb", `{"StartAt":"Put","States":{
	  "Put":{"Type":"Task","Resource":"arn:aws:states:::dynamodb:putItem",
	    "Parameters":{"TableName":"orders","Item":{"pk":{"S.$":"$.id"},"total":{"N":"42"}},
	                  "ConditionExpression":"attribute_not_exists(pk)"},
	    "ResultPath":null,
	    "Catch":[{"ErrorEquals":["DynamoDB.ConditionalCheckFailedException"],"ResultPath":"$.dup","Next":"Get"}],
	    "Next":"Get"},
	  "Get":{"Type":"Task","Resource":"arn:aws:states:::dynamodb:getItem",
	    "Parameters":{"TableName":"orders","Key":{"pk":{"S.$":"$.id"}}},
	    "ResultPath":"$.got","End":true}}}`)
	startExecInput(t, s, "ddb", "first", `{"id":"o-1"}`)
	out := waitSucceeded(t, s, "ddb", "first")
	var v struct {
		Got struct {
			Item map[string]map[string]string
		} `json:"got"`
		Dup *struct{ Error, Cause string } `json:"dup"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.Got.Item["total"]["N"] != "42" || v.Dup != nil {
		t.Errorf("first run: getItem should answer {Item:...} in DynamoDB JSON, got %s", out)
	}
	if st.seen[0] != "DynamoDB_20120810.PutItem" || st.seen[1] != "DynamoDB_20120810.GetItem" {
		t.Errorf("targets = %v", st.seen)
	}

	// Same key again: the condition fails, and the Catch names it.
	startExecInput(t, s, "ddb", "second", `{"id":"o-1"}`)
	out = waitSucceeded(t, s, "ddb", "second")
	json.Unmarshal([]byte(out), &v)
	if v.Dup == nil || v.Dup.Error != "DynamoDB.ConditionalCheckFailedException" || !strings.Contains(v.Dup.Cause, "conditional request failed") {
		t.Errorf("Catch should receive DynamoDB.ConditionalCheckFailedException, got %s", out)
	}

	// An unknown table, uncaught, fails the execution under its API code.
	createMachine(t, s, "ddb-missing", `{"StartAt":"Get","States":{"Get":{"Type":"Task",
	  "Resource":"arn:aws:states:::dynamodb:getItem",
	  "Parameters":{"TableName":"nonesuch","Key":{"pk":{"S":"x"}}},"End":true}}}`)
	startExec(t, s, "ddb-missing", "run")
	if e := waitFailed(t, s, "ddb-missing", "run"); e.Error != "DynamoDB.ResourceNotFoundException" {
		t.Errorf("error = %q, want DynamoDB.ResourceNotFoundException", e.Error)
	}
}

func TestOptimizedPutEventsSerialisesDetail(t *testing.T) {
	s, _ := sdkServer(t)
	createMachine(t, s, "evt", `{"StartAt":"Emit","States":{"Emit":{"Type":"Task",
	  "Resource":"arn:aws:states:::events:putEvents",
	  "Parameters":{"Entries":[{"Source":"orders","DetailType":"OrderPlaced","EventBusName":"default",
	                            "Detail":{"orderId.$":"$.id"}}]},"End":true}}}`)
	startExecInput(t, s, "evt", "run", `{"id":"o-9"}`)
	out := waitSucceeded(t, s, "evt", "run")
	var v struct {
		Entries          []struct{ EventId string }
		FailedEntryCount int
	}
	json.Unmarshal([]byte(out), &v)
	if len(v.Entries) != 1 || v.Entries[0].EventId == "" || v.FailedEntryCount != 0 {
		t.Errorf("putEvents result should be the API response, got %s", out)
	}
}

func TestSDKSSMGetParameterAndErrorSuffix(t *testing.T) {
	s, _ := sdkServer(t)
	createMachine(t, s, "ssm", `{"StartAt":"Get","States":{"Get":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:ssm:getParameter",
	  "Parameters":{"Name.$":"$.name"},"OutputPath":"$.Parameter.Value","End":true}}}`)
	startExecInput(t, s, "ssm", "hit", `{"name":"/app/colour"}`)
	if out := waitSucceeded(t, s, "ssm", "hit"); out != `"red"` {
		t.Errorf("output = %s, want \"red\"", out)
	}
	// SSM's code is ParameterNotFound; the aws-sdk convention adds the
	// suffix the API reference leaves off.
	startExecInput(t, s, "ssm", "miss", `{"name":"/app/nonesuch"}`)
	if e := waitFailed(t, s, "ssm", "miss"); e.Error != "Ssm.ParameterNotFoundException" {
		t.Errorf("error = %q, want Ssm.ParameterNotFoundException", e.Error)
	}
}

func TestSDKSNSPublishThroughTheQueryEncoder(t *testing.T) {
	s, st := sdkServer(t)
	createMachine(t, s, "sns", `{"StartAt":"Pub","States":{"Pub":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:sns:publish",
	  "Parameters":{"TopicArn":"arn:aws:sns:us-east-1:000000000000:orders","Message.$":"$.text",
	    "MessageAttributes":{"colour":{"DataType":"String","StringValue":"red"}}},
	  "End":true}}}`)
	startExecInput(t, s, "sns", "run", `{"text":"shipped"}`)
	if out := waitSucceeded(t, s, "sns", "run"); out != `{"MessageId":"msg-7"}` {
		t.Errorf("output = %s", out)
	}
	form := st.forms[0]
	for k, want := range map[string]string{
		"Action": "Publish", "Message": "shipped",
		"MessageAttributes.entry.1.Name":              "colour",
		"MessageAttributes.entry.1.Value.DataType":    "String",
		"MessageAttributes.entry.1.Value.StringValue": "red",
	} {
		if form.Get(k) != want {
			t.Errorf("form %s = %q, want %q (form %v)", k, form.Get(k), want, form)
		}
	}
	createMachine(t, s, "sns-missing", `{"StartAt":"Pub","States":{"Pub":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:sns:publish",
	  "Parameters":{"TopicArn":"arn:aws:sns:us-east-1:000000000000:nonesuch","Message":"x"},"End":true}}}`)
	startExec(t, s, "sns-missing", "run")
	if e := waitFailed(t, s, "sns-missing", "run"); e.Error != "Sns.NotFoundException" {
		t.Errorf("error = %q, want Sns.NotFoundException", e.Error)
	}
}

func TestSDKSTSGetCallerIdentityLiftsXML(t *testing.T) {
	s, _ := sdkServer(t)
	createMachine(t, s, "sts", `{"StartAt":"Who","States":{"Who":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:sts:getCallerIdentity","End":true}}}`)
	startExec(t, s, "sts", "run")
	var v map[string]string
	json.Unmarshal([]byte(waitSucceeded(t, s, "sts", "run")), &v)
	if v["Account"] != "000000000000" || v["UserId"] != "AIDATEST" || !strings.HasPrefix(v["Arn"], "arn:aws:iam::") {
		t.Errorf("result = %v", v)
	}
}

func TestSDKS3PutGetList(t *testing.T) {
	s, _ := sdkServer(t)
	createMachine(t, s, "s3", `{"StartAt":"Put","States":{
	  "Put":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:s3:putObject",
	    "Parameters":{"Bucket":"docs","Key":"reports/a.txt","Body.$":"$.text","ContentType":"text/plain"},
	    "ResultPath":"$.put","Next":"Get"},
	  "Get":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:s3:getObject",
	    "Parameters":{"Bucket":"docs","Key":"reports/a.txt"},"ResultPath":"$.get","Next":"Head"},
	  "Head":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:s3:headObject",
	    "Parameters":{"Bucket":"docs","Key":"reports/a.txt"},"ResultPath":"$.head","Next":"List"},
	  "List":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:s3:listObjectsV2",
	    "Parameters":{"Bucket":"docs","Prefix":"reports/"},"ResultPath":"$.list","End":true}}}`)
	startExecInput(t, s, "s3", "run", `{"text":"hello"}`)
	out := waitSucceeded(t, s, "s3", "run")
	var v struct {
		Put struct{ ETag string }
		Get struct {
			Body, ContentType string
			ContentLength     int
			Metadata          map[string]string
		}
		Head struct{ ContentLength int }
		List struct {
			KeyCount    int
			IsTruncated bool
			Contents    []struct {
				Key  string
				Size int
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.Put.ETag != `"etag-1"` || v.Get.Body != "hello" || v.Get.ContentType != "text/plain" ||
		v.Get.ContentLength != 5 || v.Get.Metadata["owner"] != "sfn" || v.Head.ContentLength != 5 {
		t.Errorf("put/get/head shapes wrong: %s", out)
	}
	if v.List.KeyCount != 1 || v.List.IsTruncated || len(v.List.Contents) != 1 ||
		v.List.Contents[0].Key != "reports/a.txt" || v.List.Contents[0].Size != 5 {
		t.Errorf("listObjectsV2 shape wrong: %s", out)
	}

	createMachine(t, s, "s3-missing", `{"StartAt":"Get","States":{"Get":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:s3:getObject",
	  "Parameters":{"Bucket":"docs","Key":"nonesuch"},"End":true}}}`)
	startExec(t, s, "s3-missing", "run")
	if e := waitFailed(t, s, "s3-missing", "run"); e.Error != "S3.NoSuchKeyException" {
		t.Errorf("error = %q, want S3.NoSuchKeyException (the suffix rule)", e.Error)
	}
}

func TestSDKUnwiredPeerIsTaskFailed(t *testing.T) {
	s := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})
	defer s.Close()
	createMachine(t, s, "nopeer", `{"StartAt":"T","States":{"T":{"Type":"Task",
	  "Resource":"arn:aws:states:::aws-sdk:kms:encrypt","Parameters":{"KeyId":"k","Plaintext":"x"},"End":true}}}`)
	startExec(t, s, "nopeer", "run")
	if e := waitFailed(t, s, "nopeer", "run"); e.Error != "States.TaskFailed" || !strings.Contains(e.Cause, "no kms peer") {
		t.Errorf("error = %q (%s), want States.TaskFailed naming the missing peer", e.Error, e.Cause)
	}
}

// Refusals happen at create, not at the first execution: the analyser
// accepts what AWS accepts, and createStateMachine says what this build
// cannot call.
func TestSDKUnknownServiceOrActionRefusedAtCreate(t *testing.T) {
	s := newTestServer(t, t.TempDir(), &testClock{now: time.Now()})
	defer s.Close()
	for _, tc := range []struct{ resource, want string }{
		{"arn:aws:states:::aws-sdk:ec2:describeInstances", "aws-sdk:ec2 is not a service this build calls"},
		{"arn:aws:states:::aws-sdk:s3:selectObjectContent", "aws-sdk:s3:selectObjectContent is not an action this build calls (getObject, putObject"},
		{"arn:aws:states:::aws-sdk:apigateway:getRestApis", "aws-sdk:apigateway is not a service this build calls"},
		{"arn:aws:states:::aws-sdk:dynamodb:GetItem", "lowercase-initial"},
		{"arn:aws:states:::dynamodb:query", "dynamodb:query integration is not one this build calls"},
	} {
		def := `{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"` + tc.resource + `","End":true}}}`
		res, aerr := s.validateDefinition(context.Background(), map[string]any{"definition": def})
		if aerr != nil || res.(map[string]any)["result"] != "OK" {
			t.Errorf("%s: ValidateStateMachineDefinition should accept (AWS does): %v %v", tc.resource, res, aerr)
		}
		_, aerr = s.createStateMachine(context.Background(), map[string]any{
			"name": "m", "definition": def, "roleArn": "arn:aws:iam::000000000000:role/StepFunctions",
		})
		if aerr == nil || aerr.Code != "UnsupportedOperationException" || !strings.Contains(aerr.Message, tc.want) {
			t.Errorf("%s: create should refuse mentioning %q, got %v", tc.resource, tc.want, aerr)
		}
	}
	// And the supported spellings create.
	for _, resource := range []string{
		"arn:aws:states:::aws-sdk:dynamodb:getItem", "arn:aws:states:::aws-sdk:sfn:startExecution",
		"arn:aws:states:::aws-sdk:sts:getCallerIdentity", "arn:aws:states:::aws-sdk:lambda:invoke",
		"arn:aws:states:::events:putEvents.waitForTaskToken", "arn:aws:states:::dynamodb:updateItem",
	} {
		if _, err := ParseResource(resource); err != nil {
			t.Errorf("%s: %v", resource, err)
		}
	}
}
