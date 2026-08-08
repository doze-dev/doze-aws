package kinesis

// Kinesis-shaped errors. The codes are the ones the SDKs' retry and error
// handling match on, so they are spelled exactly as AWS spells them.

import (
	"strconv"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

type apiError = awshttp.APIError

func errNoStream(name string) *apiError {
	return &apiError{
		Code: "ResourceNotFoundException", Status: 400, SenderFault: true,
		Message: "Stream " + name + " under account " + accountID + " not found.",
	}
}

func errNoShard(shard, stream string) *apiError {
	return &apiError{
		Code: "ResourceNotFoundException", Status: 400, SenderFault: true,
		Message: "Shard " + shard + " in stream " + stream + " under account " + accountID + " does not exist",
	}
}

func errNoConsumer(name string) *apiError {
	return &apiError{
		Code: "ResourceNotFoundException", Status: 400, SenderFault: true,
		Message: "Consumer " + name + " not found.",
	}
}

func errInvalid(format string, args ...any) *apiError {
	return awshttp.Errf(400, "InvalidArgumentException", format, args...)
}

func errValidation(format string, args ...any) *apiError {
	return awshttp.Errf(400, "ValidationException", format, args...)
}

// errInUse is what AWS returns when a stream is being created, deleted or
// resharded — and for a duplicate CreateStream.
func errInUse(format string, args ...any) *apiError {
	return awshttp.Errf(400, "ResourceInUseException", format, args...)
}

// errExpiredIterator maps to the SDK's ExpiredIteratorException, which
// consumers (and the KCL) handle by fetching a fresh iterator.
func errExpiredIterator(format string, args ...any) *apiError {
	return awshttp.Errf(400, "ExpiredIteratorException", format, args...)
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// notifier wakes blocked readers the instant records are appended, so pollers
// don't spin-scan. Each stream has a broadcast channel: waiters take the
// current channel, then signal() closes it (waking everyone) and drops it, so
// the next waiter creates a fresh one. Taking the channel before re-checking
// the store is what avoids lost wakeups.
type notifier struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

func newNotifier() *notifier { return &notifier{chans: map[string]chan struct{}{}} }

func (n *notifier) wait(stream string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[stream]
	if !ok {
		ch = make(chan struct{})
		n.chans[stream] = ch
	}
	return ch
}

func (n *notifier) signal(stream string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[stream]; ok {
		close(ch)
		delete(n.chans, stream)
	}
}
