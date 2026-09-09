package provision_test

// The first test in provision.
//
// The package is 5,113 lines across 25 files and had no test of its own.
// Everything that reached it did so sideways, through
// cloudformation/*_apply_test.go, which deploy a template and therefore only
// ever exercise the paths the transpiler can emit — and only the apply
// direction. Measured that way the package sits at 59.4%, but the shape of
// the gap matters more than the number: apply reaches 15 of 18 service
// appliers, export 7 of 13, destroy 6 of 15.
//
// Worse, destroy.go's header declares two properties load-bearing:
//
//	It is TOLERANT. A resource that is already gone is not an error.
//	It does not stop at the first failure ... and reports precisely what it
//	could not [remove].
//
// Both were at zero. record()'s "absent" and "failed" arms never ran in any
// test in the repository, Counts() never returned a non-zero absent or
// failed, and Failures() had never returned a non-empty slice. No test
// anywhere even calls Counts() or Failures() on a DestroyReport — the two
// CloudFormation tests that destroy capture the report into a variable used
// only inside a t.Fatalf message.
//
// These tests construct a provision.Stack directly, which nothing did
// before: every path in was through Transpile, so any Stack shape the
// transpiler cannot emit was unreachable.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/provision"
)

// liveStack boots an in-process stack and returns its handler plus a config
// for talking to it. provision speaks to the gateway through ServeHTTP with
// no listener, so this costs a temp dir and nothing else.
func liveStack(t *testing.T, services ...string) (http.Handler, aws.Config, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("boots a stack")
	}
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := st.Handler()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	cfg := aws.Config{Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, "")}
	return h, cfg, ts.URL
}

// primitivesStack is a Stack of the resources the CloudFormation tests never
// carry, which is why their appliers measured 3-9%: KMS keys, secrets and
// parameters, plus a queue with tags (tagList had never run).
func primitivesStack() *provision.Stack {
	return &provision.Stack{
		Queues: map[string]provision.Queue{
			"orders": {Visibility: 45, Tags: map[string]string{"team": "core"}},
		},
		Keys: map[string]provision.Key{
			"app": {Description: "app key", Tags: map[string]string{"team": "core"}},
		},
		Secrets: map[string]provision.Secret{
			"db": {Value: "s3cret", Description: "db password", Tags: map[string]string{"team": "core"}},
		},
		Parameters: map[string]provision.Parameter{
			"/app/stage": {Value: "prod", Type: "String", Tags: map[string]string{"team": "core"}},
		},
	}
}

