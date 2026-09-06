package console

import (
	"context"
	"encoding/json"
	"html/template"
	"strconv"
	"strings"
)

// serviceCounts is the stack's census, keyed by console service key.
//
// It exists because the rail's numbers used to arrive by a different route than
// every other number on the page. The rail rendered EMPTY count slots and a 5s
// fetch(/api/counts) filled them in — which meant a queue you had just created
// did not appear in the rail for up to five seconds, and never at all while the
// tab was in the background, because the poller returns early on document.hidden.
//
// Now render() calls this on every page and the numbers ship with the markup,
// so they are correct before the first paint rather than five seconds after it.
//
// Sequential on purpose. The count-only probes are cheap — measured end to end,
// all thirteen take 0.6ms against a page render of 0.77ms — so the concurrency
// and the short-TTL cache this looked like it needed are both complexity against
// a non-problem. The N+1 fan-out that the apiCounts comment warns about lives in
// the full List* helpers, which the Count* probes exist precisely to avoid.
//
// Errors are swallowed per service, deliberately: one unreachable service should
// blank its own badge, not fail the page it appears on.
func (c *Console) serviceCounts(ctx context.Context) map[string]int {
	counts := map[string]int{}
	if v, err := c.be.ListBuckets(ctx); err == nil { // single call already
		counts["s3"] = len(v)
	}
	if n, err := c.be.CountQueues(ctx); err == nil {
		counts["sqs"] = n
	}
	if n, err := c.be.CountTables(ctx); err == nil {
		counts["ddb"] = n
	}
	if n, err := c.be.CountTopics(ctx); err == nil {
		counts["sns"] = n
	}
	if n, err := c.be.CountBuses(ctx); err == nil {
		counts["eb"] = n
	}
	if v, err := c.be.ListFunctions(ctx); err == nil { // single call already
		counts["lambda"] = len(v)
	}
	if n, err := c.be.CountKeys(ctx); err == nil {
		counts["kms"] = n
	}
	if n, err := c.be.CountStreams(ctx); err == nil {
		counts["kinesis"] = n
	}
	if n, err := c.be.CountStacks(ctx); err == nil {
		counts["cfn"] = n
	}
	if n, err := c.be.CountRestAPIs(ctx); err == nil {
		counts["apigw"] = n
	}
	if n, err := c.be.CountStateMachines(ctx); err == nil {
		counts["sfn"] = n
	}
	if n, err := c.be.CountPrincipals(ctx); err == nil {
		counts["iam"] = n
	}
	if v, err := c.be.ListParameters(ctx); err == nil { // single call already
		counts["ssm"] = len(v)
	}
	if v, err := c.be.ListSecrets(ctx); err == nil { // single call already
		counts["sm"] = len(v)
	}
	return counts
}

// resSlug makes a resource name safe to put in a DOM id AND safe to address
// with a CSS selector.
//
// The second half is the one that bites. htmx builds an out-of-band target by
// string concatenation — "#" + id — so a FIFO queue named "orders.fifo" would
// produce "#lr-sqs-orders.fifo", which parses as the element #lr-sqs-orders
// that also has class .fifo. Nothing matches, and htmx's miss is SILENT: its
// querySelectorAllExt returns an empty array rather than null, so the patch is
// dropped with no error event and no swap. A live count that quietly stops
// updating for exactly the queues whose names carry a dot is a worse bug than
// one that throws.
//
// Anything outside [A-Za-z0-9_-] becomes "_", and a short fingerprint of the
// original is appended so two names that collapse to the same mangling —
// "a.b" and "a-b" — cannot land on each other's slot.
func resSlug(s string) string {
	if s == "" {
		return ""
	}
	b := make([]byte, 0, len(s)+9)
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b = append(b, c)
		default:
			b = append(b, '_')
			safe = false
		}
	}
	if safe {
		return string(b)
	}
	return string(b) + "-" + contentHash(s)[:8]
}

// humanSecs renders a seconds-valued queue attribute the way a person says it.
//
// SQS reports these as strings of seconds, so a four-day retention arrives as
// "345600" and rendered straight it says "345600 s" — technically the value and
// practically unreadable, which is how a summary row starts looking unfinished.
//
// Deliberately the same shape as window.dozeDur in layout.html, which writes
// the live echo under duration INPUTS ("= 4 days"). The number a form promises
// while you type and the number the summary shows afterwards should not be
// phrased differently.
//
// Empty renders as an em dash, per Cloudscape's empty-value rule: a blank cell
// reads as "not loaded", a dash reads as "there is nothing here".
func humanSecs(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	// Humanise only where it earns its keep. "4 days" beats "345600 s"; "1 min
	// 17s" is worse than "77s", because a visibility timeout is a number you
	// TYPED in seconds and want to read back in seconds. An hour is the point
	// where the raw figure stops being legible at a glance.
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 3600 {
		if err != nil {
			return s
		}
		return strconv.Itoa(n) + "s"
	}
	units := []struct {
		name string
		secs int
	}{{"day", 86400}, {"hour", 3600}, {"min", 60}, {"s", 1}}
	var parts []string
	for _, u := range units {
		if len(parts) == 2 {
			break
		}
		if q := n / u.secs; q > 0 {
			label := u.name
			if q > 1 && label != "s" {
				label += "s"
			}
			if label == "s" {
				parts = append(parts, strconv.Itoa(q)+label)
			} else {
				parts = append(parts, strconv.Itoa(q)+" "+label)
			}
			n -= q * u.secs
		}
	}
	return strings.Join(parts, " ")
}

// tagsJSON renders a tag list as the JSON literal an x-data attribute needs.
//
// template.JS rather than a string: the value is being placed inside an HTML
// attribute that Alpine will evaluate as JavaScript, and html/template escapes
// a plain string for HTML text rather than for a JS expression — which turns a
// quote in a tag value into &#34; and the x-data into a syntax error. Marshal
// already produces valid JS for this shape, and the values it carries are tag
// keys and values that came from the service.
func tagsJSON(tags []KV) template.JS {
	type kv struct {
		K string `json:"k"`
		V string `json:"v"`
	}
	out := make([]kv, 0, len(tags))
	for _, t := range tags {
		out = append(out, kv{K: t.K, V: t.V})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return template.JS("[]")
	}
	return template.JS(b)
}
