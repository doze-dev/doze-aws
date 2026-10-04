package console

import (
	"strconv"
	"sync"
	"time"
)

// The messages the Consume tab is holding.
//
// A receive hides messages from every other consumer until their visibility
// timeout lapses, and a consumer that has them is expected to go on holding
// them while it deletes or releases some. The panel used to be rebuilt from
// nothing after every action, so acting on one message made the others vanish
// from view while they stayed invisible on the queue. The console now keeps
// what it received, per queue, for exactly as long as the handle is good: a
// row leaves when it is deleted, released, or its timeout lapses, never
// because a neighbour was acted on.
type heldMsg struct {
	msg   SQSMessage
	until time.Time
}

type heldSet struct {
	mu sync.Mutex
	by map[string][]heldMsg // queue name → messages, in receive order
}

// add records a freshly received batch, hidden for vis seconds.
func (h *heldSet) add(queue string, msgs []SQSMessage, vis int) {
	if len(msgs) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.by == nil {
		h.by = map[string][]heldMsg{}
	}
	until := time.Now().Add(time.Duration(vis) * time.Second)
	for _, m := range msgs {
		h.by[queue] = append(h.by[queue], heldMsg{msg: m, until: until})
	}
}

// drop forgets the handles that were deleted or released.
func (h *heldSet) drop(queue string, handles map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.by[queue]) == 0 {
		return
	}
	keep := h.by[queue][:0]
	for _, m := range h.by[queue] {
		if !handles[m.msg.ReceiptHandle] {
			keep = append(keep, m)
		}
	}
	h.by[queue] = keep
}

// rehide moves the deadline of handles whose visibility was changed.
func (h *heldSet) rehide(queue string, handles map[string]bool, secs int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	until := time.Now().Add(time.Duration(secs) * time.Second)
	for i := range h.by[queue] {
		if handles[h.by[queue][i].msg.ReceiptHandle] {
			h.by[queue][i].until = until
		}
	}
}

// live returns the messages still hidden, each with the seconds left, and
// forgets the ones whose timeout has lapsed — their handles no longer work, and
// a button that fails is worse than a row that is gone.
func (h *heldSet) live(queue string) []SQSMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.by[queue]) == 0 {
		return nil
	}
	now := time.Now()
	keep := h.by[queue][:0]
	var out []SQSMessage
	for _, m := range h.by[queue] {
		left := int(m.until.Sub(now).Round(time.Second) / time.Second)
		if left <= 0 {
			continue
		}
		keep = append(keep, m)
		m.msg.Left = left
		out = append(out, m.msg)
	}
	h.by[queue] = keep
	return out
}

// settled is the set of handles a batch actually changed: every handle except
// the entries SQS refused. Batch entry ids are "m<index into the request>".
func settled(handles []string, failed []BatchFailure) map[string]bool {
	out := make(map[string]bool, len(handles))
	for _, h := range handles {
		out[h] = true
	}
	for _, f := range failed {
		if len(f.ID) > 1 && f.ID[0] == 'm' {
			if i, err := strconv.Atoi(f.ID[1:]); err == nil && i >= 0 && i < len(handles) {
				delete(out, handles[i])
			}
		}
	}
	return out
}
