package sqs

// Strict mode: the two rules AWS has because a delete and a purge take up to a
// minute there.
//
// doc: the model's DeleteQueue ("When you delete a queue, you must wait at
// least 60 seconds before creating a queue with the same name"), PurgeQueue
// ("The message deletion process takes up to 60 seconds") and the
// QueueDeletedRecently and PurgeQueueInProgress error shapes.
//
// Both are off by default. Here a delete and a purge are immediate, so there is
// nothing to wait out, and a rule that made every local test loop wait a minute
// would be a worse tool than one that could not tell you. They exist for the
// person who wants the failure before the deploy.
//
// What is remembered is a name and a time, in memory, for a minute: a restart
// forgets it, and only makes the emulator more forgiving for the rest of that
// minute. Nothing is written to the store.

import (
	"sync"
	"time"
)

const minuteRule = time.Minute

type minuteRules struct {
	mu      sync.Mutex
	deleted map[string]time.Time // queue name -> when it was deleted
	purged  map[string]time.Time // queue name -> when it was purged
}

// recent reports whether name is in m with a time under a minute old, and
// forgets the entries that are not.
func recent(m map[string]time.Time, name string, now time.Time) bool {
	for k, at := range m {
		if now.Sub(at) >= minuteRule {
			delete(m, k)
		}
	}
	_, ok := m[name]
	return ok
}

func (r *minuteRules) noteDeleted(name string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleted == nil {
		r.deleted = map[string]time.Time{}
	}
	r.deleted[name] = now
	delete(r.purged, name)
}

func (r *minuteRules) deletedRecently(name string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recent(r.deleted, name, now)
}

// purgeAllowed records a purge of name and reports whether it may go ahead: not
// when the queue was purged less than a minute ago.
func (r *minuteRules) purgeAllowed(name string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if recent(r.purged, name, now) {
		return false
	}
	if r.purged == nil {
		r.purged = map[string]time.Time{}
	}
	r.purged[name] = now
	return true
}

func errQueueDeletedRecently() *apiError {
	return &apiError{Code: "AWS.SimpleQueueService.QueueDeletedRecently", Status: 400, SenderFault: true,
		Message: "You must wait 60 seconds after deleting a queue before you can create another queue with the same name."}
}

func errPurgeInProgress() *apiError {
	return &apiError{Code: "AWS.SimpleQueueService.PurgeQueueInProgress", Status: 400, SenderFault: true,
		Message: "Only one PurgeQueue operation on this queue is allowed every 60 seconds."}
}
