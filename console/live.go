package console

import "context"

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
