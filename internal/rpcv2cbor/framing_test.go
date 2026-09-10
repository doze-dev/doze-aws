package rpcv2cbor

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		path    string
		svc, op string
		ok      bool
	}{
		{"/service/GraniteServiceVersion20100801/operation/PutMetricData",
			"GraniteServiceVersion20100801", "PutMetricData", true},
		// A fronting router may serve the stack under a prefix.
		{"/cloudwatch/service/Granite/operation/ListMetrics", "Granite", "ListMetrics", true},
		{"service/Granite/operation/ListMetrics", "Granite", "ListMetrics", true},
		{"/service/Granite/operation/", "", "", false},
		{"/service//operation/X", "", "", false},
		{"/service/Granite/operation", "", "", false},
		{"/", "", "", false},
		{"", "", "", false},
		{"/2015-03-31/functions/f/invocations", "", "", false},
		// A namespaced shape id would give one operation two spellings.
		{"/service/Granite/operation/com.amazonaws.cloudwatch#ListMetrics", "", "", false},
	}
	for _, c := range cases {
		svc, op, ok := ParsePath(c.path)
		if ok != c.ok || svc != c.svc || op != c.op {
			t.Errorf("ParsePath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.path, svc, op, ok, c.svc, c.op, c.ok)
		}
	}
}

func TestIsRequest(t *testing.T) {
	req := func(path, ct, proto string) *http.Request {
		r := httptest.NewRequest("POST", path, nil)
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		if proto != "" {
			r.Header.Set(ProtocolHeader, proto)
		}
		return r
	}
	good := "/service/Granite/operation/PutMetricData"
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"the real shape", req(good, ContentType, ProtocolID), true},
		{"charset parameter", req(good, ContentType+"; charset=utf-8", ProtocolID), true},
		{"no protocol header", req(good, ContentType, ""), true},
		{"no content type", req(good, "", ""), true},
		{"wrong protocol header", req(good, ContentType, "rpc-v2-json"), false},
		{"json content type", req(good, "application/x-amz-json-1.0", ""), false},
		{"not an rpc path", req("/", ContentType, ProtocolID), false},
	}
	for _, c := range cases {
		if got := IsRequest(c.r); got != c.want {
			t.Errorf("%s: IsRequest = %v, want %v", c.name, got, c.want)
		}
	}
}

// An error keeps the modern name in the body and puts the legacy Query code
// in a header — and only when the caller asked for it. Swapping those breaks
// the modern client to serve the old one.
func TestWriteErrorSplitsModernAndLegacyCodes(t *testing.T) {
	e := awshttp.Errf(400, "InvalidParameterValueException", "bad value")

	w := httptest.NewRecorder()
	WriteError(w, e, "InvalidParameterValue", true)
	if w.Code != 400 {
		t.Errorf("status = %d", w.Code)
	}
	if got := w.Header().Get(QueryErrorHeader); got != "InvalidParameterValue;Sender" {
		t.Errorf("%s = %q", QueryErrorHeader, got)
	}
	if got := w.Header().Get(ProtocolHeader); got != ProtocolID {
		t.Errorf("the response must echo the protocol, got %q", got)
	}
	body, err := DecodeMap(w.Body.Bytes())
	if err != nil {
		t.Fatalf("the error body is not decodable CBOR: %v", err)
	}
	if body["__type"] != "InvalidParameterValueException" {
		t.Errorf("__type = %v, want the modern name", body["__type"])
	}
	if body["message"] != "bad value" {
		t.Errorf("message = %v", body["message"])
	}

	// A caller that did not opt in gets no legacy header at all.
	w2 := httptest.NewRecorder()
	WriteError(w2, e, "InvalidParameterValue", false)
	if got := w2.Header().Get(QueryErrorHeader); got != "" {
		t.Errorf("legacy code sent unasked: %q", got)
	}
}

// A Unit-returning operation still answers with a decodable empty structure;
// a client will not accept no body at all.
func TestWriteNilIsAnEmptyMap(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, nil)
	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
	got, err := DecodeMap(w.Body.Bytes())
	if err != nil {
		t.Fatalf("empty result did not decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want an empty map, got %#v", got)
	}
}

// Round-tripping is what says the encoder and decoder agree; the struct case
// is what says the `json` tags are being honoured.
func TestMarshalRoundTrip(t *testing.T) {
	type dim struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	}
	type datum struct {
		MetricName string    `json:"MetricName"`
		Dimensions []dim     `json:"Dimensions,omitempty"`
		Value      float64   `json:"Value"`
		Count      int       `json:"Count,omitempty"`
		Stamp      time.Time `json:"Timestamp"`
		Skipped    string    `json:"-"`
		Blob       []byte    `json:"Blob,omitempty"`
	}
	in := datum{
		MetricName: "Checkouts",
		Dimensions: []dim{{Name: "Stage", Value: "prod"}},
		Value:      1.5,
		Stamp:      time.Unix(1700000000, 0).UTC(),
		Skipped:    "never on the wire",
		Blob:       []byte{1, 2, 3},
	}
	raw, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMap(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"MetricName": "Checkouts",
		"Dimensions": []any{map[string]any{"Name": "Stage", "Value": "prod"}},
		"Value":      1.5,
		"Timestamp":  time.Unix(1700000000, 0).UTC(),
		"Blob":       "AQID",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if _, ok := got["Count"]; ok {
		t.Error("omitempty did not drop a zero field")
	}
	if _, ok := got["Skipped"]; ok {
		t.Error(`a json:"-" field reached the wire`)
	}
}

// Encoding is deterministic, which is what makes comparing two encodings a
// meaningful test rather than map-iteration roulette.
func TestMarshalIsDeterministic(t *testing.T) {
	v := map[string]any{"b": 2, "a": 1, "c": map[string]any{"z": 1, "y": 2}}
	first, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("Marshal is not deterministic")
		}
	}
}
