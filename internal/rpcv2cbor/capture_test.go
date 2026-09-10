package rpcv2cbor_test

// Regenerates testdata/ from the real SDKs, so the fixtures the decoder is
// tested against are observed bytes rather than a reading of the spec — the
// same discipline dzaudit applies to constraint tables.
//
// It WRITES files, so it does not run with the suite. Ask for it by name:
//
//	DOZE_CAPTURE=1 GOWORK=off go test ./internal/rpcv2cbor/ \
//	    -run TestCaptureCloudWatchFixtures -count=1 -v
//
// Re-run it when the pinned SDK versions move: the two findings that shaped
// this package — indefinite-length containers and gzipped request bodies —
// were both properties of a specific client, not of the protocol, so a
// version bump can change them.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	cwv1 "github.com/aws/aws-sdk-go/service/cloudwatch"
)

type capture struct {
	Wire    string            `json:"wire"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_base64,omitempty"`
	BodyRaw string            `json:"body_raw,omitempty"`
}

const fixtureDir = "testdata"

func record(t *testing.T, name, wire string, r *http.Request, body []byte, binary bool) {
	t.Helper()
	c := capture{Wire: wire, Method: r.Method, Path: r.URL.Path, Headers: map[string]string{}}
	for k, v := range r.Header {
		if len(v) > 0 {
			c.Headers[k] = v[0]
		}
	}
	if binary {
		c.BodyB64 = encodeB64(body)
	} else {
		c.BodyRaw = string(body)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixtureDir, name+".json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %s %s %v (%d body bytes)", path, c.Method, c.Path, c.Headers, len(body))
	// Also drop the raw body beside it, so decoder tests can read the exact
	// bytes without a base64 hop.
	if binary {
		if err := os.WriteFile(filepath.Join(fixtureDir, name+".cbor"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func encodeB64(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		v := uint32(chunk[0])<<16 | uint32(chunk[1])<<8 | uint32(chunk[2])
		out = append(out, alphabet[(v>>18)&63], alphabet[(v>>12)&63])
		if n > 1 {
			out = append(out, alphabet[(v>>6)&63])
		} else {
			out = append(out, '=')
		}
		if n > 2 {
			out = append(out, alphabet[v&63])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}

// The datum is deliberately awkward: two dimensions (the list-vs-map shape),
// a float value, an explicit timestamp, a unit enum, and StorageResolution —
// so the fixture exercises nesting, doubles, timestamps and small ints at once.
func TestCaptureCloudWatchFixtures(t *testing.T) {
	if os.Getenv("DOZE_CAPTURE") == "" {
		t.Skip("writes testdata/; set DOZE_CAPTURE=1 to regenerate")
	}
	var got *http.Request
	var body []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		// Answer in whatever the caller asked for; the request is what matters.
		if r.Header.Get("Accept") == "application/cbor" {
			w.Header().Set("Content-Type", "application/cbor")
			w.Write([]byte{0xa0}) // empty CBOR map
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(`<PutMetricDataResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/"></PutMetricDataResponse>`))
	}))
	defer ts.Close()

	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// ---- aws-sdk-go-v2: rpcv2Cbor ----
	v2 := awscw.New(awscw.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	})
	_, _ = v2.PutMetricData(t.Context(), &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Checkouts"),
			Value:      aws.Float64(1.5),
			Unit:       cwtypes.StandardUnitCount,
			Dimensions: []cwtypes.Dimension{
				{Name: aws.String("FunctionName"), Value: aws.String("checkout")},
				{Name: aws.String("Stage"), Value: aws.String("prod")},
			},
			StorageResolution: aws.Int32(1),
		}},
	})
	record(t, "putmetricdata_cbor", "rpcv2Cbor", got, body, true)

	// ---- aws-sdk-go v1: awsQuery ----
	sess := session.Must(session.NewSession(&awsv1.Config{
		Region:      awsv1.String("us-east-1"),
		Endpoint:    awsv1.String(ts.URL),
		Credentials: credsv1.NewStaticCredentials("test", "test", ""),
	}))
	_, _ = cwv1.New(sess).PutMetricData(&cwv1.PutMetricDataInput{
		Namespace: awsv1.String("Shop"),
		MetricData: []*cwv1.MetricDatum{{
			MetricName: awsv1.String("Checkouts"),
			Value:      awsv1.Float64(1.5),
			Unit:       awsv1.String("Count"),
			Dimensions: []*cwv1.Dimension{
				{Name: awsv1.String("FunctionName"), Value: awsv1.String("checkout")},
				{Name: awsv1.String("Stage"), Value: awsv1.String("prod")},
			},
			StorageResolution: awsv1.Int64(1),
		}},
	})
	record(t, "putmetricdata_query", "awsQuery", got, body, false)
}
