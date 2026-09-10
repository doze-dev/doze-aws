package rpcv2cbor

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The one test that matters most: the exact bytes aws-sdk-go-v2 put on the
// wire for PutMetricData, gzipped as it sent them, decoded to the same shape
// the JSON wire produces for the same call.
//
// Both fixtures were captured from the real clients (see the package doc), so
// this is a statement about what the SDKs do, not about what the spec allows.
func TestDecodeCapturedSDKBody(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "putmetricdata_cbor.cbor"))
	if err != nil {
		t.Fatal(err)
	}
	// Still gzipped on disk: that is what arrived.
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		t.Fatal("the fixture is not gzipped — recapture it, the compression is the point")
	}

	got, err := DecodeMap(body)
	if err != nil {
		t.Fatalf("decoding a real SDK body failed: %v", err)
	}

	want := map[string]any{
		"Namespace": "Shop",
		"MetricData": []any{map[string]any{
			"MetricName":        "Checkouts",
			"Unit":              "Count",
			"Value":             1.5,
			"StorageResolution": float64(1),
			"Dimensions": []any{
				map[string]any{"Name": "FunctionName", "Value": "checkout"},
				map[string]any{"Name": "Stage", "Value": "prod"},
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decode mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// The two wires must produce the same Go values, or "one handler set over one
// shape" is not true and every handler needs to know where its input came from.
func TestCBORAndJSONAgree(t *testing.T) {
	cbor, err := os.ReadFile(filepath.Join("testdata", "putmetricdata_cbor.cbor"))
	if err != nil {
		t.Fatal(err)
	}
	fromCBOR, err := DecodeMap(cbor)
	if err != nil {
		t.Fatal(err)
	}

	var jsonFixture struct {
		BodyRaw string `json:"body_raw"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "putmetricdata_json.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &jsonFixture); err != nil {
		t.Fatal(err)
	}
	var fromJSON map[string]any
	if err := json.Unmarshal([]byte(jsonFixture.BodyRaw), &fromJSON); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fromCBOR, fromJSON) {
		t.Errorf("the two wires decode differently — handlers would need to know which\n"+
			"cbor: %#v\njson: %#v", fromCBOR, fromJSON)
	}
}

// Indefinite lengths are what the Go SDK actually emits, so they are not an
// edge case here — they are the common path. Definite lengths are the ones
// that need proving, since nothing observed produces them yet.
func TestDecodeScalarsAndContainers(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want any
	}{
		{"uint immediate", []byte{0x01}, float64(1)},
		{"uint one byte", []byte{0x18, 0xff}, float64(255)},
		{"uint two byte", []byte{0x19, 0x01, 0x00}, float64(256)},
		{"uint four byte", []byte{0x1a, 0x00, 0x01, 0x00, 0x00}, float64(65536)},
		{"uint eight byte", []byte{0x1b, 0, 0, 0, 0, 0, 0, 0x01, 0x00}, float64(256)},
		{"negative", []byte{0x20}, float64(-1)},
		{"negative one byte", []byte{0x38, 0x63}, float64(-100)},
		{"false", []byte{0xf4}, false},
		{"true", []byte{0xf5}, true},
		{"null", []byte{0xf6}, nil},
		{"undefined reads as null", []byte{0xf7}, nil},
		{"float64", []byte{0xfb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}, 1.5},
		{"float32", []byte{0xfa, 0x3f, 0xc0, 0x00, 0x00}, 1.5},
		{"float16", []byte{0xf9, 0x3e, 0x00}, 1.5},
		{"empty text", []byte{0x60}, ""},
		{"text", []byte{0x63, 'a', 'b', 'c'}, "abc"},
		{"bytes become base64", []byte{0x43, 0x01, 0x02, 0x03}, "AQID"},
		{"definite array", []byte{0x82, 0x01, 0x02}, []any{float64(1), float64(2)}},
		{"empty array", []byte{0x80}, []any{}},
		{"indefinite array", []byte{0x9f, 0x01, 0x02, 0xff}, []any{float64(1), float64(2)}},
		{"definite map", []byte{0xa1, 0x61, 'k', 0x01}, map[string]any{"k": float64(1)}},
		{"empty map", []byte{0xa0}, map[string]any{}},
		{"indefinite map", []byte{0xbf, 0x61, 'k', 0x01, 0xff}, map[string]any{"k": float64(1)}},
		{"indefinite text chunks", []byte{0x7f, 0x62, 'a', 'b', 0x61, 'c', 0xff}, "abc"},
	}
	for _, c := range cases {
		got, err := Decode(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v (%T), want %#v", c.name, got, got, c.want)
		}
	}
}

// float16 has three regimes and the subnormal one is the easy one to get
// wrong, so each is pinned.
func TestFloat16Regimes(t *testing.T) {
	cases := []struct {
		name string
		bits []byte
		want float64
	}{
		{"zero", []byte{0xf9, 0x00, 0x00}, 0},
		{"negative zero", []byte{0xf9, 0x80, 0x00}, math.Copysign(0, -1)},
		{"one", []byte{0xf9, 0x3c, 0x00}, 1},
		{"smallest subnormal", []byte{0xf9, 0x00, 0x01}, math.Ldexp(1, -24)},
		{"largest subnormal", []byte{0xf9, 0x03, 0xff}, math.Ldexp(1023, -24)},
		{"infinity", []byte{0xf9, 0x7c, 0x00}, math.Inf(1)},
		{"negative infinity", []byte{0xf9, 0xfc, 0x00}, math.Inf(-1)},
	}
	for _, c := range cases {
		got, err := Decode(c.bits)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		f := got.(float64)
		if f != c.want || math.Signbit(f) != math.Signbit(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, f, c.want)
		}
	}
	nan, err := Decode([]byte{0xf9, 0x7e, 0x00})
	if err != nil || !math.IsNaN(nan.(float64)) {
		t.Errorf("NaN: got %v (%v)", nan, err)
	}
}

// Tag 1 is the only tag carried; everything else is refused rather than
// unwrapped, because a tag changes what the payload means.
func TestTags(t *testing.T) {
	// tag(1) 1700000000
	got, err := Decode([]byte{0xc1, 0x1a, 0x65, 0x50, 0x1a, 0x00})
	if err != nil {
		t.Fatalf("tag 1: %v", err)
	}
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("tag 1 decoded to %T, want time.Time", got)
	}
	if ts.Unix() != 0x6550_1a00 {
		t.Errorf("tag 1 = %v (%d)", ts, ts.Unix())
	}
	// tag(2), a bignum: refused, not silently handed back as bytes.
	if _, err := Decode([]byte{0xc2, 0x41, 0x01}); err == nil {
		t.Error("an unsupported tag was accepted; its payload would read as a plain value")
	}
}

// Malformed frames must be errors, never panics and never a plausible value.
func TestDecodeRefusesMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":                      {},
		"truncated uint argument":    {0x19, 0x01},
		"truncated text":             {0x63, 'a'},
		"unterminated indef array":   {0x9f, 0x01},
		"unterminated indef map":     {0xbf, 0x61, 'k', 0x01},
		"map with an odd item count": {0xa1, 0x61, 'k'},
		"non-string map key":         {0xa1, 0x01, 0x01},
		"reserved additional info":   {0x1c},
		"bare break":                 {0xff},
		"trailing bytes":             {0x01, 0x01},
		"mismatched string chunk":    {0x7f, 0x42, 0x01, 0x02, 0xff},
	}
	for name, in := range cases {
		if v, err := Decode(in); err == nil {
			t.Errorf("%s: accepted, produced %#v", name, v)
		}
	}
}

// A nesting bomb has to stop at the bound rather than run the stack out.
func TestDecodeBoundsNesting(t *testing.T) {
	deep := make([]byte, 0, MaxDepth+8)
	for range MaxDepth + 4 {
		deep = append(deep, 0x9f) // indefinite array, never closed
	}
	if _, err := Decode(deep); err == nil {
		t.Error("a nesting bomb was accepted")
	}
}

// Gunzip passes plain bodies through untouched — the JavaScript SDK does not
// compress, so the same operation arrives both ways.
func TestGunzipPassesPlainBodiesThrough(t *testing.T) {
	plain := []byte{0xa0}
	out, err := Gunzip(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, plain) {
		t.Errorf("a plain body was altered: %#v", out)
	}
}
