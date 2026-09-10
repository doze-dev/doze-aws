package cloudwatch_test

// The contract test for the modern wire: aws-sdk-go-v2, which speaks Smithy
// RPC v2 CBOR to CloudWatch.
//
// This is the one that matters most. The handwritten tests replay bytes; this
// drives the actual SDK, so it exercises the parts nobody writes by hand —
// the signer, the request compressor, the CBOR serialiser, and the
// deserialiser that has to accept whatever comes back. A response this
// accepts is one a real Go program accepts.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/doze-dev/doze-aws/cloudwatch"
)

func v2Client(t *testing.T) (*awscw.Client, context.Context) {
	t.Helper()
	s, err := cloudwatch.New(cloudwatch.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return awscw.New(awscw.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	}), context.Background()
}

func TestSDKPutAndListMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)

	// The SDK gzips this body and encodes it with indefinite-length
	// containers; neither is visible from here, which is the point.
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{
			{
				MetricName: aws.String("Checkouts"),
				Value:      aws.Float64(1.5),
				Unit:       cwtypes.StandardUnitCount,
				Dimensions: []cwtypes.Dimension{
					{Name: aws.String("Stage"), Value: aws.String("prod")},
				},
				StorageResolution: aws.Int32(1),
			},
			{
				MetricName: aws.String("Checkouts"),
				Value:      aws.Float64(2),
				Dimensions: []cwtypes.Dimension{
					{Name: aws.String("Stage"), Value: aws.String("staging")},
				},
			},
		},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := c.ListMetrics(ctx, &awscw.ListMetricsInput{Namespace: aws.String("Shop")})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(out.Metrics) != 2 {
		t.Fatalf("want 2 metrics, got %d", len(out.Metrics))
	}
	for _, m := range out.Metrics {
		if aws.ToString(m.Namespace) != "Shop" || aws.ToString(m.MetricName) != "Checkouts" {
			t.Errorf("unexpected metric %+v", m)
		}
		if len(m.Dimensions) != 1 || aws.ToString(m.Dimensions[0].Name) != "Stage" {
			t.Errorf("the SDK did not deserialise the nested dimensions: %+v", m.Dimensions)
		}
	}
}

// A filter the SDK builds, round-tripped through the CBOR encoder on the way
// out and the decoder on the way in.
func TestSDKListMetricsFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)
	for _, stage := range []string{"prod", "staging"} {
		if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
			Namespace: aws.String("Shop"),
			MetricData: []cwtypes.MetricDatum{{
				MetricName: aws.String("Checkouts"),
				Value:      aws.Float64(1),
				Dimensions: []cwtypes.Dimension{{Name: aws.String("Stage"), Value: aws.String(stage)}},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := c.ListMetrics(ctx, &awscw.ListMetricsInput{
		Namespace:  aws.String("Shop"),
		Dimensions: []cwtypes.DimensionFilter{{Name: aws.String("Stage"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(out.Metrics) != 1 {
		t.Fatalf("want the one prod metric, got %d", len(out.Metrics))
	}
	if v := aws.ToString(out.Metrics[0].Dimensions[0].Value); v != "prod" {
		t.Errorf("got the %q metric", v)
	}
}

// A refusal has to arrive as a typed error, not as a transport failure — the
// error shape is part of the contract the SDK deserialises against.
func TestSDKRefusalIsTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)
	_, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Shop"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("Checkouts"),
			Value:      aws.Float64(1),
			// A dimension with no Value: @required in the model.
			Dimensions: []cwtypes.Dimension{{Name: aws.String("Stage")}},
		}},
	})
	if err == nil {
		t.Fatal("a dimension with no Value was accepted")
	}
	// The SDK must have understood the body well enough to surface the
	// message; a decode failure here would mean the error envelope is wrong.
	if !strings.Contains(strings.ToLower(err.Error()), "dimensions") {
		t.Errorf("the SDK did not surface a useful message: %v", err)
	}
}

// An operation refused by name reaches the SDK as an error naming it, rather
// than as an empty success the caller has to infer from.
func TestSDKRefusedOperationIsNamed(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)
	_, err := c.GetMetricWidgetImage(ctx, &awscw.GetMetricWidgetImageInput{
		MetricWidget: aws.String("{}"),
	})
	if err == nil {
		t.Fatal("a refused operation succeeded")
	}
	if !strings.Contains(err.Error(), "graphics stack") {
		t.Errorf("the refusal lost its reason on the way to the SDK: %v", err)
	}
}
