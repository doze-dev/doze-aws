package sqs

import "testing"

// A queue tells whoever asked the id of each message it enqueues, and tells it
// before the message can be received (internal/msgcause: the mapping that reads
// the message asks who sent it, and must find the answer). Only a message that
// is really enqueued is announced: a refused entry and a FIFO duplicate are not.
func TestAQueueAnnouncesTheIDsOfTheMessagesItEnqueued(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateQueue("orders", map[string]string{"MaximumMessageSize": "1024"}, nil); err != nil {
		t.Fatal(err)
	}

	var told []string
	committed := 0 // what the queue held before the call in flight
	note := func(id string) {
		// Not yet visible: the transaction that makes it so has not committed.
		if msgs, _ := s.Peek("orders", 10); len(msgs) != committed {
			t.Errorf("message %s announced when the queue already held %d, want the %d from before this call", id, len(msgs), committed)
		}
		told = append(told, id)
	}

	m, err := s.sendNoted(note, "orders", "one", nil, -1, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	committed = 1
	if len(told) != 1 || told[0] != m.ID {
		t.Fatalf("a send announced %v, want its id %q", told, m.ID)
	}

	// A batch announces each entry that was enqueued and none that was refused.
	big := make([]byte, 2048)
	res, err := s.SendBatch("orders", []sendItem{
		{Body: "a", Delay: -1, Note: note},
		{Body: string(big), Delay: -1, Note: note},
		{Body: "c", Delay: -1, Note: note},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[1].Err == nil {
		t.Fatal("the oversized entry was accepted")
	}
	if len(told) != 3 || told[1] != res[0].Msg.ID || told[2] != res[2].Msg.ID {
		t.Errorf("a batch announced %v, want the ids of entries a and c only", told[1:])
	}

	// A send that is refused announces nothing.
	if _, err := s.sendNoted(note, "orders", string(big), nil, -1, "", "", nil); err == nil {
		t.Fatal("the oversized send was accepted")
	}
	if len(told) != 3 {
		t.Errorf("a refused send announced %v", told[3:])
	}
}
