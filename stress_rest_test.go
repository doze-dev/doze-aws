package dozeaws_test

// Concurrent -race coverage for the ten services TestConcurrencyStress does not
// drive.
//
// WHY THIS EXISTS. Two shutdown races shipped this session, and both were
// invisible for the same reason: the race detector only reports what actually
// runs in parallel, so unsynchronised state in a path no test exercises
// concurrently is state nobody is checking. Stack.Close was the clearest case —
// it nils its closers slice, which makes a second SEQUENTIAL close a clean
// no-op, so the obvious test passed while two at once raced on the field.
//
// TestConcurrencyStress is real coverage, but it drives seven services: s3,
// dynamodb, sqs, kms, sns, eventbridge, stepfunctions. The other ten had their
// stores and in-memory caches guarded by inspection only. This closes that.
//
// It is a sibling rather than more work inside stressOnce because that test
// already runs 68s under -race and is the slowest in the suite; doubling its
// operation count would make the full gate painful enough to skip, which is a
// worse outcome than a second test.
//
// TestEveryServiceHasConcurrentCoverage below is the ratchet: adding an
// eighteenth service fails until it is driven by one of these two.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsapigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	awscw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	awscwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kintypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

// restClients are the ten services this test drives.
type restClients struct {
	sts  *awssts.Client
	ssm  *awsssm.Client
	sm   *awssm.Client
	logs *awscwl.Client
	cw   *awscw.Client
	kin  *awskinesis.Client
	iam  *awsiam.Client
	cfn  *awscfn.Client
	apig *awsapigw.Client
	lam  *awslambda.Client
}

