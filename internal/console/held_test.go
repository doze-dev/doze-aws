package console

import "testing"

// Acting on one held message leaves the others where they are, and a batch
// only lets go of the entries SQS accepted.
func TestHeldKeepsNeighboursWhenOneIsActedOn(t *testing.T) {
	var h heldSet
	h.add("q", []SQSMessage{{ReceiptHandle: "a"}, {ReceiptHandle: "b"}, {ReceiptHandle: "c"}}, 300)

	h.drop("q", map[string]bool{"b": true})
	got := h.live("q")
	if len(got) != 2 || got[0].ReceiptHandle != "a" || got[1].ReceiptHandle != "c" {
		t.Fatalf("after dropping b: %+v", got)
	}
	if got[0].Left < 299 {
		t.Errorf("Left = %d, want about 300", got[0].Left)
	}

	// SQS refused the first entry of this batch, so "a" is still held.
	h.drop("q", settled([]string{"a", "c"}, []BatchFailure{{ID: "m0", Code: "ReceiptHandleIsInvalid"}}))
	if got := h.live("q"); len(got) != 1 || got[0].ReceiptHandle != "a" {
		t.Fatalf("after a partial batch: %+v", got)
	}
}

func TestHeldForgetsWhatHasLapsed(t *testing.T) {
	var h heldSet
	h.add("q", []SQSMessage{{ReceiptHandle: "a"}}, 0)
	if got := h.live("q"); len(got) != 0 {
		t.Fatalf("a lapsed message is still shown: %+v", got)
	}
	if got := h.live("other"); got != nil {
		t.Fatalf("an unknown queue holds %+v", got)
	}
}
