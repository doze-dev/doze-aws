package dozeaws_test

// The stack's clock reaches the services it assembles.
//
// Every one of the seventeen services has taken `Clock func() time.Time` in its
// Options since it was written, and the stack passed one to NONE of them. The
// seam existed, per-service unit tests exercised it, and the assembly every
// deployment and every cross-service test actually goes through threw it away.
// A test that wanted to move time could do it one service at a time, which is
// no use at all for anything involving two.
//
// This asserts it through the WIRE rather than by reading fields, because the
// failure being guarded against is precisely a constructor literal that forgets
// to pass it — which reading the field back from the same literal would not
// catch.

import (
	"context"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// A date far enough from now that no real clock could produce it by accident.
var clockEpoch = time.Date(2011, 3, 14, 15, 9, 26, 0, time.UTC)

func TestTheStackClockReachesItsServices(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	ctx := context.Background()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(),
		Logf:    func(string, ...any) {},
		Clock:   func() time.Time { return clockEpoch },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(st.Handler())
	defer ts.Close()

	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	ep := aws.String(ts.URL)

	// Four services, four different ways of reporting a creation time: a
	// DynamoDB table (JSON, epoch float), a KMS key (JSON), a secret (JSON),
	// and an SQS queue attribute (string of unix seconds).
	t.Run("dynamodb", func(t *testing.T) {
		c := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = ep })
		out, err := c.CreateTable(ctx, &awsddb.CreateTableInput{
			TableName:            aws.String("clock"),
			AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
			KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertEpoch(t, aws.ToTime(out.TableDescription.CreationDateTime))
	})

	t.Run("kms", func(t *testing.T) {
		c := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = ep })
		out, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
		if err != nil {
			t.Fatal(err)
		}
		assertEpoch(t, aws.ToTime(out.KeyMetadata.CreationDate))
	})

	t.Run("secretsmanager", func(t *testing.T) {
		c := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = ep })
		if _, err := c.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name: aws.String("clock"), SecretString: aws.String("x"),
		}); err != nil {
			t.Fatal(err)
		}
		got, err := c.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("clock")})
		if err != nil {
			t.Fatal(err)
		}
		assertEpoch(t, aws.ToTime(got.CreatedDate))
	})

	t.Run("sqs", func(t *testing.T) {
		c := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
		q, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("clock")})
		if err != nil {
			t.Fatal(err)
		}
		attrs, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl:       q.QueueUrl,
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameCreatedTimestamp},
		})
		if err != nil {
			t.Fatal(err)
		}
		raw := attrs.Attributes["CreatedTimestamp"]
		if raw == "" {
			t.Fatal("no CreatedTimestamp")
		}
		if want := "1300115366"; raw != want {
			t.Errorf("CreatedTimestamp = %s, want %s (the injected clock)", raw, want)
		}
	})
}

func assertEpoch(t *testing.T, got time.Time) {
	t.Helper()
	if got.IsZero() {
		t.Fatal("no timestamp reported")
	}
	// Second precision: the wire formats round, so compare the truncated value
	// rather than the instant.
	if !got.UTC().Truncate(time.Second).Equal(clockEpoch) {
		t.Errorf("timestamp = %s, want %s — the stack is not passing its Clock to this service",
			got.UTC().Format(time.RFC3339), clockEpoch.Format(time.RFC3339))
	}
}

// A nil Clock must still mean real time, or every deployment breaks.
func TestANilStackClockMeansRealTime(t *testing.T) {
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: t.TempDir(), Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(st.Handler())
	defer ts.Close()

	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	c := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	out, err := c.CreateKey(context.Background(), &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}
	created := aws.ToTime(out.KeyMetadata.CreationDate)
	if d := time.Since(created); d < -time.Minute || d > time.Minute {
		t.Errorf("creation date %s is not close to now — a nil Clock should mean time.Now",
			created.Format(time.RFC3339))
	}
	if strings.HasPrefix(created.UTC().Format(time.RFC3339), "2011-") {
		t.Error("a nil Clock produced the test epoch")
	}
}
