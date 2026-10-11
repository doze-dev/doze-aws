package sqs

// StartMessageMoveTask without a DestinationArn returns each message to the
// queue it came from, as AWS does (SQS-7). That needs the message to remember
// where it was dead-lettered from, which is also what AWS reports on the
// message as DeadLetterQueueSourceArn. doze-aws used to refuse the call
// because it kept no provenance.
//
// doc: the StartMessageMoveTask API reference (DestinationArn is optional:
// "if a destination is not specified, the messages are moved to their original
// source queues"; SourceArn "currently only ... dead-letter queues"), and the
// message system attribute DeadLetterQueueSourceArn.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type redriveFixture struct {
	t    *testing.T
	c    *awssqs.Client
	urls map[string]string
	arns map[string]string
	ctx  context.Context
}

func newRedriveFixture(t *testing.T, names ...string) *redriveFixture {
	t.Helper()
	f := &redriveFixture{t: t, c: sdkClient(t), urls: map[string]string{}, arns: map[string]string{}, ctx: context.Background()}
	for _, n := range names {
		f.create(n, nil)
	}
	return f
}

func (f *redriveFixture) create(name string, attrs map[string]string) {
	out, err := f.c.CreateQueue(f.ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: attrs})
	if err != nil {
		f.t.Fatalf("CreateQueue %s: %v", name, err)
	}
	f.urls[name] = aws.ToString(out.QueueUrl)
	a, err := f.c.GetQueueAttributes(f.ctx, &awssqs.GetQueueAttributesInput{QueueUrl: out.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		f.t.Fatal(err)
	}
	f.arns[name] = a.Attributes["QueueArn"]
}

// deadLetter sends a body to src and receives it twice, so a maxReceiveCount
// of 1 moves it to src's dead-letter queue.
func (f *redriveFixture) deadLetter(src, body string) {
	f.t.Helper()
	if _, err := f.c.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.urls[src]), MessageBody: aws.String(body)}); err != nil {
		f.t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.c.ReceiveMessage(f.ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(f.urls[src])}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *redriveFixture) bodies(queue string) []string {
	f.t.Helper()
	var out []string
	for i := 0; i < 5; i++ {
		r, err := f.c.ReceiveMessage(f.ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(f.urls[queue]),
			MaxNumberOfMessages: 10, VisibilityTimeout: 600})
		if err != nil {
			f.t.Fatal(err)
		}
		if len(r.Messages) == 0 {
			break
		}
		for _, m := range r.Messages {
			out = append(out, aws.ToString(m.Body))
		}
	}
	sort.Strings(out)
	return out
}

// move starts a move task and waits for it to leave RUNNING, because a task
// runs in the background: AWS answers the start with a handle and the messages
// follow. A refused start is returned as it came.
func (f *redriveFixture) move(in *awssqs.StartMessageMoveTaskInput) (*awssqs.StartMessageMoveTaskOutput, error) {
	f.t.Helper()
	out, err := f.c.StartMessageMoveTask(f.ctx, in)
	if err != nil {
		return nil, err
	}
	f.waitForMoves(aws.ToString(in.SourceArn))
	return out, nil
}

func (f *redriveFixture) waitForMoves(sourceARN string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		list, err := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(sourceARN)})
		if err != nil {
			f.t.Fatal(err)
		}
		if len(list.Results) > 0 && aws.ToString(list.Results[0].Status) != "RUNNING" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("a move task was still running after ten seconds")
}

func redrivePolicyTo(f *redriveFixture, dlq string) map[string]string {
	// A visibility timeout of 0 makes a received message visible again at once,
	// which is how a message is received "too often" inside one test.
	return map[string]string{"VisibilityTimeout": "0",
		"RedrivePolicy": `{"deadLetterTargetArn":"` + f.arns[dlq] + `","maxReceiveCount":"1"}`}
}

func TestADeadLetteredMessageSaysWhereItCameFrom(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "poison")

	r, err := f.c.ReceiveMessage(f.ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(f.urls["dlq"]),
		MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameAll}})
	if err != nil || len(r.Messages) != 1 {
		t.Fatalf("receive from the dead-letter queue: %v %v", r, err)
	}
	if got := r.Messages[0].Attributes["DeadLetterQueueSourceArn"]; got != f.arns["orders"] {
		t.Errorf("DeadLetterQueueSourceArn = %q, want %q", got, f.arns["orders"])
	}
}

func TestAMoveWithoutADestinationReturnsEachMessageToItsOwnQueue(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.create("billing", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "order-1")
	f.deadLetter("billing", "invoice-1")
	f.deadLetter("orders", "order-2")

	task, err := f.move(&awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(f.arns["dlq"])})
	if err != nil {
		t.Fatalf("a move with no destination: %v", err)
	}
	if task.TaskHandle == nil {
		t.Error("no task handle")
	}

	if got := f.bodies("orders"); len(got) != 2 || got[0] != "order-1" || got[1] != "order-2" {
		t.Errorf("orders holds %v, want its two messages back", got)
	}
	if got := f.bodies("billing"); len(got) != 1 || got[0] != "invoice-1" {
		t.Errorf("billing holds %v, want its message back", got)
	}
	if got := f.bodies("dlq"); len(got) != 0 {
		t.Errorf("the dead-letter queue still holds %v", got)
	}

	list, err := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"])})
	if err != nil || len(list.Results) != 1 {
		t.Fatalf("ListMessageMoveTasks: %v %v", list, err)
	}
	if got := aws.ToString(list.Results[0].Status); got != "COMPLETED" {
		t.Errorf("task status %q, want COMPLETED", got)
	}
	if n := list.Results[0].ApproximateNumberOfMessagesMoved; n != 3 {
		t.Errorf("moved %d, want 3", n)
	}
}

