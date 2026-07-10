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
