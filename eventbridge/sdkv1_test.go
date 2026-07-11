// SDK v1 contract tests: the same EventBridge scenarios driven by the legacy
// aws-sdk-go (v1) client — the second independent client generation, per the
// doze-aws dual-SDK requirement.
package eventbridge_test

import (
	"net/http/httptest"
	"testing"

	awsv1 "github.com/aws/aws-sdk-go/aws"
	credsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	ebv1 "github.com/aws/aws-sdk-go/service/eventbridge"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func ebV1Client(t *testing.T) *ebv1.EventBridge {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping SDK contract test in -short mode")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	sess, err := session.NewSession(awsv1.NewConfig().
		WithRegion(awsident.Region).
		WithEndpoint(ts.URL).
		WithCredentials(credsv1.NewStaticCredentials(awsident.AccessKeyID, awsident.SecretAccessKey, "")))
	if err != nil {
		t.Fatal(err)
	}
	return ebv1.New(sess)
}

func TestSDKV1RuleLifecycle(t *testing.T) {
	c := ebV1Client(t)

	if _, err := c.PutRule(&ebv1.PutRuleInput{
		Name:         awsv1.String("v1-rule"),
		EventPattern: awsv1.String(`{"source":["v1"]}`),
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}
	if _, err := c.PutTargets(&ebv1.PutTargetsInput{
		Rule: awsv1.String("v1-rule"),
		Targets: []*ebv1.Target{{
			Id:  awsv1.String("t1"),
			Arn: awsv1.String(awsident.ARN("sqs", "v1-eb")),
		}},
	}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	dr, err := c.DescribeRule(&ebv1.DescribeRuleInput{Name: awsv1.String("v1-rule")})
	if err != nil || awsv1.StringValue(dr.EventPattern) == "" {
		t.Fatalf("DescribeRule = %+v err=%v", dr, err)
	}
	lr, err := c.ListRules(&ebv1.ListRulesInput{NamePrefix: awsv1.String("v1-")})
	if err != nil || len(lr.Rules) != 1 {
		t.Fatalf("ListRules = %d err=%v", len(lr.Rules), err)
	}
	lt, err := c.ListTargetsByRule(&ebv1.ListTargetsByRuleInput{Rule: awsv1.String("v1-rule")})
	if err != nil || len(lt.Targets) != 1 {
		t.Fatalf("ListTargetsByRule = %d err=%v", len(lt.Targets), err)
	}

	// PutEvents: a matching event is accepted (delivery is proven in the v2
	// suite; here the legacy client's JSON1.1 encoding must be accepted).
	pe, err := c.PutEvents(&ebv1.PutEventsInput{
		Entries: []*ebv1.PutEventsRequestEntry{{
			Source: awsv1.String("v1"), DetailType: awsv1.String("t"), Detail: awsv1.String(`{}`),
		}},
	})
	if err != nil || awsv1.Int64Value(pe.FailedEntryCount) != 0 {
		t.Fatalf("PutEvents = %+v err=%v", pe, err)
	}

	if _, err := c.RemoveTargets(&ebv1.RemoveTargetsInput{Rule: awsv1.String("v1-rule"), Ids: []*string{awsv1.String("t1")}}); err != nil {
		t.Fatalf("RemoveTargets: %v", err)
	}
	if _, err := c.DeleteRule(&ebv1.DeleteRuleInput{Name: awsv1.String("v1-rule")}); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
}
