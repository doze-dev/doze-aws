package sns

// FIFO topics: the part of them a local stack needs.
//
// A FIFO topic used to be a topic with ".fifo" in its name and nothing else.
// CreateTopic stored the FifoTopic attribute, Publish ignored MessageGroupId,
// and the delivery to a FIFO queue was then refused by SQS for having no
// group — which SNS logged and dropped. So the one arrangement FIFO topics
// exist for, a FIFO topic fanning out to FIFO queues, accepted every call and
// delivered nothing.
//
// What is here: a publish to a FIFO topic must name its group, and must be
// deduplicable (an explicit id, or content-based deduplication on the topic);
// both are carried to every FIFO queue subscribed, where SQS does the ordering
// and the deduplication it already knows how to do; and the publish answers
// with a sequence number. Deduplication is therefore per subscribed queue
// rather than at the topic, which is the same thing wherever the subscribers
// are queues — and a FIFO topic's subscribers nearly always are.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type fifoKey struct{}

// fifoSend is what a FIFO publish carries to its subscribers.
type fifoSend struct{ group, dedup string }

func withFIFO(ctx context.Context, f fifoSend) context.Context {
	return context.WithValue(ctx, fifoKey{}, f)
}

func fifoFrom(ctx context.Context) (fifoSend, bool) {
	f, ok := ctx.Value(fifoKey{}).(fifoSend)
	return f, ok
}

// fifoFor works out what a publish to this topic must carry. ok is false for
// a standard topic, where a group id is allowed and means nothing.
func (srv *Server) fifoFor(topicARN, message, group, dedup string) (f fifoSend, ok bool, aerr *apiError) {
	t, err := srv.store.GetTopic(topicARN)
	if err != nil || t.Attrs["FifoTopic"] != "true" {
		return fifoSend{}, false, nil
	}
	if group == "" {
		return fifoSend{}, false, errInvalid("Invalid parameter: The MessageGroupId parameter is required for FIFO topics")
	}
	if dedup == "" {
		if t.Attrs["ContentBasedDeduplication"] != "true" {
			return fifoSend{}, false, errInvalid("Invalid parameter: The topic should either have ContentBasedDeduplication enabled or MessageDeduplicationId provided explicitly")
		}
		sum := sha256.Sum256([]byte(message))
		dedup = hex.EncodeToString(sum[:])
	}
	return fifoSend{group: group, dedup: dedup}, true, nil
}

// sequenceNumber is a FIFO publish's position: twenty digits, increasing.
func sequenceNumber() string {
	return fmt.Sprintf("1%019d", time.Now().UnixNano())
}
