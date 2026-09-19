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
// Goroutines are the sensitive one, and the reliable one: a count is exact,
// there is no noise to allow for, and "one goroutine per request, never
// reaped" is a real failure mode that a 300ms test cannot see.
//
// Heap is the coarse one, and the band is wide on purpose. A Go heap after GC
// still moves several percent between runs, so the threshold has to sit above
// that — which means this catches a leak measured in megabytes and NOT a map
// that grew two thousand small entries. Those three audit bugs would each be a
// few hundred kilobytes here, comfortably inside the noise. Pretending this
// covers it would be worse than saying it does not.
//
// Disk is the tightest of the three, because it is the only one that actually
// plateaus: bbolt reuses freed pages, so this workload settles on a size and
// stays there — the same number at 150 rounds and 300, and again at 600 and
// 1,200. Zero growth, so the allowance can be four pages instead of a
// fraction. It answers a question neither of the others can: heap says what
// the process is holding, and disk says what it LEFT BEHIND. Making an S3
// delete a no-op moves disk by 1,759 bytes a round and moves the heap check by
// 47, which is nowhere near firing.
//
// The way to close it, for whoever takes it on: runtime.MemProfile is stdlib
// and reports live bytes per ALLOCATION STACK. Snapshot at N rounds and at 2N,
// key by stack, and a site whose live bytes grew with the round count is named
// by file and line — four hundred kilobytes is invisible in a five-megabyte
// total and obvious as a doubling at one call site. The costs are that
// runtime.MemProfileRate has to come down from its 512 KiB default to see
// allocations this small, which taxes the whole run, and that "grew with the
// round count" needs a threshold that will not fire on caches which are
// supposed to fill.

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
	"github.com/doze-dev/doze-aws/internal/lightness"
)

// rounds is the measurement point; the test runs 2×rounds in total.
//
// The default is sized for the normal suite. A round is seven calls, two of
// which create and destroy a bbolt-backed queue and so pay an fsync, and the
// cost of that is almost entirely the platform's: about 55ms on macOS, where
// fsync means F_FULLFSYNC, against about 4ms on the Linux runners — 8,000
// rounds take half a minute in CI and would take seven minutes here.
//
// The goroutine half, which is the sensitive half, is just as conclusive at 150
// as at 4,000. The heap half is not: a slow leak needs a long run to climb out
// of the noise, which is what the nightly is for.
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
	diskBytes  int64
}

// measure forces two collections before reading. One is not enough: the first
// can queue finalisers whose objects are only freed by the second, and reading
// after a single GC reports garbage as live.
func measure(t *testing.T, dir string) sample {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	disk, _, err := lightness.DirBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	return sample{goroutines: runtime.NumGoroutine(), heapBytes: m.HeapAlloc, diskBytes: disk}
}

func (s sample) String() string {
	return fmt.Sprintf("%d goroutines, %.1f MiB heap, %d bytes on disk",
		s.goroutines, float64(s.heapBytes)/(1<<20), s.diskBytes)
}

