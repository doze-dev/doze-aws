package dozeaws_test

// Where do the 208 bytes a round go?
//
// TestResourcesStayBoundedUnderRepeatedUse reports retention linear in work —
// +208 bytes per round on macOS at 150/300 rounds, ~210 in CI at 4,000/8,000 —
// over a workload whose live set is identical at both measurement points. The
// total is far too small to trip any sane band and far too consistent to be
// noise. This names the allocation sites responsible.
//
// runtime.MemProfile reports live bytes per allocation STACK, so the same
// snapshot-at-N-and-2N comparison that is invisible in a five-megabyte total is
// obvious as growth at one call site. MemProfileRate has to come down from its
// 512 KiB default or allocations this small are never sampled; rate 1 profiles
// every allocation exactly and costs a large constant factor, which is why this
// is opt-in rather than part of the suite.
//
//	DOZE_HEAP_ATTRIB=1 go test -run TestWhereTheHeapGoes -v -timeout 30m .

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
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

// liveBySite reads the in-use bytes of every allocation site, keyed by its
// stack rendered as text.
func liveBySite() map[string]int64 {
	runtime.GC()
	runtime.GC()
	var recs []runtime.MemProfileRecord
	n, _ := runtime.MemProfile(nil, false)
	for {
		recs = make([]runtime.MemProfileRecord, n+64)
		var ok bool
		n, ok = runtime.MemProfile(recs, false)
		if ok {
			recs = recs[:n]
			break
		}
	}
	out := make(map[string]int64, len(recs))
	for i := range recs {
		site := renderStack(recs[i].Stack())
		// The probe's own strings are live between the two snapshots and grow
		// with the number of sites, so unfiltered it reports ITSELF as the
		// largest grower by an order of magnitude — which is what the first
		// run did, at 2,810 bytes a round against the 257 that mattered.
		if strings.Contains(site, "heapattrib_test.go") {
			continue
		}
		out[site] += recs[i].InUseBytes()
	}
	return out
}

// renderStack names the first frames that are not the allocator.
//
// Every stack starts with mallocgc and whatever runtime helper called it — for
// a map it is six frames of internal/runtime/maps before the caller appears.
// Rendering the top six verbatim produced a report whose largest entry was
// "a map grew" with no hint whose, and it silently defeated the filter below,
// since a stack of pure runtime frames never mentions this file.
func renderStack(pcs []uintptr) string {
	frames := runtime.CallersFrames(pcs)
	var b strings.Builder
	shown := 0
	for shown < 5 {
		f, more := frames.Next()
		if f.Function == "" {
			break
		}
		if b.Len() == 0 && (strings.HasPrefix(f.Function, "runtime.") ||
			strings.HasPrefix(f.Function, "internal/runtime/")) {
			if !more {
				break
			}
			continue // still inside the allocator; the caller is what matters
		}
		fmt.Fprintf(&b, "%s\n    %s:%d\n", f.Function, f.File, f.Line)
		shown++
		if !more {
			break
		}
	}
	if b.Len() == 0 {
		return "(runtime internals only)"
	}
	return b.String()
}

func TestWhereTheHeapGoes(t *testing.T) {
	if os.Getenv("DOZE_HEAP_ATTRIB") == "" {
		t.Skip("set DOZE_HEAP_ATTRIB=1; profiles every allocation, so it is slow")
	}
	// Must be set before the stack is built, or its own allocations are
	// sampled at the old rate and the baseline is a guess.
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

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

	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("attrib")}); err != nil {
		t.Fatal(err)
	}

	// The same steady-state round the bounds test drives: create, use, delete,
	// under a name never used before. Anything retained afterwards is the
	// thing being hunted.
	round := func(i int) {
		name := fmt.Sprintf("attrib-%d", i)
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
			Bucket: aws.String("attrib"), Key: aws.String(key),
			Body: strings.NewReader(name)}); err != nil {
			t.Fatalf("round %d: PutObject: %v", i, err)
		}
		if _, err := s3c.DeleteObject(ctx, &awss3.DeleteObjectInput{
			Bucket: aws.String("attrib"), Key: aws.String(key)}); err != nil {
			t.Fatalf("round %d: DeleteObject: %v", i, err)
		}
	}

	const n = 120
	for i := 0; i < n; i++ {
		round(i)
	}
	before := liveBySite()
	for i := n; i < 2*n; i++ {
		round(i)
	}
	after := liveBySite()

	type growth struct {
		site  string
		delta int64
	}
	var grew []growth
	for site, b := range after {
		if d := b - before[site]; d > 0 {
			grew = append(grew, growth{site, d})
		}
	}
	sort.Slice(grew, func(i, j int) bool { return grew[i].delta > grew[j].delta })

	var total int64
	for _, g := range grew {
		total += g.delta
	}
	t.Logf("live heap grew %d bytes across %d rounds (%.0f per round) at %d sites",
		total, n, float64(total)/float64(n), len(grew))

	for i, g := range grew {
		if i == 12 {
			break
		}
		t.Logf("\n#%d  +%d bytes  (%.0f per round)\n%s",
			i+1, g.delta, float64(g.delta)/float64(n), g.site)
	}
}
