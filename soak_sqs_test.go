package dozeaws_test

// A soak for SQS redrive (plan/services/sqs.md section 9, "Redrive under load,
// and a restart mid-move"): a few thousand messages are moved out of a
// dead-letter queue while a consumer reads and deletes at the destination, the
// service is closed and reopened over the same data directory in the middle of
// the move, and the move is started again. At-least-once is what SQS promises,
// so a message may be seen twice (one in flight when the service went down comes
// back after its visibility timeout) but none may be lost, and nothing may be
// left behind in either queue. Bounded to well under a minute.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func TestSQSRedriveOfAFewThousandMessagesSurvivesARestartMidMove(t *testing.T) {
	if testing.Short() {
		t.Skip("moves a few thousand messages and restarts the service (about 15 s)")
	}
	const total = 3000
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	dir := t.TempDir()

	type up struct {
		stack *dozeaws.Stack
		ts    *httptest.Server
		sqs   *awssqs.Client
	}
	open := func() *up {
		t.Helper()
		st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: dozetest.Logf(t), Services: []string{"sqs"}})
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(st.Handler())
		return &up{stack: st, ts: ts, sqs: clientsFor(ts.URL).sqs}
	}
	closeUp := func(u *up) {
		u.ts.Close()
		u.stack.Close()
	}
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	queueARN := func(u *up, name string) string {
		return fmt.Sprintf("arn:aws:sqs:us-east-1:000000000000:%s", name)
	}

	cur := open()
	var live atomic.Pointer[up]
	live.Store(cur)
	defer func() { closeUp(live.Load()) }()

	create := func(name string, attrs map[string]string) string {
		q, err := cur.sqs.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: attrs})
		must(err, "CreateQueue "+name)
		return aws.ToString(q.QueueUrl)
	}
	dlqURL := create("soak-dlq", nil)
	create("soak-src", map[string]string{"RedrivePolicy": `{"deadLetterTargetArn":"` + queueARN(cur, "soak-dlq") + `","maxReceiveCount":5}`})
	// A short visibility timeout at the destination, so a message that was in
	// flight when the service went down is back within the test's time.
	create("soak-dest", map[string]string{"VisibilityTimeout": "2"})

	// The messages are in the dead-letter queue to begin with.
	for i := 0; i < total; i += 10 {
		var entries []sqstypes.SendMessageBatchRequestEntry
		for j := i; j < i+10; j++ {
			entries = append(entries, sqstypes.SendMessageBatchRequestEntry{Id: aws.String(fmt.Sprintf("e%d", j)), MessageBody: aws.String(fmt.Sprintf("m-%04d", j))})
		}
		out, err := cur.sqs.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: aws.String(dlqURL), Entries: entries})
		must(err, "SendMessageBatch")
		if len(out.Failed) != 0 {
			t.Fatalf("a batch failed: %+v", out.Failed)
		}
	}

	// The destination's consumer: receives, records, deletes. It reads through
	// whichever service is up, and shrugs off the gap while it restarts.
	var seenMu sync.Mutex
	seen := map[string]int{}
	stopConsumer := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			select {
			case <-stopConsumer:
				return
			default:
			}
			u := live.Load()
			q, err := u.sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("soak-dest")})
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			rec, err := u.sqs.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: q.QueueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 1})
			if err != nil || len(rec.Messages) == 0 {
				continue
			}
			var del []sqstypes.DeleteMessageBatchRequestEntry
			seenMu.Lock()
			for i, m := range rec.Messages {
				seen[aws.ToString(m.Body)]++
				del = append(del, sqstypes.DeleteMessageBatchRequestEntry{Id: aws.String(fmt.Sprint(i)), ReceiptHandle: m.ReceiptHandle})
			}
			seenMu.Unlock()
			_, _ = u.sqs.DeleteMessageBatch(ctx, &awssqs.DeleteMessageBatchInput{QueueUrl: q.QueueUrl, Entries: del})
		}
	}()

	// The move: at the most a task may ask for, 500 a second, so 3,000 take six.
	moved := func(u *up) (status string, n int64) {
		t.Helper()
		l, err := u.sqs.ListMessageMoveTasks(ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(queueARN(u, "soak-dlq"))})
		must(err, "ListMessageMoveTasks")
		if len(l.Results) == 0 {
			t.Fatal("no move task is listed")
		}
		return aws.ToString(l.Results[0].Status), l.Results[0].ApproximateNumberOfMessagesMoved
	}
	_, err := cur.sqs.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(queueARN(cur, "soak-dlq")), DestinationArn: aws.String(queueARN(cur, "soak-dest")), MaxNumberOfMessagesPerSecond: aws.Int32(500)})
	must(err, "StartMessageMoveTask")

	// Mid-move: some have moved, and not all.
	var movedBefore int64
	for {
		status, n := moved(cur)
		if status != "RUNNING" {
			t.Fatalf("the task is %s with %d moved, before the restart could be made mid-move", status, n)
		}
		if n >= 800 {
			movedBefore = n
			break
		}
		if time.Now().After(started.Add(30 * time.Second)) {
			t.Fatalf("only %d moved after 30 s", n)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The restart: an orderly close, and a new service over the same directory.
	old := cur
	live.Store(&up{stack: old.stack, ts: old.ts, sqs: old.sqs}) // the consumer's calls fail until the new one is up
	closeUp(old)
	cur = open()
	live.Store(cur)

	// The task that was running is not running: a running task lives in memory
	// (sqs/move_task.go), and what it had moved stays moved.
	status, n := moved(cur)
	if status == "RUNNING" || status == "COMPLETED" {
		t.Errorf("the task that was mid-move before the restart is %s afterwards (%d moved of %d)", status, n, total)
	}
	t.Logf("restarted with %d moved (task now %s, %d)", movedBefore, status, n)

	// Starting it again moves the rest.
	_, err = cur.sqs.StartMessageMoveTask(ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(queueARN(cur, "soak-dlq")), DestinationArn: aws.String(queueARN(cur, "soak-dest")), MaxNumberOfMessagesPerSecond: aws.Int32(500)})
	must(err, "StartMessageMoveTask after the restart")
	for {
		status, n := moved(cur)
		if status == "COMPLETED" {
			t.Logf("second task completed, %d moved", n)
			break
		}
		if status != "RUNNING" {
			t.Fatalf("the second task is %s", status)
		}
		if time.Now().After(started.Add(45 * time.Second)) {
			t.Fatalf("the second task was still running at %s", time.Since(started))
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Everything reaches the consumer.
	for {
		seenMu.Lock()
		got := len(seen)
		seenMu.Unlock()
		if got >= total {
			break
		}
		if time.Now().After(started.Add(50 * time.Second)) {
			t.Fatalf("the consumer has seen %d of %d messages at %s", got, total, time.Since(started))
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(stopConsumer)
	<-consumerDone

	seenMu.Lock()
	defer seenMu.Unlock()
	var missing []string
	dup := 0
	for i := 0; i < total; i++ {
		body := fmt.Sprintf("m-%04d", i)
		switch c := seen[body]; {
		case c == 0:
			missing = append(missing, body)
		case c > 1:
			dup += c - 1
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d messages were lost, among them %s", len(missing), strings.Join(missing[:min(5, len(missing))], ", "))
	}
	// At-least-once allows a repeat: a batch the consumer had received, and not
	// yet deleted, when the service went down comes back after two seconds.
	if dup > 20 {
		t.Errorf("%d messages were delivered twice; at most one batch in flight at the restart (10) or two could be", dup)
	}
	if len(seen) != total {
		t.Errorf("the consumer saw %d distinct bodies, want %d (something was invented)", len(seen), total)
	}

	// Nothing is left behind: the dead-letter queue is empty, and so is the destination.
	for _, name := range []string{"soak-dlq", "soak-dest"} {
		q, err := cur.sqs.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
		must(err, "GetQueueUrl "+name)
		a, err := cur.sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl,
			AttributeNames: []sqstypes.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible", "ApproximateNumberOfMessagesDelayed"}})
		must(err, "GetQueueAttributes "+name)
		if a.Attributes["ApproximateNumberOfMessages"] != "0" || a.Attributes["ApproximateNumberOfMessagesNotVisible"] != "0" {
			t.Errorf("%s still holds messages: %v", name, a.Attributes)
		}
	}
	t.Logf("%d messages, %d repeats, restart at %d moved, %s", total, dup, movedBefore, time.Since(started).Round(100*time.Millisecond))
	if time.Since(started) > 55*time.Second {
		t.Errorf("the soak took %s; it is bounded to under a minute", time.Since(started))
	}
}
