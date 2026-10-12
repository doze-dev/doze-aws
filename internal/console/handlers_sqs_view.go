package console

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// What the SQS pages say in words, and the two small reads the boards need
// that the queue's own attributes do not carry.

// sqsShort writes a number of seconds the way the design's queue pages do: whole
// days and whole hours in words ("4 days", "12 hours"), everything else as the
// seconds that were typed ("60 s", "180 s"). A visibility timeout is a number
// typed in seconds and read back in seconds; a retention period is not.
func sqsShort(seconds string) string {
	n, err := strconv.Atoi(strings.TrimSpace(seconds))
	if err != nil {
		return "—"
	}
	unit := func(q int, word string) string {
		if q == 1 {
			return "1 " + word
		}
		return strconv.Itoa(q) + " " + word + "s"
	}
	switch {
	case n > 0 && n%86400 == 0:
		return unit(n/86400, "day")
	case n > 0 && n%3600 == 0:
		return unit(n/3600, "hour")
	}
	return strconv.Itoa(n) + " s"
}

// settingDefault writes a setting's default the way the line under a field
// says it: a duration of a minute or more in words ("4 days"), a byte count
// with its digits in threes, and anything else as stated.
func settingDefault(unit, def string) string {
	n, err := strconv.ParseInt(def, 10, 64)
	if err != nil {
		return def
	}
	switch unit {
	case "seconds":
		if n >= 3600 {
			return sqsShort(def)
		}
	case "bytes":
		return strings.ReplaceAll(humanCount(n), ",", " ")
	}
	return def
}

// sqsSource is one queue that dead-letters into the queue being shown.
type sqsSource struct {
	Name       string
	MaxReceive int // its maxReceiveCount: how many deliveries a message got there
}

// sqsFedBy reads each source queue's redrive policy, for the sentence under a
// dead-letter queue's messages: how many receives a message had before it was
// set aside, and where.
func (c *Console) sqsFedBy(ctx context.Context, sources []string) []sqsSource {
	out := make([]sqsSource, 0, len(sources))
	for _, name := range sources {
		s := sqsSource{Name: name}
		if attrs, err := c.be.queueAttrs(ctx, name); err == nil {
			s.MaxReceive = atoi(redrivePolicyMaxReceive(attrs["RedrivePolicy"]))
		}
		out = append(out, s)
	}
	return out
}

// lastDoneDest is where the newest finished move task sent its messages, for
// "Open <dest> to see them again". Empty when none has finished.
func lastDoneDest(tasks []MoveTask) string {
	for _, t := range tasks {
		if t.Status == "COMPLETED" && t.Dest != "" {
			return t.Dest
		}
	}
	return ""
}

// permAccount and permAction show a statement AddPermission wrote the way it
// was asked for: the account and the action, not the ARN and the prefix the
// policy document spells them with.
func permAccount(principal string) string {
	if rest, ok := strings.CutPrefix(principal, "arn:aws:iam::"); ok {
		if acct, ok := strings.CutSuffix(rest, ":root"); ok {
			return acct
		}
	}
	return principal
}

func permAction(action string) string {
	if i := strings.Index(action, ":"); i >= 0 && strings.EqualFold(action[:i], "sqs") {
		return action[i+1:]
	}
	return action
}

// sqsHeldHash fingerprints the Consume tab's held batch and the counts beside
// it, so the tab can be polled and answer 204 while nothing has moved.
func sqsHeldHash(held []SQSMessage, attrs map[string]string) string {
	parts := []string{attrs["ApproximateNumberOfMessages"], attrs["ApproximateNumberOfMessagesNotVisible"]}
	for _, m := range held {
		parts = append(parts, m.ReceiptHandle)
	}
	return contentHash(parts...)
}

// sqsConsumed is the Consume tab's live partial (GET …/messages?held=1). A held message comes back by
// itself when its time runs out, and nothing the user does marks the moment:
// without this the row stayed, and the counts above it said "0 waiting, 1 in
// flight" about a message anybody could receive.
func (c *Console) sqsConsumed(w http.ResponseWriter, r *http.Request) {
	name := param(r, "queue")
	data := c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, "")
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "sqs_consumed", data)
}
