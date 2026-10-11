package sqs

// A receipt handle belongs to a receive. The model: "When you want to delete a
// message using the DeleteMessage action, you must use the ReceiptHandle from
// the most recent time you received the message. If you use an old
// ReceiptHandle, the request will succeed, but the message might not be
// deleted."
//
// The bug this exists to show locally is the classic one: a worker is slow, the
// visibility timeout lapses, a second worker receives the message, and the
// first deletes with its old handle. On AWS the message survives and is
// processed twice. Here the delete used to remove it, which hid the bug.

import (
	"encoding/base64"
	"testing"
)

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestADeleteWithAnOldHandleSucceedsAndLeavesTheMessage(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "0"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("q", "work", nil, -1, "", "", nil); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Receive("q", 1, 0, -1)
	second, _ := s.Receive("q", 1, 0, -1) // a second worker: the timeout was 0
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("receives: %v %v", first, second)
	}
	if first[0].Handle() == second[0].Handle() {
		t.Fatal("two receives of one message produced the same receipt handle")
	}
	// The slow worker finishes and deletes with its handle: accepted...
	if err := s.Delete("q", first[0].Handle()); err != nil {
		t.Fatalf("a delete with an old handle was refused: %v", err)
	}
	// ...and the message is still there, to be received again.
	third, _ := s.Receive("q", 1, 0, -1)
	if len(third) != 1 || third[0].ID != second[0].ID {
		t.Fatalf("the old handle deleted the message: %v", third)
	}
	// The most recent handle does delete it.
	if err := s.Delete("q", third[0].Handle()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Receive("q", 1, 0, -1); len(got) != 0 {
		t.Errorf("the current handle did not delete: %v", got)
	}
}

func TestTheHandleOfTheLatestReceiveStaysGoodUntilAnotherReceive(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("q", map[string]string{"VisibilityTimeout": "0"}, nil); err != nil {
		t.Fatal(err)
	}
	s.Send("q", "work", nil, -1, "", "", nil)
	got, _ := s.Receive("q", 1, 0, -1)
	// The timeout lapsed (it is 0) and nobody else received it: the handle is
	// still the latest and still deletes.
	if err := s.Delete("q", got[0].Handle()); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Receive("q", 1, 0, -1); len(again) != 0 {
		t.Errorf("a handle that was still the latest did not delete: %v", again)
	}
}

func TestAHandleMintedBeforeHandlesCarriedTheirReceiveStillWorks(t *testing.T) {
	s := testStore(t)
	s.CreateQueue("q", nil, nil)
	s.Send("q", "work", nil, -1, "", "", nil)
	got, _ := s.Receive("q", 1, 0, -1)
	old := encodeHandleWithoutReceive(seqKey(got[0].Seq), got[0].ID)
	if err := s.Delete("q", old); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Attributes("q"); a["ApproximateNumberOfMessagesNotVisible"] != "0" {
		t.Errorf("a handle in the earlier format did not delete: %v", a)
	}
}

// A batch takes each entry's handle on its own terms.
func TestABatchDeleteWithAnOldHandleSucceedsPerEntry(t *testing.T) {
	s := testStore(t)
	s.CreateQueue("q", map[string]string{"VisibilityTimeout": "0"}, nil)
	s.Send("q", "work", nil, -1, "", "", nil)
	first, _ := s.Receive("q", 1, 0, -1)
	second, _ := s.Receive("q", 1, 0, -1)
	errs, err := s.DeleteBatch("q", []string{first[0].Handle(), second[0].Handle()})
	if err != nil || errs[0] != nil || errs[1] != nil {
		t.Fatalf("%v %v", errs, err)
	}
	if got, _ := s.Receive("q", 1, 0, -1); len(got) != 0 {
		t.Errorf("the current handle in the batch did not delete: %v", got)
	}
}

// encodeHandleWithoutReceive is a handle as earlier builds minted it: the
// sequence key and the id, nothing else.
func encodeHandleWithoutReceive(seq []byte, id string) string {
	return base64Std(append(append([]byte{}, seq...), id...))
}
