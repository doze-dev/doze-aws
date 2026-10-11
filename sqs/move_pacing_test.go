package sqs

// A move task is a background task: RUNNING while its messages are on their way,
// at the rate it was asked for, cancellable, one at a time per queue, and gone
// with the service. The rest of the suite runs the pacing at full speed; these
// set it back to something a person can observe.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func pace(t *testing.T, start time.Duration, rate int, tick time.Duration) {
	t.Helper()
	ds, dr, dt := moveStartDelay, moveDefaultRate, moveTick
	moveStartDelay, moveDefaultRate, moveTick = start, rate, tick
	t.Cleanup(func() { moveStartDelay, moveDefaultRate, moveTick = ds, dr, dt })
}

func parkInDLQ(t *testing.T, f *redriveFixture, n int) {
	t.Helper()
	f.create("orders", redrivePolicyTo(f, "dlq"))
	for i := 0; i < n; i++ {
		f.deadLetter("orders", "m")
	}
}

func listTasks(t *testing.T, f *redriveFixture) []awssqs.ListMessageMoveTasksOutput {
	t.Helper()
	out, err := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"]), MaxResults: aws.Int32(5)})
	if err != nil {
		t.Fatal(err)
	}
	return []awssqs.ListMessageMoveTasksOutput{*out}
}

func dlqDepth(t *testing.T, f *redriveFixture) string {
	t.Helper()
	a, err := f.c.GetQueueAttributes(f.ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(f.urls["dlq"]),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages}})
	if err != nil {
		t.Fatal(err)
	}
	return a.Attributes["ApproximateNumberOfMessages"]
}

func TestAMoveIsRunningFirstAndThenDone(t *testing.T) {
	pace(t, 300*time.Millisecond, 1000, 20*time.Millisecond)
	f := newRedriveFixture(t, "dlq")
	parkInDLQ(t, f, 3)

	task, err := f.c.StartMessageMoveTask(f.ctx, startMove(f.arns["dlq"]))
	if err != nil {
		t.Fatal(err)
	}
	// Straight after the start: running, nothing moved, the messages still here.
	r := listTasks(t, f)[0].Results
	if len(r) != 1 || aws.ToString(r[0].Status) != "RUNNING" || r[0].ApproximateNumberOfMessagesMoved != 0 {
		t.Fatalf("straight after the start: %+v", r)
	}
	if aws.ToString(r[0].TaskHandle) != aws.ToString(task.TaskHandle) {
		t.Errorf("a running task lists its handle: got %q, want %q", aws.ToString(r[0].TaskHandle), aws.ToString(task.TaskHandle))
	}
	if got := dlqDepth(t, f); got != "3" {
		t.Errorf("the dead-letter queue holds %s straight after the start, want all 3", got)
	}

	f.waitForMoves(f.arns["dlq"])
	r = listTasks(t, f)[0].Results
	if aws.ToString(r[0].Status) != "COMPLETED" || r[0].ApproximateNumberOfMessagesMoved != 3 || r[0].TaskHandle != nil {
		t.Errorf("finished: %+v", r[0])
	}
	if got := dlqDepth(t, f); got != "0" {
		t.Errorf("the dead-letter queue holds %s once done", got)
	}
}

func TestAMoveRunsAtTheRateAsked(t *testing.T) {
	pace(t, 0, 500, 20*time.Millisecond)
	f := newRedriveFixture(t, "dlq")
	parkInDLQ(t, f, 30)

	// 20 a second: 30 messages take about a second and a half.
	start := time.Now()
	if _, err := f.c.StartMessageMoveTask(f.ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(f.arns["dlq"]), MaxNumberOfMessagesPerSecond: aws.Int32(20)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	r := listTasks(t, f)[0].Results
	if aws.ToString(r[0].Status) != "RUNNING" || r[0].ApproximateNumberOfMessagesMoved < 4 || r[0].ApproximateNumberOfMessagesMoved > 16 {
		t.Errorf("half a second in, at 20 a second: %+v", r[0])
	}
	f.waitForMoves(f.arns["dlq"])
	if took := time.Since(start); took < 1200*time.Millisecond {
		t.Errorf("30 messages at 20 a second took %v", took)
	}
}

func TestACancelStopsARunningMove(t *testing.T) {
	pace(t, 0, 500, 20*time.Millisecond)
	f := newRedriveFixture(t, "dlq")
	parkInDLQ(t, f, 40)
	task, err := f.c.StartMessageMoveTask(f.ctx, &awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(f.arns["dlq"]), MaxNumberOfMessagesPerSecond: aws.Int32(10)})
	if err != nil {
		t.Fatal(err)
	}
	// One at a time per queue.
	if _, err := f.c.StartMessageMoveTask(f.ctx, startMove(f.arns["dlq"])); err == nil {
		t.Error("a second task was started on a queue with one running")
	}
	time.Sleep(400 * time.Millisecond)
	out, err := f.c.CancelMessageMoveTask(f.ctx, &awssqs.CancelMessageMoveTaskInput{TaskHandle: task.TaskHandle})
	if err != nil {
		t.Fatal(err)
	}
	moved := out.ApproximateNumberOfMessagesMoved
	if moved < 1 || moved >= 40 {
		t.Errorf("a cancel after 400ms at 10 a second reports %d moved", moved)
	}
	r := listTasks(t, f)[0].Results
	if aws.ToString(r[0].Status) != "CANCELLED" || r[0].ApproximateNumberOfMessagesMoved != moved || r[0].TaskHandle != nil {
		t.Errorf("after the cancel: %+v", r[0])
	}
	if got := dlqDepth(t, f); got != strconv.Itoa(int(40-moved)) {
		t.Errorf("the dead-letter queue holds %s after moving %d of 40", got, moved)
	}
	// Cancelling again, or a handle that never was, is refused: it is not running.
	if _, err := f.c.CancelMessageMoveTask(f.ctx, &awssqs.CancelMessageMoveTaskInput{TaskHandle: task.TaskHandle}); err == nil {
		t.Error("a task that was already cancelled was cancelled again")
	}
	// And a new one may start.
	if _, err := f.move(startMove(f.arns["dlq"])); err != nil {
		t.Errorf("a new task after the cancel: %v", err)
	}
	if got := dlqDepth(t, f); got != "0" {
		t.Errorf("the second task left %s", got)
	}
}

func TestClosingTheServiceStopsAMove(t *testing.T) {
	pace(t, 0, 500, 20*time.Millisecond)
	s := testStore(t)
	if _, err := s.CreateQueue("dlq", nil, nil); err != nil {
		t.Fatal(err)
	}
	arn := s.queueARN("dlq")
	if _, err := s.CreateQueue("src", map[string]string{"RedrivePolicy": `{"deadLetterTargetArn":"` + arn + `","maxReceiveCount":1}`}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := s.Send("dlq", "m", nil, -1, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.StartMessageMoveTask("dlq", "src", 1); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.moves.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stopping the service did not stop a running move")
	}
	if _, err := s.StartMessageMoveTask("dlq", "src", 1); err == nil {
		t.Error("a move was started on a service that is stopping")
	}
}

var _ = context.Background