func TestConcurrencyStressRest(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency stress; run in the full -race suite")
	}
	ctx := context.Background()
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	ts := httptest.NewServer(stack.Handler())
	defer ts.Close()

	creds := credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")
	cfg := aws.Config{Region: awsident.Region, Credentials: creds}
	c := &restClients{
		sts:  awssts.NewFromConfig(cfg, func(o *awssts.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		ssm:  awsssm.NewFromConfig(cfg, func(o *awsssm.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		sm:   awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		logs: awscwl.NewFromConfig(cfg, func(o *awscwl.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		cw:   awscw.NewFromConfig(cfg, func(o *awscw.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		kin:  awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		iam:  awsiam.NewFromConfig(cfg, func(o *awsiam.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		cfn:  awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		apig: awsapigw.NewFromConfig(cfg, func(o *awsapigw.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		lam:  awslambda.NewFromConfig(cfg, func(o *awslambda.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
	}

	// Shared resources every worker contends on. Contention on ONE record is
	// the point — per-worker resources would exercise the store without
	// exercising its locking.
	if _, err := c.logs.CreateLogGroup(ctx, &awscwl.CreateLogGroupInput{
		LogGroupName: aws.String("/stress"),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}
	if _, err := c.kin.CreateStream(ctx, &awskinesis.CreateStreamInput{
		StreamName: aws.String("stress"), ShardCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}
	if _, err := c.lam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("stressfn"), Runtime: lamtypes.RuntimeProvidedal2,
		Handler: aws.String("bootstrap"),
		Role:    aws.String("arn:aws:iam::000000000000:role/r"),
		Code:    &lamtypes.FunctionCode{S3Bucket: aws.String("_local_"), S3Key: aws.String(buildRecorder(t))},
		Environment: &lamtypes.Environment{Variables: map[string]string{
			"MARKER_FILE": t.TempDir() + "/marker.log"}},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// Fewer than TestConcurrencyStress's 24x20: these ten include process
	// spawning (Lambda) and template application (CloudFormation), and the
	// point is contention rather than throughput.
	const (
		workers = 16
		iters   = 6
	)
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iters {
				id := fmt.Sprintf("w%d-i%d", w, i)
				if err := restOnce(ctx, c, id); err != nil {
					select {
					case errCh <- fmt.Errorf("worker %d iter %d: %w", w, i, err):
					default:
					}
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	// Assert the work actually landed, rather than trusting that nothing
	// returned an error. The audit's own finding about the soak test was that
	// it drops every return value and so cannot observe three services
	// failing; a stress test that only checks for errors has the same hole,
	// because a store that silently dropped writes under contention would
	// still return 200 to every call.
	const want = workers * iters

	// CloudWatch: one shared metric, so the sum across every datapoint is
	// exactly the number of PutMetricData calls that survived contention.
	stats, err := c.cw.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace: aws.String("Stress"), MetricName: aws.String("hits"),
		StartTime: aws.Time(time.Now().Add(-time.Hour)), EndTime: aws.Time(time.Now().Add(time.Hour)),
		Period: aws.Int32(60), Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	})
	if err != nil {
		t.Fatalf("final GetMetricStatistics: %v", err)
	}
	var sum float64
	for _, d := range stats.Datapoints {
		sum += aws.ToFloat64(d.Sum)
	}
	if int(sum) != want {
		t.Errorf("cloudwatch recorded %d samples, want %d — writes were lost under contention", int(sum), want)
	}

	// Kinesis: one shard read from the horizon, so every PutRecord must be here.
	shards, err := c.kin.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("stress")})
	if err != nil {
		t.Fatalf("final ListShards: %v", err)
	}
	it, err := c.kin.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("stress"), ShardId: shards.Shards[0].ShardId,
		ShardIteratorType: kintypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		t.Fatalf("final GetShardIterator: %v", err)
	}
	recs, err := c.kin.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator})
	if err != nil {
		t.Fatalf("final GetRecords: %v", err)
	}
	if len(recs.Records) != want {
		t.Errorf("kinesis holds %d records, want %d — writes were lost under contention", len(recs.Records), want)
	}

	// IAM: one user per iteration, and the list is the contended structure.
	users, err := c.iam.ListUsers(ctx, &awsiam.ListUsersInput{MaxItems: aws.Int32(1000)})
	if err != nil {
		t.Fatalf("final ListUsers: %v", err)
	}
	if len(users.Users) != want {
		t.Errorf("iam holds %d users, want %d — writes were lost under contention", len(users.Users), want)
	}
}

func restOnce(ctx context.Context, c *restClients, id string) error {
	// STS: stateless, but every worker hits the same signing and identity path.
	if _, err := c.sts.GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{}); err != nil {
		return fmt.Errorf("sts caller identity: %w", err)
	}

	// SSM: write then read back a unique parameter.
	if _, err := c.ssm.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/stress/" + id), Value: aws.String(id), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		return fmt.Errorf("ssm put: %w", err)
	}
	got, err := c.ssm.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/stress/" + id)})
	if err != nil {
		return fmt.Errorf("ssm get: %w", err)
	}
	if aws.ToString(got.Parameter.Value) != id {
		return fmt.Errorf("ssm value = %q, want %q", aws.ToString(got.Parameter.Value), id)
	}

	// Secrets Manager: create then read back.
	if _, err := c.sm.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("stress-" + id), SecretString: aws.String(id),
	}); err != nil {
		return fmt.Errorf("secretsmanager create: %w", err)
	}
	sec, err := c.sm.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("stress-" + id)})
	if err != nil {
		return fmt.Errorf("secretsmanager get: %w", err)
	}
	if aws.ToString(sec.SecretString) != id {
		return fmt.Errorf("secret = %q, want %q", aws.ToString(sec.SecretString), id)
	}

	// Logs: every worker writes into the SHARED group, which is what puts the
	// fan-out and the NoSync store under contention.
	if _, err := c.logs.CreateLogStream(ctx, &awscwl.CreateLogStreamInput{
		LogGroupName: aws.String("/stress"), LogStreamName: aws.String(id),
	}); err != nil {
		return fmt.Errorf("logs create stream: %w", err)
	}
	if _, err := c.logs.PutLogEvents(ctx, &awscwl.PutLogEventsInput{
		LogGroupName: aws.String("/stress"), LogStreamName: aws.String(id),
		LogEvents: []cwltypes.InputLogEvent{{
			Message: aws.String(id), Timestamp: aws.Int64(time.Now().UnixMilli())}},
	}); err != nil {
		return fmt.Errorf("logs put: %w", err)
	}
	if _, err := c.logs.FilterLogEvents(ctx, &awscwl.FilterLogEventsInput{
		LogGroupName: aws.String("/stress"),
	}); err != nil {
		return fmt.Errorf("logs filter: %w", err)
	}

	// CloudWatch: shared namespace and metric, so the sample list is contended.
	if _, err := c.cw.PutMetricData(ctx, &awscw.PutMetricDataInput{
		Namespace: aws.String("Stress"),
		MetricData: []cwtypes.MetricDatum{{
			MetricName: aws.String("hits"), Value: aws.Float64(1), Timestamp: aws.Time(time.Now())}},
	}); err != nil {
		return fmt.Errorf("cloudwatch put: %w", err)
	}
	if _, err := c.cw.GetMetricStatistics(ctx, &awscw.GetMetricStatisticsInput{
		Namespace: aws.String("Stress"), MetricName: aws.String("hits"),
		StartTime: aws.Time(time.Now().Add(-time.Hour)), EndTime: aws.Time(time.Now().Add(time.Hour)),
		Period: aws.Int32(60), Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
	}); err != nil {
		return fmt.Errorf("cloudwatch stats: %w", err)
	}

	// Kinesis: every worker writes to the SHARED single-shard stream, then
	// reads it back through an iterator.
	if _, err := c.kin.PutRecord(ctx, &awskinesis.PutRecordInput{
		StreamName: aws.String("stress"), PartitionKey: aws.String(id), Data: []byte(id),
	}); err != nil {
		return fmt.Errorf("kinesis put: %w", err)
	}
	shards, err := c.kin.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String("stress")})
	if err != nil {
		return fmt.Errorf("kinesis list shards: %w", err)
	}
	it, err := c.kin.GetShardIterator(ctx, &awskinesis.GetShardIteratorInput{
		StreamName: aws.String("stress"), ShardId: shards.Shards[0].ShardId,
		ShardIteratorType: kintypes.ShardIteratorTypeTrimHorizon,
	})
	if err != nil {
		return fmt.Errorf("kinesis iterator: %w", err)
	}
	if _, err := c.kin.GetRecords(ctx, &awskinesis.GetRecordsInput{ShardIterator: it.ShardIterator}); err != nil {
		return fmt.Errorf("kinesis get: %w", err)
	}

	// IAM: a unique user, which also drives the access recorder that every
	// other service's soft-mode guard writes into.
	if _, err := c.iam.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("u-" + id)}); err != nil {
		return fmt.Errorf("iam create user: %w", err)
	}
	if _, err := c.iam.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("u-" + id)}); err != nil {
		return fmt.Errorf("iam get user: %w", err)
	}

	// API Gateway: a unique REST API, then read it back.
	api, err := c.apig.CreateRestApi(ctx, &awsapigw.CreateRestApiInput{Name: aws.String("api-" + id)})
	if err != nil {
		return fmt.Errorf("apigateway create: %w", err)
	}
	if _, err := c.apig.GetRestApi(ctx, &awsapigw.GetRestApiInput{RestApiId: api.Id}); err != nil {
		return fmt.Errorf("apigateway get: %w", err)
	}

	// CloudFormation: a one-resource stack, which exercises the store and the
	// apply path rather than just a record write.
	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("st-" + id),
		TemplateBody: aws.String(`{"Resources":{"Q":{"Type":"AWS::SQS::Queue",` +
			`"Properties":{"QueueName":"cfn-` + id + `"}}}}`),
	}); err != nil {
		return fmt.Errorf("cloudformation create: %w", err)
	}
	if _, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{
		StackName: aws.String("st-" + id),
	}); err != nil {
		return fmt.Errorf("cloudformation describe: %w", err)
	}

	// Lambda: every worker invokes the SAME function, so the runner pool's
	// warm-process bookkeeping is what is under contention — the one piece of
	// per-service state here that is not a bbolt store.
	out, err := c.lam.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("stressfn"), Payload: []byte(`{"id":"` + id + `"}`),
	})
	if err != nil {
		return fmt.Errorf("lambda invoke: %w", err)
	}
	if out.FunctionError != nil {
		return fmt.Errorf("lambda invoke: %s: %s", aws.ToString(out.FunctionError), out.Payload)
	}
	return nil
}