func TestResourcesStayBoundedUnderRepeatedUse(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a stack through hundreds of create/use/delete rounds")
	}
	rounds := roundCount(t)
	dir := t.TempDir()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  dir,
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
	base := measure(t, dir)
	for i := rounds; i < 2*rounds; i++ {
		round(i)
	}
	grown := measure(t, dir)

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

	// Heap: growth is allowed up to whichever is larger — a fixed slack, so a
	// small baseline does not make the comparison jumpy, or a fraction of the
	// baseline, so a large one is not held to an unreasonable absolute number.
	//
	// This replaces a check that could never fire. It read
	//
	//	const heapFloor = 8 << 20
	//	if grown.heapBytes > heapFloor && grown.heapBytes > base.heapBytes*3/2
	//
	// which demanded the heap be over 8 MiB BEFORE it would look at the ratio.
	// The measured heap is four to five megabytes, so the second half was never
	// evaluated and the assertion was decorative at every scale it runs at — it
	// would have sat there passing next to any amount of growth. A floor meant
	// to stop a small baseline being jumpy has to WIDEN THE ALLOWED GROWTH, not
	// switch the check off below a size.
	const minSlack = 1 << 20
	allowed := base.heapBytes / 2
	if allowed < minSlack {
		allowed = minSlack
	}

	// This test asks "does it grow?" and CANNOT ask "how big is it?" — the
	// comparison is N rounds against 2N inside a single run, so the starting
	// point is free to move. Verified rather than reasoned: retaining 5 MiB in
	// sqs.New takes the baseline from 3.8 MiB to 8.8 and this test still
	// passes, printing the doubled figure in the line below without a single
	// assertion firing.
	//
	// Making it absolute does not work either, because the measurement point
	// moves with DOZE_BOUND_ROUNDS and one number would be wrong at one of the
	// two scales. So the absolute half lives in footprint_test.go, which boots
	// the same sqs+s3 shape in its own process and holds it to a ceiling. The
	// two together cover the question; neither does alone.
	//
	// Logged on every run, passing or not. Bytes-per-round is comparable across
	// scales in a way that two absolute figures are not, and a number nobody
	// can see is a number nobody notices moving.
	//
	// It read +208 a round when it was first logged, and it was a real leak:
	// every queue that was received from and then deleted stranded a wakeup
	// channel in SQS's long-poll notifier, because a SUCCESSFUL receive removed
	// its registration on neither of the two paths that remove one. See
	// sqs/notify_leak_test.go. Fixed, and this now reads about -7, which is
	// noise around zero.
	//
	// Worth noting which half of this file did that. The assertion below did
	// not fire and should not have: a few hundred bytes a round is nowhere near
	// any defensible band. The LOG LINE found it — a number visible on every
	// run, consistent across two platforms and twenty-six times the scale, was
	// enough to be worth attributing, and runtime.MemProfile named the site
	// from there. A bound catches what is gross; a published number catches
	// what is small and steady.
	perRound := float64(int64(grown.heapBytes)-int64(base.heapBytes)) / float64(rounds)
	t.Logf("heap moved %+.0f bytes per round across the second %d rounds", perRound, rounds)

	if grown.heapBytes > base.heapBytes+allowed {
		t.Errorf("heap grew with use: %s after %d rounds, %s after %d (%+.0f bytes "+
			"per round, %d allowed in total) — the live set is identical at both "+
			"points, so this is retention",
			base, rounds, grown, 2*rounds, perRound, allowed)
	}

	// Disk: the tightest of the three, because it is the one that actually
	// plateaus.
	//
	// bbolt never truncates. It grows the file when it needs pages and then
	// reuses freed ones, so a steady-state workload settles at a size and stays
	// there — measured, not assumed: this reads exactly the same number at 150
	// rounds and at 300, and again at 600 and 1,200. Zero growth, not "a bit".
	//
	// So growth here is not page churn, it is something the workload created
	// and did not remove: an S3 object whose delete leaves the blob, a logs
	// retention sweeper that never runs, SQS tombstones accumulating. Each of
	// those is proportional to the round count, which is exactly what comparing
	// N against 2N sees.
	//
	// The slack is four pages rather than a byte count, because bbolt allocates
	// in pages and a page is 4 KiB on the Linux runners and 16 KiB on an Apple
	// laptop — a fixed number would be four times tighter on one than the
	// other without saying so. Four pages is generous against a measured zero
	// and still an order of magnitude under what a real leak produces: making
	// s3store.DeleteObject a no-op grows this by 263,794 bytes, about 1.7 KiB
	// a round, against a 64 KiB allowance here and 16 KiB on Linux.
	diskSlack := int64(4 * os.Getpagesize())
	diskPerRound := float64(grown.diskBytes-base.diskBytes) / float64(rounds)
	t.Logf("disk moved %+.0f bytes per round across the second %d rounds", diskPerRound, rounds)

	if grown.diskBytes > base.diskBytes+diskSlack {
		t.Errorf("the data directory grew with use: %d bytes after %d rounds, "+
			"%d after %d (%+.0f bytes per round, %d allowed).\n"+
			"  The live set is identical at both points, so something a round "+
			"creates is not being\n  removed. bbolt reuses freed pages, so this "+
			"is not the file growing — it is content\n  staying in it, or a blob "+
			"a delete did not unlink.",
			base.diskBytes, rounds, grown.diskBytes, 2*rounds, diskPerRound, diskSlack)
	}

	dozetest.NoFaults(t, st)
}
