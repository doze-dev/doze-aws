package awsjson

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func TestAction(t *testing.T) {
	api := API{TargetPrefix: "AmazonSQS", JSONVersion: "1.0"}

	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")
	action, err := api.Action(r)
	if err != nil || action != "SendMessage" {
		t.Errorf("action = %q, err = %v", action, err)
	}

	for _, target := range []string{"", "AmazonSQS", "AmazonSQS.", "DynamoDB_20120810.GetItem"} {
		r := httptest.NewRequest("POST", "/", nil)
		if target != "" {
			r.Header.Set("X-Amz-Target", target)
		}
		if _, err := api.Action(r); err == nil {
			t.Errorf("target %q: want error", target)
		}
	}
}

func TestDecodeBody(t *testing.T) {
	var dst struct{ QueueName string }

	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"QueueName":"jobs"}`))
	if err := DecodeBody(r, &dst); err != nil || dst.QueueName != "jobs" {
		t.Errorf("dst = %+v, err = %v", dst, err)
	}

	// Empty body decodes as an empty object.
	r = httptest.NewRequest("POST", "/", nil)
	if err := DecodeBody(r, &dst); err != nil {
		t.Errorf("empty body: %v", err)
	}

	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"QueueName":`))
	if err := DecodeBody(r, &dst); err == nil || err.Code != "SerializationException" {
		t.Errorf("malformed body: err = %v", err)
	}
}

func TestWrite(t *testing.T) {
	api := API{TargetPrefix: "TrentService", JSONVersion: "1.1"}
	rec := httptest.NewRecorder()
	api.Write(rec, map[string]string{"KeyId": "abc"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-amz-json-1.1" {
		t.Errorf("content-type = %q", ct)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["KeyId"] != "abc" {
		t.Errorf("body = %s, err = %v", rec.Body.String(), err)
	}

	// nil result still writes a JSON object body.
	rec = httptest.NewRecorder()
	api.Write(rec, nil)
	if body := strings.TrimSpace(rec.Body.String()); body != "{}" {
		t.Errorf("nil result body = %q", body)
	}
}

func TestWriteError(t *testing.T) {
	api := API{TargetPrefix: "AmazonSQS", JSONVersion: "1.0"}
	rec := httptest.NewRecorder()
	api.WriteError(rec, awshttp.Errf(400, "QueueDoesNotExist", "no queue named %q", "jobs"))

	if rec.Code != 400 {
		t.Fatalf("status = %d", rec.Code)
	}
	if et := rec.Header().Get("x-amzn-ErrorType"); et != "QueueDoesNotExist" {
		t.Errorf("x-amzn-ErrorType = %q", et)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["__type"] != "QueueDoesNotExist" || !strings.Contains(out["message"], "jobs") {
		t.Errorf("body = %v", out)
	}
}
