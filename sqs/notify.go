package sqs

import "sync"

// notifier wakes long-poll receivers the instant a message is enqueued, so they
// don't spin-scan. Each queue has a broadcast channel: waiters take the current
// channel, then signal() closes it (waking everyone) and drops it, so the next
// waiter creates a fresh one. Getting the channel before re-checking the store
// avoids lost wakeups.
type notifier struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

func newNotifier() *notifier { return &notifier{chans: map[string]chan struct{}{}} }

func (n *notifier) wait(queue string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[queue]
	if !ok {
		ch = make(chan struct{})
		n.chans[queue] = ch
	}
	return ch
}

func (n *notifier) signal(queue string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[queue]; ok {
		close(ch)
		delete(n.chans, queue)
	}
}

// forget drops a queue's channel WITHOUT waking anyone.
//
// wait() registers interest before the receive is attempted, which is
// deliberate — a Send landing between the check and the wait would otherwise be
// missed. The cost is that a receive for a queue that does not exist also
// registers, and nothing will ever signal that name, so the entry stayed
// forever. `for i in $(seq 1000000); do aws sqs receive-message --queue-url
// .../nope-$i; done` left a million live channels and their strings; a queue
// deleted out from under a waiter stranded one the same way.
//
// Not closed, only dropped: anyone still holding the channel is waiting on a
// queue that cannot deliver, and they have a long-poll deadline of their own.
// Closing would be a spurious wakeup with nothing behind it.
func (n *notifier) forget(queue string) {
	n.mu.Lock()
	delete(n.chans, queue)
	n.mu.Unlock()
}
