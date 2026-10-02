package cloudwatch_test

// Refusals the boto3 conformance suite found missing
// (conformance/tests/test_cloudwatch.py).

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/smithy-go"
)

func wantRefused(t *testing.T, what string, err error) {
	t.Helper()
	var api smithy.APIError
	if !errors.As(err, &api) {
		t.Errorf("%s: got %v, want a refusal", what, err)
	}
}

// AWS/ is where the services publish, and a client may not. doze-aws's own
// services still can: they call as peers, which cloudwatch/producers_test.go
// exercises end to end.
func TestAClientCannotPublishUnderTheAWSNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)
	_, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace:  aws.String("AWS/Lambda"),
		MetricData: []cwtypes.MetricDatum{{MetricName: aws.String("Errors"), Value: aws.Float64(1)}},
	})
	wantRefused(t, "PutMetricData under AWS/", err)
	if _, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace:  aws.String("AWSome/mine"),
		MetricData: []cwtypes.MetricDatum{{MetricName: aws.String("m"), Value: aws.Float64(1)}},
	}); err != nil {
		t.Errorf("a namespace that merely starts with the letters: %v", err)
	}
}

func TestOneObservationOrASummaryNotBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	c, ctx := v2Client(t)
	_, err := c.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("app"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("m"), Value: aws.Float64(1),
			StatisticValues: &cwtypes.StatisticSet{
				SampleCount: aws.Float64(1), Sum: aws.Float64(1), Minimum: aws.Float64(1), Maximum: aws.Float64(1)},
		}},
	})
	wantRefused(t, "a datum with a Value and StatisticValues", err)

	_, err = c.PutMetricAlarm(ctx, &awscw.PutMetricAlarmInput{
		AlarmName: aws.String("both"), Namespace: aws.String("app"), MetricName: aws.String("m"),
		Statistic: cwtypes.StatisticSum, ExtendedStatistic: aws.String("p99"),
		Period: aws.Int32(60), EvaluationPeriods: aws.Int32(1), Threshold: aws.Float64(1),
		ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
	})
	wantRefused(t, "an alarm with a Statistic and an ExtendedStatistic", err)
}