// TestApplyAndDestroyPrimitives: the three appliers no template reaches, and
// the destroy phases that go with them.
func TestApplyAndDestroyPrimitives(t *testing.T) {
	ctx := context.Background()
	h, cfg, url := liveStack(t)
	sf := primitivesStack()

	rep, err := provision.Apply(ctx, h, sf)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	created, _, _ := rep.Counts()
	if created != 4 {
		t.Fatalf("want four resources created, got %d: %+v", created, rep.Actions)
	}

	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
	kmsC := awskms.NewFromConfig(cfg, func(o *awskms.Options) { o.BaseEndpoint = aws.String(url) })
	smC := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	ssmC := awsssm.NewFromConfig(cfg, func(o *awsssm.Options) { o.BaseEndpoint = aws.String(url) })

	q, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")})
	if err != nil {
		t.Fatalf("the queue was not created: %v", err)
	}
	// A key is reachable by the alias apply gives it.
	created1, err := kmsC.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String("alias/app")})
	if err != nil {
		t.Fatalf("the key was not created under alias/app: %v", err)
	}
	keyID := aws.ToString(created1.KeyMetadata.KeyId)
	sv, err := smC.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")})
	if err != nil || aws.ToString(sv.SecretString) != "s3cret" {
		t.Fatalf("the secret was not created: %v %v", sv, err)
	}
	pv, err := ssmC.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/stage")})
	if err != nil || aws.ToString(pv.Parameter.Value) != "prod" {
		t.Fatalf("the parameter was not created: %v %v", pv, err)
	}

	// Applying again converges: nothing is created twice.
	rep2, err := provision.Apply(ctx, h, sf)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if created2, _, skipped2 := rep2.Counts(); created2 != 0 || skipped2 == 0 {
		t.Errorf("a second apply must skip, not recreate: created=%d skipped=%d %+v", created2, skipped2, rep2.Actions)
	}

	// Destroy removes all four.
	drep, err := provision.Destroy(ctx, h, sf)
	if err != nil {
		t.Fatalf("Destroy: %v — %+v", err, drep.Actions)
	}
	deleted, absent, failed := drep.Counts()
	if deleted != 4 || absent != 0 || failed != 0 {
		t.Fatalf("destroy counts = deleted %d absent %d failed %d: %+v", deleted, absent, failed, drep.Actions)
	}
	if _, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")}); err == nil {
		t.Error("the queue survived Destroy")
	}
	if _, err := ssmC.GetParameter(ctx, &awsssm.GetParameterInput{Name: aws.String("/app/stage")}); err == nil {
		t.Error("the parameter survived Destroy")
	}
	if _, err := smC.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")}); err == nil {
		t.Error("the secret survived Destroy")
	}
	// The key is the one that has to be checked on the resource rather than
	// in the report: scheduling it by an alias that had already been deleted
	// answered NotFound, which reads as "absent", so Destroy reported the key
	// removed while it was still live. A KMS key is scheduled, not deleted,
	// so the state is what says it worked.
	kd, err := kmsC.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("DescribeKey after Destroy: %v", err)
	}
	if kd.KeyMetadata.KeyState != kmstypes.KeyStatePendingDeletion {
		t.Errorf("the key is %s after Destroy, want PendingDeletion — it was never scheduled", kd.KeyMetadata.KeyState)
	}
	if _, err := kmsC.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String("alias/app")}); err == nil {
		t.Error("the alias survived Destroy")
	}
	_ = q
}

// TestDestroyIsTolerantOfAbsentResources is the first property in destroy.go's
// header, and it had never been exercised: record()'s notFound arm was dead,
// and Counts() had never reported a non-zero "absent".
func TestDestroyIsTolerantOfAbsentResources(t *testing.T) {
	ctx := context.Background()
	h, _, _ := liveStack(t)

	// Nothing was ever applied: every resource is already in its goal state.
	drep, err := provision.Destroy(ctx, h, primitivesStack())
	if err != nil {
		t.Fatalf("destroying a stack that was never applied must succeed: %v", err)
	}
	deleted, absent, failed := drep.Counts()
	if failed != 0 {
		t.Fatalf("a resource that is already gone is not a failure: %+v", drep.Failures())
	}
	if absent == 0 {
		t.Fatalf("nothing was reported absent, so the tolerant path did not run: deleted=%d %+v", deleted, drep.Actions)
	}
	if len(drep.Failures()) != 0 {
		t.Errorf("Failures() = %+v, want none", drep.Failures())
	}
}

// TestDestroyIsIdempotent: the same Destroy twice. The second is entirely the
// absent path.
func TestDestroyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h, _, _ := liveStack(t)
	sf := primitivesStack()
	if _, err := provision.Apply(ctx, h, sf); err != nil {
		t.Fatal(err)
	}
	if _, err := provision.Destroy(ctx, h, sf); err != nil {
		t.Fatal(err)
	}
	drep, err := provision.Destroy(ctx, h, sf)
	if err != nil {
		t.Fatalf("a second Destroy must be a no-op, not an error: %v", err)
	}
	if _, absent, failed := drep.Counts(); absent == 0 || failed != 0 {
		t.Errorf("second destroy: absent=%d failed=%d %+v", absent, failed, drep.Actions)
	}
}

// brokenGateway answers a chosen path family with a 500 and passes everything
// else through. A real service will not fail on demand, so this is the only
// way to reach the failure arm.
type brokenGateway struct {
	inner http.Handler
	fail  func(*http.Request) bool
}

