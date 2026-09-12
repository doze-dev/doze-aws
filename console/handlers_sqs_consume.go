package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
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

// tagsFromRows reads the shared tag row-editor's parallel inputs. The same
// name/value pair shape parseEnvRows uses for Lambda environment variables, so
// a create form can carry tags without inventing a third encoding.
func tagsFromRows(r *http.Request) map[string]string {
	keys, vals := r.Form["tag_key"], r.Form["tag_val"]
	if len(keys) == 0 {
		return nil
	}
	out := map[string]string{}
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v := ""
		if i < len(vals) {
			v = vals[i]
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sqsSendBatch publishes several messages in one call — the composer's
// "send N" mode. SendMessageBatch was implemented by the emulator and had no
// way in from the console, which meant the only way to produce a burst was to
// press Send repeatedly and get a different timing profile than a real batch.
func (c *Console) sqsSendBatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	var bodies []string
	for _, b := range r.Form["body"] {
		if strings.TrimSpace(b) != "" {
			bodies = append(bodies, b)
		}
	}
	// "Repeat this body N times" is the common case when what you want is
	// depth rather than distinct payloads.
	if n := atoiDefault(r.FormValue("repeat"), 0); n > 1 && len(bodies) == 1 {
		for i := 1; i < min(n, 10); i++ {
			bodies = append(bodies, bodies[0])
		}
	}
	if len(bodies) == 0 {
		c.fail(w, errors.New("a batch needs at least one message body"))
		return
	}
	failed, err := c.be.SendMessageBatch(r.Context(), name, bodies,
		r.FormValue("delay"), r.FormValue("group"))
	if err != nil {
		c.fail(w, err)
		return
	}
	toast(w, batchNote(len(bodies), failed, "sent"))
	c.sqsMessages(w, r)
}

// sqsAddPermission and sqsRemovePermission write the queue's resource policy.
// AddPermission writes the statement AWS writes; under IAM soft and enforce the
// queue evaluates the policy on every request, so a grant here is real.
func (c *Console) sqsAddPermission(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		c.fail(w, errors.New("a permission needs a label"))
		return
	}
	acct := strings.TrimSpace(r.FormValue("account"))
	if acct == "" {
		acct = c.be.id.Account()
	}
	action := strings.TrimSpace(r.FormValue("action"))
	if action == "" {
		action = "SendMessage"
	}
	if err := c.be.AddPermission(r.Context(), name, label, acct, action); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Permission “"+label+"” added")
	c.sqsConfigPartial(w, r, name)
}

func (c *Console) sqsRemovePermission(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("queue")
	if err := c.be.RemovePermission(r.Context(), name, r.FormValue("label")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Permission removed")
	c.sqsConfigPartial(w, r, name)
}

// sqsCancelMove stops an in-progress redrive. Locally the move already
// completed synchronously, so this reliably answers "task is not active" —
// which is exactly what AWS says about a finished task. The button makes the
// real call and shows the real answer rather than pretending either way.
func (c *Console) sqsCancelMove(w http.ResponseWriter, r *http.Request) {
	if err := c.be.CancelMessageMoveTask(r.Context(), r.FormValue("handle")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Move task cancelled")
	c.sqsMessages(w, r)
}

// sqsPermission is one statement of the queue's resource policy, rendered as a
// row you can remove.
type sqsPermission struct {
	Label   string
	Account string
	Action  string
}

// sqsPermissionsOf reads back what AddPermission wrote. The policy is a normal
// IAM document, and AddPermission's statements carry the Label as the Sid —
// which is how RemovePermission finds them again.
func sqsPermissionsOf(policy string) []sqsPermission {
	if strings.TrimSpace(policy) == "" {
		return nil
	}
	var doc struct {
		Statement []struct {
			Sid       string `json:"Sid"`
			Principal any    `json:"Principal"`
			Action    any    `json:"Action"`
		} `json:"Statement"`
	}
	if json.Unmarshal([]byte(policy), &doc) != nil {
		return nil
	}
	out := make([]sqsPermission, 0, len(doc.Statement))
	for _, st := range doc.Statement {
		if st.Sid == "" {
			continue
		}
		out = append(out, sqsPermission{
			Label: st.Sid, Account: flattenPolicyValue(st.Principal), Action: flattenPolicyValue(st.Action),
		})
	}
	return out
}

// flattenPolicyValue renders the string-or-list-or-object shapes an IAM
// document uses for a single display cell. Deliberately lossy and display-only:
// nothing here is parsed back into a policy.
func flattenPolicyValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, flattenPolicyValue(e))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, flattenPolicyValue(e))
		}
		sort.Strings(parts)
		return strings.Join(parts, ", ")
	}
	return ""
}
