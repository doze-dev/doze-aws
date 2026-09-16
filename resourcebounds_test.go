package dozeaws_test

// Does a stack that has been USED cost more than a stack that has been used
// half as much?
//
// Every other test in this repo asks whether an operation is correct.
// `idleprobe_test.go` measures footprint but says of itself "a probe, not an
// assertion", and it measures an IDLE stack — one that has done nothing. Nothing
// asserted that resources stay bounded under use, and three of the thirteen
// bugs the audit found were exactly that: an authorizer cache, an SQS notify
// map, and an S3 `vhostWarned` set, each growing forever.
//
// The workload here is deliberately steady-state: every round creates a
// resource, uses it, and deletes it. The live set after round 2,000 is
// identical to the live set after round 1,000, so any difference between the
// two measurements is something the stack kept that it had no reason to keep.
//
// # What this catches, and what it does not
//
// Goroutines are the sensitive half, and the reliable one: a count is exact,
// there is no noise to allow for, and "one goroutine per request, never
// reaped" is a real failure mode that a 300ms test cannot see.
//
// Heap is the coarse half, and the band is wide on purpose. A Go heap after GC
// still moves several percent between runs, so the threshold has to sit above
// that — which means this catches a leak measured in megabytes and NOT a map
// that grew two thousand small entries. Those three audit bugs would each be a
// few hundred kilobytes here, comfortably inside the noise. Closing that gap
// needs a direct probe of the specific container per service, which is a
// different piece of work; pretending this covers it would be worse than
// saying it does not.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// rounds is the measurement point; the test runs 2×rounds in total.
//
// The default is sized for the normal suite. A round costs about 55ms — it is
// seven calls, two of which create and destroy a bbolt-backed queue and so pay
// an fsync — and the goroutine half, which is the sensitive half, is just as
// conclusive at 150 as at 4,000. The heap half is not: a slow leak needs a long
// run to climb out of the noise, which is what the nightly is for.
//
//	DOZE_BOUND_ROUNDS=4000 go test -run TestResourcesStayBounded -timeout 30m .
func roundCount(t *testing.T) int {
	if v := os.Getenv("DOZE_BOUND_ROUNDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("DOZE_BOUND_ROUNDS=%q is not a positive number", v)
		}
		return n
	}
	return 150
}

type sample struct {
	goroutines int
	heapBytes  uint64
}

// measure forces two collections before reading. One is not enough: the first
// can queue finalisers whose objects are only freed by the second, and reading
// after a single GC reports garbage as live.
func measure() sample {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return sample{goroutines: runtime.NumGoroutine(), heapBytes: m.HeapAlloc}
}

func (s sample) String() string {
	return fmt.Sprintf("%d goroutines, %.1f MiB heap", s.goroutines, float64(s.heapBytes)/(1<<20))
}

func TestResourcesStayBoundedUnderRepeatedUse(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a stack through hundreds of create/use/delete rounds")
	}
	rounds := roundCount(t)
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  t.TempDir(),
		Services: []string{"sqs", "s3"},
		Logf:     dozetest.Quiet(t),
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
	sqsc := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) { o.BaseEndpoint = ep })
	s3c := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = ep; o.UsePathStyle = true })
	ctx := context.Background()

	// One bucket for the whole run: creating 800 of them would be legitimate
	// growth and would measure the wrong thing.
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("bounded")}); err != nil {
		t.Fatal(err)
	}

	// Each round leaves nothing behind. Names are distinct per round on
	// purpose — a cache keyed by name only grows if it is asked about names it
	// has not seen, so reusing one name would hide the bug class being looked
	// for.
	round := func(i int) {
		name := fmt.Sprintf("bounded-%d", i)
		q, err := sqsc.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name)})
		if err != nil {
			t.Fatalf("round %d: CreateQueue: %v", i, err)
		}
		if _, err := sqsc.SendMessage(ctx, &awssqs.SendMessageInput{
			QueueUrl: q.QueueUrl, MessageBody: aws.String(name)}); err != nil {
			t.Fatalf("round %d: SendMessage: %v", i, err)
		}
		recv, err := sqsc.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl: q.QueueUrl, MaxNumberOfMessages: 1})
		if err != nil {
			t.Fatalf("round %d: ReceiveMessage: %v", i, err)
		}
		for _, m := range recv.Messages {
			if _, err := sqsc.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl: q.QueueUrl, ReceiptHandle: m.ReceiptHandle}); err != nil {
				t.Fatalf("round %d: DeleteMessage: %v", i, err)
			}
		}
		if _, err := sqsc.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: q.QueueUrl}); err != nil {
			t.Fatalf("round %d: DeleteQueue: %v", i, err)
		}

		key := fmt.Sprintf("k-%d", i)
		if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String("bounded"), Key: aws.String(key),
			Body: strings.NewReader(name)}); err != nil {
			t.Fatalf("round %d: PutObject: %v", i, err)
		}
		if _, err := s3c.DeleteObject(ctx, &awss3.DeleteObjectInput{
			Bucket: aws.String("bounded"), Key: aws.String(key)}); err != nil {
			t.Fatalf("round %d: DeleteObject: %v", i, err)
		}
	}

	for i := 0; i < rounds; i++ {
		round(i)
	}
	base := measure()
	for i := rounds; i < 2*rounds; i++ {
		round(i)
	}
	grown := measure()

	t.Logf("after %d rounds: %s", rounds, base)
	t.Logf("after %d rounds: %s", 2*rounds, grown)

	// Goroutines: an exact count, so the slack is small. It is not zero because
	// the SDK's connection pool keeps idle read/write loops around and their
	// number depends on when the runtime got round to reaping them.
	const goroutineSlack = 8
	if grown.goroutines > base.goroutines+goroutineSlack {
		t.Errorf("goroutines grew with use: %d after %d rounds, %d after %d — "+
			"a steady-state workload should not accumulate any",
			base.goroutines, rounds, grown.goroutines, 2*rounds)
	}

	// Heap: a ratio, generously banded, for the reasons at the top of the file.
	// The floor stops a small absolute baseline from making the ratio jumpy.
	const heapFloor = 8 << 20
	if grown.heapBytes > heapFloor && grown.heapBytes > base.heapBytes*3/2 {
		t.Errorf("heap grew with use: %s after %d rounds, %s after %d — "+
			"the live set is identical at both points, so this is retention",
			base, rounds, grown, 2*rounds)
	}

	dozetest.NoFaults(t, st)
}
