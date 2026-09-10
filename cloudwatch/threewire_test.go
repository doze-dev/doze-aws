package cloudwatch_test

// D1a's exit criterion.
//
// The same call, made three ways, must reach one handler and be validated by
// one table. These are not mock requests: the CBOR and Query bodies come from
// the real SDKs, and the JSON body is what @aws-sdk/client-cloudwatch sends,
// which is also what the AWS CLI sends.
//
// If any of this is wrong, everything built on top of it is wrong, which is
// why it is proved before the metric store exists.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/cloudwatch"
	"github.com/doze-dev/doze-aws/internal/rpcv2cbor"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := cloudwatch.New(cloudwatch.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, r *http.Request) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

// cborRequest replays the captured SDK body — still gzipped, exactly as
// aws-sdk-go-v2 sent it.
func cborRequest(t *testing.T, base, op string) *http.Request {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "internal", "rpcv2cbor", "testdata", "putmetricdata_cbor.cbor"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("POST",
		base+"/service/GraniteServiceVersion20100801/operation/"+op, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/cbor")
	r.Header.Set("Accept", "application/cbor")
	r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set("X-Amzn-Query-Mode", "true")
	return r
}

const jsonBody = `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts",` +
	`"Dimensions":[{"Name":"FunctionName","Value":"checkout"},{"Name":"Stage","Value":"prod"}],` +
	`"Value":1.5,"Unit":"Count","StorageResolution":1}]}`

func jsonRequest(t *testing.T, base, op, body string) *http.Request {
	t.Helper()
	r, _ := http.NewRequest("POST", base+"/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801."+op)
	return r
}

// The Query body is the one aws-sdk-go v1 produced, member-indexed exactly as
// it flattens a list of structures.
const queryBody = "Action=PutMetricData&Version=2010-08-01&Namespace=Shop" +
	"&MetricData.member.1.MetricName=Checkouts" +
	"&MetricData.member.1.Dimensions.member.1.Name=FunctionName" +
	"&MetricData.member.1.Dimensions.member.1.Value=checkout" +
	"&MetricData.member.1.Dimensions.member.2.Name=Stage" +
	"&MetricData.member.1.Dimensions.member.2.Value=prod" +
	"&MetricData.member.1.Unit=Count" +
	"&MetricData.member.1.StorageResolution=1" +
	"&MetricData.member.1.Value=1.5"

func queryRequest(t *testing.T, base, body string) *http.Request {
	t.Helper()
	r, _ := http.NewRequest("POST", base+"/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// One valid call, three wires, one handler.
func TestPutMetricDataOnAllThreeWires(t *testing.T) {
	ts := server(t)

	t.Run("rpcv2Cbor", func(t *testing.T) {
		code, hdr, body := do(t, cborRequest(t, ts.URL, "PutMetricData"))
		if code != 200 {
			t.Fatalf("status %d: %x", code, body)
		}
		if got := hdr.Get("Smithy-Protocol"); got != "rpc-v2-cbor" {
			t.Errorf("the response must echo the protocol, got %q", got)
		}
		if _, err := rpcv2cbor.DecodeMap(body); err != nil {
			t.Errorf("the response is not decodable CBOR: %v", err)
		}
	})

	t.Run("awsJson1_0", func(t *testing.T) {
		code, _, body := do(t, jsonRequest(t, ts.URL, "PutMetricData", jsonBody))
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Errorf("the response is not JSON: %v (%s)", err, body)
		}
	})

	t.Run("awsQuery", func(t *testing.T) {
		code, _, body := do(t, queryRequest(t, ts.URL, queryBody))
		if code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
		if !strings.Contains(string(body), "PutMetricDataResponse") {
			t.Errorf("the response is not the Query envelope: %s", body)
		}
	})
}

// The constraint table is written once against the JSON shape. This is what
// says it actually reaches all three — and the Query case is the one that
// only works because FromQuery keeps `.member.` containers as lists.
func TestOneConstraintTableRefusesOnAllThreeWires(t *testing.T) {
	ts := server(t)

	// A dimension missing its Value: MetricData[].Dimensions[].Value is
	// @required, and the path only resolves if Dimensions stayed a LIST —
	// which is the whole point of the FromQuery marker fix.
	//
	// One dimension, not two. FromQuery deliberately folds every element of a
	// list into the first ("checking the first is checking the rule"), so a
	// second, complete dimension would supply the Value removed from the
	// first and the case would pass for the wrong reason.
	t.Run("awsQuery rejects a dimension with no Value", func(t *testing.T) {
		bad := "Action=PutMetricData&Version=2010-08-01&Namespace=Shop" +
			"&MetricData.member.1.MetricName=Checkouts" +
			"&MetricData.member.1.Value=1.5" +
			"&MetricData.member.1.Dimensions.member.1.Name=FunctionName"
		code, _, body := do(t, queryRequest(t, ts.URL, bad))
		if code == 200 {
			t.Fatalf("accepted a dimension with no Value: %s", body)
		}
		if !strings.Contains(strings.ToLower(string(body)), "dimensions") {
			t.Errorf("the refusal does not name the member: %s", body)
		}
	})

	// And the same body WITH the Value is accepted, so the case above is
	// failing for the member it names rather than for the shorter body.
	t.Run("awsQuery accepts it once the Value is there", func(t *testing.T) {
		good := "Action=PutMetricData&Version=2010-08-01&Namespace=Shop" +
			"&MetricData.member.1.MetricName=Checkouts" +
			"&MetricData.member.1.Value=1.5" +
			"&MetricData.member.1.Dimensions.member.1.Name=FunctionName" +
			"&MetricData.member.1.Dimensions.member.1.Value=checkout"
		if code, _, body := do(t, queryRequest(t, ts.URL, good)); code != 200 {
			t.Fatalf("status %d: %s", code, body)
		}
	})

	t.Run("awsJson1_0 rejects the same thing", func(t *testing.T) {
		bad := strings.Replace(jsonBody, `{"Name":"FunctionName","Value":"checkout"},`, `{"Name":"FunctionName"},`, 1)
		code, _, body := do(t, jsonRequest(t, ts.URL, "PutMetricData", bad))
		if code == 200 {
			t.Fatalf("accepted a dimension with no Value: %s", body)
		}
	})

	// An enum the model does not allow, on the JSON wire.
	t.Run("an invalid Unit is refused", func(t *testing.T) {
		bad := strings.Replace(jsonBody, `"Unit":"Count"`, `"Unit":"Furlongs"`, 1)
		code, _, body := do(t, jsonRequest(t, ts.URL, "PutMetricData", bad))
		if code == 200 {
			t.Fatalf("accepted Unit=Furlongs: %s", body)
		}
		// modelcheck lowercases the first letter, as AWS does: "metricData.1.unit".
		if !strings.Contains(strings.ToLower(string(body)), "unit") {
			t.Errorf("the refusal does not name the member: %s", body)
		}
	})

	// A namespace beginning with a colon violates the model's pattern.
	t.Run("a namespace the pattern forbids is refused", func(t *testing.T) {
		bad := strings.Replace(jsonBody, `"Namespace":"Shop"`, `"Namespace":":Shop"`, 1)
		code, _, body := do(t, jsonRequest(t, ts.URL, "PutMetricData", bad))
		if code == 200 {
			t.Fatalf("accepted a namespace starting with a colon: %s", body)
		}
	})
}

// An operation the service refuses says so by name on every wire, rather than
// answering InvalidAction and reading like a typo.
func TestRefusedOperationsAreNamed(t *testing.T) {
	ts := server(t)
	code, _, body := do(t, jsonRequest(t, ts.URL, "GetMetricWidgetImage", "{}"))
	if code == 200 {
		t.Fatal("a refused operation answered 200")
	}
	if !strings.Contains(string(body), "UnsupportedOperationException") ||
		!strings.Contains(string(body), "graphics stack") {
		t.Errorf("the refusal does not carry its reason: %s", body)
	}
}

// An error on the CBOR wire keeps the modern name in the body and carries the
// legacy Query code in a header, because the caller set x-amzn-query-mode.
func TestCBORErrorCarriesTheLegacyQueryCode(t *testing.T) {
	ts := server(t)
	// An empty CBOR map: Namespace is required.
	r, _ := http.NewRequest("POST",
		ts.URL+"/service/GraniteServiceVersion20100801/operation/PutMetricData",
		bytes.NewReader([]byte{0xa0}))
	r.Header.Set("Content-Type", "application/cbor")
	r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	r.Header.Set("X-Amzn-Query-Mode", "true")

	code, hdr, body := do(t, r)
	if code == 200 {
		t.Fatal("an empty body was accepted")
	}
	if got := hdr.Get("X-Amzn-Query-Error"); !strings.Contains(got, ";Sender") {
		t.Errorf("X-Amzn-Query-Error = %q, want a Code;Fault pair", got)
	}
	out, err := rpcv2cbor.DecodeMap(body)
	if err != nil {
		t.Fatalf("the error body is not decodable CBOR: %v", err)
	}
	if _, ok := out["__type"]; !ok {
		t.Errorf("the error body has no __type: %#v", out)
	}
}