// concurrentlyCovered names, for each implemented service, the stress test that
// drives it from many goroutines at once under -race.
//
// This is the ratchet. Both races that shipped this session were in state no
// test exercised concurrently, and the detector cannot report what never runs
// in parallel. An eighteenth service must join one of these two tests rather
// than arriving with its locking checked by reading alone.
var concurrentlyCovered = map[string]string{
	"s3":             "TestConcurrencyStress",
	"dynamodb":       "TestConcurrencyStress",
	"sqs":            "TestConcurrencyStress",
	"sns":            "TestConcurrencyStress",
	"kms":            "TestConcurrencyStress",
	"eventbridge":    "TestConcurrencyStress",
	"stepfunctions":  "TestConcurrencyStress",
	"sts":            "TestConcurrencyStressRest",
	"ssm":            "TestConcurrencyStressRest",
	"secretsmanager": "TestConcurrencyStressRest",
	"logs":           "TestConcurrencyStressRest",
	"cloudwatch":     "TestConcurrencyStressRest",
	"kinesis":        "TestConcurrencyStressRest",
	"iam":            "TestConcurrencyStressRest",
	"cloudformation": "TestConcurrencyStressRest",
	"apigateway":     "TestConcurrencyStressRest",
	"lambda":         "TestConcurrencyStressRest",
}

func TestEveryServiceHasConcurrentCoverage(t *testing.T) {
	for _, name := range dozeaws.Implemented {
		if _, ok := concurrentlyCovered[name]; !ok {
			t.Errorf("service %q has no concurrent -race coverage: add it to "+
				"TestConcurrencyStress or TestConcurrencyStressRest, then list it "+
				"in concurrentlyCovered", name)
		}
	}
	for name := range concurrentlyCovered {
		if !slices.Contains(dozeaws.Implemented, name) {
			t.Errorf("concurrentlyCovered lists %q, which is not implemented", name)
		}
	}
}
