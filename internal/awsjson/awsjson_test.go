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

func TestStr(t *testing.T) {
	p := map[string]any{"Name": "jobs", "Count": 3.0}
	if s := Str(p, "Name"); s != "jobs" {
		t.Errorf("Name = %q", s)
	}
	if s := Str(p, "Count"); s != "" { // wrong type
		t.Errorf("Count = %q, want empty", s)
	}
	if s := Str(p, "Missing"); s != "" {
		t.Errorf("Missing = %q, want empty", s)
	}
	if s := Str(nil, "Name"); s != "" {
		t.Errorf("nil map: %q, want empty", s)
	}
}

func TestBool(t *testing.T) {
	p := map[string]any{"Enabled": true, "Name": "jobs"}
	if b := Bool(p, "Enabled"); !b {
		t.Errorf("Enabled = %v", b)
	}
	if b := Bool(p, "Name"); b { // wrong type
		t.Errorf("Name = %v, want false", b)
	}
	if b := Bool(p, "Missing"); b {
		t.Errorf("Missing = %v, want false", b)
	}
}

func TestInt(t *testing.T) {
	p := map[string]any{"Retries": 3.0, "Fractional": 3.9, "Name": "jobs"}
	if v := Int(p, "Retries", -1); v != 3 {
		t.Errorf("Retries = %d", v)
	}
	if v := Int(p, "Fractional", -1); v != 3 { // truncates, doesn't round
		t.Errorf("Fractional = %d, want 3", v)
	}
	if v := Int(p, "Name", -1); v != -1 { // wrong type falls back to def
		t.Errorf("Name = %d, want -1", v)
	}
	if v := Int(p, "Missing", 42); v != 42 {
		t.Errorf("Missing = %d, want 42", v)
	}
}

func TestInt64(t *testing.T) {
	p := map[string]any{"Bytes": 1048576.0, "Name": "jobs"}
	if v := Int64(p, "Bytes", -1); v != 1048576 {
		t.Errorf("Bytes = %d", v)
	}
	if v := Int64(p, "Name", -1); v != -1 { // wrong type falls back to def
		t.Errorf("Name = %d, want -1", v)
	}
	if v := Int64(p, "Missing", 7); v != 7 {
		t.Errorf("Missing = %d, want 7", v)
	}
}

func TestBlob(t *testing.T) {
	p := map[string]any{
		"Valid":     "aGVsbG8=", // "hello"
		"Empty":     "",
		"Malformed": "not-base64!!",
		"WrongType": 3.0,
	}

	b, err := Blob(p, "Valid")
	if err != nil || string(b) != "hello" {
		t.Errorf("Valid: b = %q, err = %v", b, err)
	}

	b, err = Blob(p, "Empty")
	if err != nil || b != nil {
		t.Errorf("Empty: b = %v, err = %v, want nil, nil", b, err)
	}

	b, err = Blob(p, "Missing")
	if err != nil || b != nil {
		t.Errorf("Missing: b = %v, err = %v, want nil, nil", b, err)
	}

	b, err = Blob(p, "WrongType") // not a string at all: lenient, not an error
	if err != nil || b != nil {
		t.Errorf("WrongType: b = %v, err = %v, want nil, nil", b, err)
	}

	b, err = Blob(p, "Malformed")
	if err == nil || err.Code != "ValidationException" {
		t.Errorf("Malformed: b = %v, err = %v, want ValidationException", b, err)
	}
}

func TestStrMap(t *testing.T) {
	p := map[string]any{
		"Tags":      map[string]any{"env": "prod", "owner": "team", "count": 3.0},
		"WrongType": "not-a-map",
	}

	m := StrMap(p, "Tags")
	if len(m) != 2 || m["env"] != "prod" || m["owner"] != "team" {
		t.Errorf("Tags = %v, want {env:prod owner:team} (count dropped, wrong type)", m)
	}
	if _, ok := m["count"]; ok {
		t.Errorf("Tags[count] should have been dropped, non-string value")
	}

	if m := StrMap(p, "WrongType"); m != nil {
		t.Errorf("WrongType = %v, want nil", m)
	}
	if m := StrMap(p, "Missing"); m != nil {
		t.Errorf("Missing = %v, want nil", m)
	}
}

func TestStrs(t *testing.T) {
	p := map[string]any{
		"Names":     []any{"a", "b", 3.0, "c"},
		"WrongType": "not-a-list",
	}

	got := Strs(p, "Names")
	want := []string{"a", "b", "c"} // the non-string element is dropped
	if len(got) != len(want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if s := Strs(p, "WrongType"); s != nil {
		t.Errorf("WrongType = %v, want nil", s)
	}
	if s := Strs(p, "Missing"); s != nil {
		t.Errorf("Missing = %v, want nil", s)
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
