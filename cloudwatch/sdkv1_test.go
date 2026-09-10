package cloudwatch_test

// The contract test for the legacy wire: aws-sdk-go v1, which still speaks
// the Query protocol to CloudWatch.
//
// A second, independently generated request builder reading the same service.
// v1 flattens a list of structures into MetricData.member.1.Dimensions.member.1.Name
// and parses <Metrics><member> back out of XML — neither of which the v2 path
// exercises at all, so a bug in the Query lane is invisible without this.

import (
	"net/http/httptest"
	"strings"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	cwv1 "github.com/aws/aws-sdk-go/service/cloudwatch"

	"github.com/doze-dev/doze-aws/cloudwatch"
)

func v1Client(t *testing.T) *cwv1.CloudWatch {
	t.Helper()
	s, err := cloudwatch.New(cloudwatch.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	sess := session.Must(session.NewSession(&awsv1.Config{
		Region:      awsv1.String("us-east-1"),
		Endpoint:    awsv1.String(ts.URL),
		Credentials: credsv1.NewStaticCredentials("test", "test", ""),
	}))
	return cwv1.New(sess)
}

func TestSDKV1PutAndListMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c := v1Client(t)

	if _, err := c.PutMetricData(&cwv1.PutMetricDataInput{
		Namespace: awsv1.String("Shop"),
		MetricData: []*cwv1.MetricDatum{{
			MetricName: awsv1.String("Checkouts"),
			Value:      awsv1.Float64(1.5),
			Unit:       awsv1.String("Count"),
			Dimensions: []*cwv1.Dimension{
				{Name: awsv1.String("Stage"), Value: awsv1.String("prod")},
				{Name: awsv1.String("Region"), Value: awsv1.String("eu")},
			},
			StorageResolution: awsv1.Int64(1),
		}},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	out, err := c.ListMetrics(&cwv1.ListMetricsInput{Namespace: awsv1.String("Shop")})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(out.Metrics) != 1 {
		t.Fatalf("want 1 metric, got %d", len(out.Metrics))
	}
	m := out.Metrics[0]
	if awsv1.StringValue(m.Namespace) != "Shop" || awsv1.StringValue(m.MetricName) != "Checkouts" {
		t.Errorf("unexpected metric %v", m)
	}
	// Two dimensions round-tripped through the Query flattening on the way in
	// and the <member> wrapping on the way out.
	if len(m.Dimensions) != 2 {
		t.Fatalf("want 2 dimensions, got %d: %v", len(m.Dimensions), m.Dimensions)
	}
	got := map[string]string{}
	for _, d := range m.Dimensions {
		got[awsv1.StringValue(d.Name)] = awsv1.StringValue(d.Value)
	}
	if got["Stage"] != "prod" || got["Region"] != "eu" {
		t.Errorf("dimensions did not survive the round trip: %v", got)
	}
}

// The Query wire answers with the LEGACY error code in <Code>, which is what
// an old client deserialises against. The modern name would be meaningless to
// it.
func TestSDKV1ErrorUsesTheLegacyCode(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c := v1Client(t)
	_, err := c.PutMetricData(&cwv1.PutMetricDataInput{
		Namespace: awsv1.String("Shop"),
		MetricData: []*cwv1.MetricDatum{{
			MetricName: awsv1.String("Checkouts"),
			Value:      awsv1.Float64(1),
			Unit:       awsv1.String("Furlongs"), // not in StandardUnit
		}},
	})
	if err == nil {
		t.Fatal("an invalid Unit was accepted")
	}
	// v1 surfaces the <Code> element as the error code.
	if !strings.Contains(err.Error(), "InvalidParameterValue") {
		t.Errorf("want the legacy code, got: %v", err)
	}
	if strings.Contains(err.Error(), "ValidationException") {
		t.Errorf("the modern code reached a Query client: %v", err)
	}
}

// A filtered listing, built and parsed by v1.
func TestSDKV1ListMetricsFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c := v1Client(t)
	for _, stage := range []string{"prod", "staging"} {
		if _, err := c.PutMetricData(&cwv1.PutMetricDataInput{
			Namespace: awsv1.String("Shop"),
			MetricData: []*cwv1.MetricDatum{{
				MetricName: awsv1.String("Checkouts"),
				Value:      awsv1.Float64(1),
				Dimensions: []*cwv1.Dimension{
					{Name: awsv1.String("Stage"), Value: awsv1.String(stage)},
				},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := c.ListMetrics(&cwv1.ListMetricsInput{
		Namespace:  awsv1.String("Shop"),
		Dimensions: []*cwv1.DimensionFilter{{Name: awsv1.String("Stage"), Value: awsv1.String("prod")}},
	})
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(out.Metrics) != 1 {
		t.Fatalf("want the one prod metric, got %d", len(out.Metrics))
	}
}