func (b brokenGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.fail(r) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(500)
		w.Write([]byte(`{"__type":"InternalFailure","message":"the service is unwell"}`))
		return
	}
	b.inner.ServeHTTP(w, r)
}

// TestDestroyContinuesPastAFailureAndReportsIt is the second property in
// destroy.go's header — "does not stop at the first failure ... reports
// precisely what it could not" — and the one with no coverage at all.
// Failures() had never returned a non-empty slice in any test run.
func TestDestroyContinuesPastAFailureAndReportsIt(t *testing.T) {
	ctx := context.Background()
	h, cfg, url := liveStack(t)
	sf := primitivesStack()
	if _, err := provision.Apply(ctx, h, sf); err != nil {
		t.Fatal(err)
	}

	// Break parameter deletion only. Parameters are destroyed in phase four
	// of fifteen, so queues, keys and secrets all come after it: if Destroy
	// stopped at the first failure they would survive.
	broken := brokenGateway{inner: h, fail: func(r *http.Request) bool {
		return strings.Contains(r.Header.Get("X-Amz-Target"), "DeleteParameter")
	}}

	drep, err := provision.Destroy(ctx, broken, sf)
	if err == nil {
		t.Fatal("a failed deletion must be reported as an error")
	}
	if !strings.Contains(err.Error(), "could not be removed") {
		t.Errorf("the error should say what happened, got %v", err)
	}

	failures := drep.Failures()
	if len(failures) != 1 {
		t.Fatalf("Failures() = %+v, want exactly the parameter", failures)
	}
	if !strings.Contains(failures[0].Resource, "app/stage") {
		t.Errorf("the failure must name the resource, got %q", failures[0].Resource)
	}
	if failures[0].Detail == "" {
		t.Error("the failure must carry why it failed")
	}
	if _, _, failed := drep.Counts(); failed != 1 {
		t.Errorf("Counts() failed = %d, want 1", failed)
	}

	// The walk continued: everything in a later phase is gone.
	sqsC := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(url) })
	if _, err := sqsC.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("orders")}); err == nil {
		t.Error("destroy stopped at the first failure: the queue, a later phase, survived")
	}
	smC := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })
	if _, err := smC.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")}); err == nil {
		t.Error("destroy stopped at the first failure: the secret, a later phase, survived")
	}
}

// TestSecretForceGovernsOverwrite: Force is a field only a hand-written
// Stack sets, so the transpiler never produced it and neither arm ran.
func TestSecretForceGovernsOverwrite(t *testing.T) {
	ctx := context.Background()
	h, cfg, url := liveStack(t)
	smC := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(url) })

	sf := &provision.Stack{Secrets: map[string]provision.Secret{"db": {Value: "first"}}}
	if _, err := provision.Apply(ctx, h, sf); err != nil {
		t.Fatal(err)
	}

	// Without Force, a live value is left alone: the running value wins,
	// because a stack file should not silently clobber a rotated secret.
	sf.Secrets["db"] = provision.Secret{Value: "second"}
	rep, err := provision.Apply(ctx, h, sf)
	if err != nil {
		t.Fatal(err)
	}
	sv, _ := smC.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")})
	if aws.ToString(sv.SecretString) != "first" {
		t.Errorf("without Force the live value must stand, got %q", aws.ToString(sv.SecretString))
	}
	if _, _, skipped := rep.Counts(); skipped == 0 {
		t.Errorf("the untouched secret should be reported skipped: %+v", rep.Actions)
	}

	// With Force, it is overwritten.
	sf.Secrets["db"] = provision.Secret{Value: "second", Force: true}
	if _, err := provision.Apply(ctx, h, sf); err != nil {
		t.Fatal(err)
	}
	sv, _ = smC.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("db")})
	if aws.ToString(sv.SecretString) != "second" {
		t.Errorf("with Force the file wins, got %q", aws.ToString(sv.SecretString))
	}
}
