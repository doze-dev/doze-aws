package console

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
)

// The consuming half of SQS.
//
// The console has always PEEKED — DozePeek reads without consuming, which is a
// genuine edge over the real AWS console, whose "poll for messages" actually
// receives them and hides them from you. But peeking is all it could do, and
// that left the single most-debugged local behaviour unreachable: a real
// ReceiveMessage, with a visibility timeout that hides the message from other
// consumers until it expires or you delete it.
//
// You cannot debug "why does my consumer see this twice" against a surface
// that never receives anything. So: real receives, with the timer on screen.

// ReceiveOpts is the ReceiveMessage request as a form fills it in.
type ReceiveOpts struct {
	Max        int // MaxNumberOfMessages, 1..10
	Visibility int // VisibilityTimeout override; -1 means "use the queue's"
	Wait       int // WaitTimeSeconds, 0..20 — long polling
}

// ReceiveMessages performs a REAL receive: it bumps ApproximateReceiveCount and
// hides what it returns for the visibility timeout. That is the point of it.
func (b *backend) ReceiveMessages(ctx context.Context, name string, o ReceiveOpts) ([]SQSMessage, error) {
	in := map[string]any{
		"QueueUrl":              b.queueURL(name),
		"MaxNumberOfMessages":   clampInt(o.Max, 1, 10),
		"WaitTimeSeconds":       clampInt(o.Wait, 0, 20),
		"AttributeNames":        []string{"All"},
		"MessageAttributeNames": []string{"All"},
	}
	// Only send the override when there is one: sending 0 is not "unset", it
	// is "make this visible again immediately", which is a different request.
	if o.Visibility >= 0 {
		in["VisibilityTimeout"] = o.Visibility
	}
	body, err := b.sqs(ctx, "ReceiveMessage", in)
	if err != nil {
		return nil, err
	}
	return parseSQSMessages(body), nil
}

// ChangeVisibility re-hides or releases one in-flight message. secs=0 is the
// "release now" case — it makes the message immediately available again, which
// is how you put something back without waiting out its timer.
func (b *backend) ChangeVisibility(ctx context.Context, name, handle string, secs int) error {
	_, err := b.sqs(ctx, "ChangeMessageVisibility", map[string]any{
		"QueueUrl": b.queueURL(name), "ReceiptHandle": handle,
		"VisibilityTimeout": clampInt(secs, 0, 43200),
	})
	return err
}

// BatchFailure is one entry SQS refused inside a batch.
//
// Batch APIs are partially-successful by design — SQS answers with Successful
// and Failed lists rather than an error — so swallowing the Failed list would
// report "deleted" for messages that are still there. The console says
// "8 deleted, 2 refused" instead, which is what actually happened.
type BatchFailure struct {
	ID      string
	Code    string
	Message string
}

// DeleteMessageBatch deletes up to N messages, chunked to SQS's limit of ten
// entries per call. The chunking lives here rather than in the page because a
// selection is a statement of intent and ten is a wire limit.
func (b *backend) DeleteMessageBatch(ctx context.Context, name string, handles []string) ([]BatchFailure, error) {
	return b.sqsBatch(ctx, "DeleteMessageBatch", name, handles, nil)
}

// ChangeMessageVisibilityBatch re-hides or releases many in-flight messages.
func (b *backend) ChangeMessageVisibilityBatch(ctx context.Context, name string, handles []string, secs int) ([]BatchFailure, error) {
	v := clampInt(secs, 0, 43200)
	return b.sqsBatch(ctx, "ChangeMessageVisibilityBatch", name, handles, &v)
}

// sqsBatch is the shared shape of the two receipt-handle batch calls: chunk to
// ten, accumulate the Failed entries, and keep going after a chunk that had
// failures — a later chunk's messages are unrelated to an earlier one's.
func (b *backend) sqsBatch(ctx context.Context, action, name string, handles []string, visibility *int) ([]BatchFailure, error) {
	var failed []BatchFailure
	for start := 0; start < len(handles); start += 10 {
		end := min(start+10, len(handles))
		entries := make([]map[string]any, 0, end-start)
		for i, h := range handles[start:end] {
			e := map[string]any{"Id": "m" + strconv.Itoa(start+i), "ReceiptHandle": h}
			if visibility != nil {
				e["VisibilityTimeout"] = *visibility
			}
			entries = append(entries, e)
		}
		body, err := b.sqs(ctx, action, map[string]any{
			"QueueUrl": b.queueURL(name), "Entries": entries,
		})
		if err != nil {
			return failed, err
		}
		var out struct {
			Failed []struct {
				ID      string `json:"Id"`
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Failed"`
		}
		json.Unmarshal(body, &out)
		for _, f := range out.Failed {
			failed = append(failed, BatchFailure{ID: f.ID, Code: f.Code, Message: f.Message})
		}
	}
	return failed, nil
}

// parseSQSMessages decodes the Messages list shared by DozePeek and
// ReceiveMessage. Extracted so the peek and the receive cannot drift into
// showing the same message two different ways.
func parseSQSMessages(body []byte) []SQSMessage {
	var out struct {
		Messages []struct {
			MessageID     string            `json:"MessageId"`
			ReceiptHandle string            `json:"ReceiptHandle"`
			Body          string            `json:"Body"`
			Attributes    map[string]string `json:"Attributes"`
			MessageAttrs  map[string]struct {
				DataType    string `json:"DataType"`
				StringValue string `json:"StringValue"`
			} `json:"MessageAttributes"`
		} `json:"Messages"`
	}
	json.Unmarshal(body, &out)
	msgs := make([]SQSMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		msg := SQSMessage{
			MessageID: m.MessageID, ReceiptHandle: m.ReceiptHandle, Body: m.Body,
			SentAt:   epochMillisToTime(m.Attributes["SentTimestamp"]),
			GroupID:  m.Attributes["MessageGroupId"],
			DedupID:  m.Attributes["MessageDeduplicationId"],
			SeqNo:    m.Attributes["SequenceNumber"],
			Receives: m.Attributes["ApproximateReceiveCount"],
		}
		names := make([]string, 0, len(m.MessageAttrs))
		for n := range m.MessageAttrs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			a := m.MessageAttrs[n]
			msg.Attrs = append(msg.Attrs, MsgAttr{Name: n, Type: a.DataType, Value: a.StringValue})
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