// A message nobody dead-lettered (one sent straight to the queue) has no origin
// to return to. The task says so and leaves it.
func TestAMessageWithNoKnownOriginIsLeftAndTheTaskSaysSo(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "from-orders")
	if _, err := f.c.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.urls["dlq"]), MessageBody: aws.String("sent-directly")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.move(&awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(f.arns["dlq"])}); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies("orders"); len(got) != 1 || got[0] != "from-orders" {
		t.Errorf("orders holds %v", got)
	}
	if got := f.bodies("dlq"); len(got) != 1 || got[0] != "sent-directly" {
		t.Errorf("the dead-letter queue holds %v, want the one with no origin", got)
	}
	list, _ := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"])})
	if len(list.Results) != 1 || aws.ToString(list.Results[0].Status) != "FAILED" || aws.ToString(list.Results[0].FailureReason) == "" {
		t.Errorf("the task should be FAILED with a reason: %+v", list.Results)
	}
}

// Only a dead-letter queue is a source (doc), with or without a destination.
func TestAMoveFromAQueueThatIsNoDeadLetterQueueIsRefused(t *testing.T) {
	f := newRedriveFixture(t, "plain", "elsewhere")
	for _, in := range []*awssqs.StartMessageMoveTaskInput{
		{SourceArn: aws.String(f.arns["plain"])},
		{SourceArn: aws.String(f.arns["plain"]), DestinationArn: aws.String(f.arns["elsewhere"])},
	} {
		if _, err := f.move(in); err == nil {
			t.Errorf("a move from a queue that is nobody's dead-letter queue was accepted: %+v", in)
		}
	}
}

// With a destination, nothing about the origin matters: everything goes there.
func TestAMoveWithADestinationSendsEverythingThere(t *testing.T) {
	f := newRedriveFixture(t, "dlq", "inbox")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "one")
	if _, err := f.c.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.urls["dlq"]), MessageBody: aws.String("two")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.move(&awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(f.arns["dlq"]), DestinationArn: aws.String(f.arns["inbox"])}); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies("inbox"); len(got) != 2 {
		t.Errorf("inbox holds %v, want both", got)
	}
}

// A task reports how many messages it was to move, counted when it started
// (doc: ListMessageMoveTasks, ApproximateNumberOfMessagesToMove), and the rate
// it was asked for. Nothing is throttled here — a move completes at once — so
// the rate is a record, not a behaviour.
func TestATaskRecordsWhatItWasToMoveAndTheRateAsked(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	f.deadLetter("orders", "one")
	f.deadLetter("orders", "two")
	if _, err := f.move(&awssqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(f.arns["dlq"]), MaxNumberOfMessagesPerSecond: aws.Int32(7)}); err != nil {
		t.Fatal(err)
	}
	list, err := f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"])})
	if err != nil || len(list.Results) != 1 {
		t.Fatalf("ListMessageMoveTasks: %v %v", list, err)
	}
	r := list.Results[0]
	if r.ApproximateNumberOfMessagesToMove == nil || *r.ApproximateNumberOfMessagesToMove != 2 {
		t.Errorf("ApproximateNumberOfMessagesToMove = %v, want 2", r.ApproximateNumberOfMessagesToMove)
	}
	if r.MaxNumberOfMessagesPerSecond == nil || *r.MaxNumberOfMessagesPerSecond != 7 {
		t.Errorf("MaxNumberOfMessagesPerSecond = %v, want 7", r.MaxNumberOfMessagesPerSecond)
	}
	if r.ApproximateNumberOfMessagesMoved != 2 {
		t.Errorf("moved %d, want 2", r.ApproximateNumberOfMessagesMoved)
	}

	// A task started without a rate reports none.
	f.deadLetter("orders", "three")
	if _, err := f.move(&awssqs.StartMessageMoveTaskInput{SourceArn: aws.String(f.arns["dlq"])}); err != nil {
		t.Fatal(err)
	}
	list, _ = f.c.ListMessageMoveTasks(f.ctx, &awssqs.ListMessageMoveTasksInput{SourceArn: aws.String(f.arns["dlq"])})
	if len(list.Results) != 1 || list.Results[0].MaxNumberOfMessagesPerSecond != nil {
		t.Errorf("a task with no rate reports one: %+v", list.Results)
	}
}

// doc: "The maximum value for messages per second is 500."
func TestTheRateAskedForIsHeldToItsRange(t *testing.T) {
	f := newRedriveFixture(t, "dlq")
	f.create("orders", redrivePolicyTo(f, "dlq"))
	for rate, ok := range map[int32]bool{0: false, -1: false, 1: true, 500: true, 501: false} {
		_, err := f.move(&awssqs.StartMessageMoveTaskInput{
			SourceArn: aws.String(f.arns["dlq"]), MaxNumberOfMessagesPerSecond: aws.Int32(rate)})
		if ok && err != nil || !ok && err == nil {
			t.Errorf("rate %d: err = %v, want ok=%v", rate, err, ok)
		}
	}
}
