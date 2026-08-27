package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// The Consume tab.
//
// Everything the console did with a queue before this was non-destructive: the
// peek reads without receiving, which is deliberate and good. But it meant the
// behaviour people actually come to a local SQS to debug — a message going
// invisible on receive, reappearing when the timeout lapses, and its receive
// count climbing toward the redrive threshold — could not be exercised here at
// all. You would drop to the CLI, and then the console was not the place you
// worked.
//
// A received batch is NOT stored. It exists in the response that produced it,
// which is honest: the receipt handles are only good for this visibility
// window, and a page that kept showing them after they expired would be
// offering buttons that fail.

// sqsReceive performs a real ReceiveMessage and renders the batch it got.
func (c *Console) sqsReceive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	o := ReceiveOpts{
		Max:        atoiDefault(r.FormValue("max"), 10),
		Wait:       atoiDefault(r.FormValue("wait"), 0),
		Visibility: -1, // blank means "use the queue's own timeout"
	}
	if v := strings.TrimSpace(r.FormValue("visibility")); v != "" {
		o.Visibility = atoiDefault(v, 0)
	}
	msgs, err := c.be.ReceiveMessages(r.Context(), name, o)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sqs_consumed", c.consumeData(r, name, msgs, o, ""))
}

// sqsChangeVisibility re-hides or releases one in-flight message.
func (c *Console) sqsChangeVisibility(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	secs := atoiDefault(r.FormValue("seconds"), 0)
	if err := c.be.ChangeVisibility(r.Context(), name, r.FormValue("handle"), secs); err != nil {
		c.fail(w, err)
		return
	}
	note := fmt.Sprintf("Visibility set to %ds", secs)
	if secs == 0 {
		note = "Released — visible again now"
	}
	toast(w, note)
	c.partial(w, "sqs_consumed", c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, note))
}

// sqsDeleteBatch and sqsVisibilityBatch turn a selection into the batch APIs.
// These two are the only way DeleteMessageBatch and
// ChangeMessageVisibilityBatch are reachable from the console at all — a batch
// endpoint with no multi-select is an endpoint you cannot call.
func (c *Console) sqsDeleteBatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	handles := parseIDs(r.FormValue("ids"))
	if len(handles) == 0 {
		c.partial(w, "sqs_consumed", c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, "Nothing selected"))
		return
	}
	failed, err := c.be.DeleteMessageBatch(r.Context(), name, handles)
	if err != nil {
		c.fail(w, err)
		return
	}
	note := batchNote(len(handles), failed, "deleted")
	toast(w, note)
	c.partial(w, "sqs_consumed", c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, note))
}

func (c *Console) sqsVisibilityBatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	handles := parseIDs(r.FormValue("ids"))
	secs := atoiDefault(r.FormValue("seconds"), 0)
	if len(handles) == 0 {
		c.partial(w, "sqs_consumed", c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, "Nothing selected"))
		return
	}
	failed, err := c.be.ChangeMessageVisibilityBatch(r.Context(), name, handles, secs)
	if err != nil {
		c.fail(w, err)
		return
	}
	verb := fmt.Sprintf("set to %ds", secs)
	if secs == 0 {
		verb = "released"
	}
	note := batchNote(len(handles), failed, verb)
	toast(w, note)
	c.partial(w, "sqs_consumed", c.consumeData(r, name, nil, ReceiveOpts{Max: 10, Visibility: -1}, note))
}

// consumeData assembles the Consume panel. It re-reads the queue's attributes
// on every render so the strip above the tabs and the in-flight number stay
// truthful — a receive moves messages between "visible" and "in flight", which
// is the whole thing being demonstrated.
func (c *Console) consumeData(r *http.Request, name string, msgs []SQSMessage, o ReceiveOpts, note string) map[string]any {
	for i := range msgs {
		msgs[i].Sum = summarize(msgs[i].Body)
	}
	attrs, _ := c.be.queueAttrs(r.Context(), name)
	vis := o.Visibility
	if vis < 0 {
		vis = atoi(attrs["VisibilityTimeout"])
	}
	return map[string]any{
		"Prefix": c.prefix, "Queue": name,
		"Received": msgs, "Note": note,
		"Max": o.Max, "Wait": o.Wait, "Visibility": o.Visibility,
		// EffVis is what the countdown counts down from: the override when one
		// was given, the queue's own setting otherwise. Showing the queue's
		// number while an override was in force would misreport the very timer
		// this tab exists to make visible.
		"EffVis":     vis,
		"Available":  atoi(attrs["ApproximateNumberOfMessages"]),
		"InFlight":   atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
		"MaxReceive": atoi(redrivePolicyMaxReceive(attrs["RedrivePolicy"])),
	}
}

// batchNote states what actually happened. Batch APIs are partially successful
// by design, so "deleted" when two of ten were refused is a lie the console
// would have no way to walk back.
func batchNote(n int, failed []BatchFailure, verb string) string {
	if len(failed) == 0 {
		return fmt.Sprintf("%d %s", n, verb)
	}
	codes := map[string]bool{}
	var order []string
	for _, f := range failed {
		if !codes[f.Code] {
			codes[f.Code] = true
			order = append(order, f.Code)
		}
	}
	return fmt.Sprintf("%d %s, %d refused (%s)", n-len(failed), verb, len(failed), strings.Join(order, ", "))
}

// parseIDs decodes the JSON array of receipt handles a multi-select submits
// through a hidden field — the same shape parseMsgAttrs already uses for
// message attributes, so selection rides the ordinary form path and inherits
// the double-submit guard and the error placement with it.
func parseIDs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	keep := out[:0]
	for _, v := range out {
		if strings.TrimSpace(v) != "" {
			keep = append(keep, v)
		}
	}
	return keep
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
