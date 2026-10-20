package console

// What the shell's top bar and rail ask the server for on every render: the
// IAM mode, the rail's default state for the page, and the rail's status dots.
// Each is answered from something already in memory, because the shell draws on
// every page and idle weight is a feature.

import (
	"context"
	"encoding/xml"
	"time"
)

// iamModeRetry is how long a failed read of the IAM mode is believed. A stack
// started without IAM answers every ask the same way, and asking on every
// render to be told so again is the cost this exists to avoid.
const iamModeRetry = 10 * time.Second

// iamModeCached is the stack's IAM mode as the top bar words it: "soft",
// "enforce" or "off".
//
// The mode is fixed when the stack is built (iam.Options.Mode, no setter), so
// one successful read is the answer for the life of the process and no render
// calls the service again. The read is the access-log call, which is the one
// place the service reports its mode; it carries the log's entries too, which
// is why this is once and not per page. A service that is not there is "off":
// nothing is deciding anything, which is what the pill claims.
func (c *Console) iamModeCached(ctx context.Context) string {
	c.chromeMu.Lock()
	defer c.chromeMu.Unlock()
	if c.iamMode != "" {
		return c.iamMode
	}
	if !c.iamTried.IsZero() && time.Since(c.iamTried) < iamModeRetry {
		return "off"
	}
	mode, err := c.be.iamMode(ctx)
	if err != nil || mode == "" {
		c.iamTried = time.Now()
		return "off"
	}
	c.iamMode = mode
	return mode
}

// iamMode asks the IAM service which mode it runs in.
func (b *backend) iamMode(ctx context.Context) (string, error) {
	body, err := b.iam(ctx, "DozeAccessLog", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Mode string `xml:"DozeAccessLogResult>Mode"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Mode, nil
}

// railDefault is the rail's state for a page when the user has not chosen:
// labelled on the wire, marks only everywhere else.
func railDefault(page string) string {
	if page == "traffic" {
		return "wide"
	}
	return "slim"
}

// troubleWindow is how far back a refused or failed call still counts toward a
// service's dot. A minute is the window the rest of the console calls recent
// (glanceWindow), so the dot and the deck's "5/min" mean the same stretch.
const troubleWindow = time.Minute

// dlqTTL is how long "some dead-letter queue holds a message" is believed. The
// probe reads every queue's attributes, which a page render and a five-second
// poll in every open tab should not each pay for.
const dlqTTL = 5 * time.Second

// railWarn is the rail's status dots: {service: "warn" | "bad"}, from the three
// facts the console already has and nothing it would have to invent.
//
//   - an alarm in ALARM is CloudWatch's (bad); the caller read the list already
//   - a dead-letter queue holding a message is SQS's (warn)
//   - a call the wire shows refused, denied or failed in the last minute is its
//     service's: warn for a 4xx, bad for a 5xx. A 404 alone is not, because
//     "is it there?" is asked that way (HeadObject on an absent key).
//
// When two facts name one service the worse one wins. The map is never nil.
func (c *Console) railWarn(ctx context.Context, anyQueues, alarmFiring bool) map[string]string {
	warn := map[string]string{}
	raise := func(svc, level string) {
		if level == "bad" || warn[svc] == "" {
			warn[svc] = level
		}
	}
	if c.rec != nil {
		for svc, level := range c.rec.trouble(time.Now(), troubleWindow) {
			raise(svc, level)
		}
	}
	if alarmFiring {
		raise("cw", "bad")
	}
	if anyQueues && c.be.dlqHolds(ctx) {
		raise("sqs", "warn")
	}
	return warn
}

// dlqHolds reports whether some queue that others dead-letter into holds a
// message, recomputed at most every dlqTTL. A console mutation drops the cache
// with the wiring graph (bustGraph), so the user's own send shows at once and
// the TTL only bounds how stale the user's APP's traffic can look.
func (b *backend) dlqHolds(ctx context.Context) bool {
	b.graphMu.Lock()
	if !b.dlqAt.IsZero() && time.Since(b.dlqAt) < dlqTTL {
		held := b.dlqHeld
		b.graphMu.Unlock()
		return held
	}
	gen := b.graphGen
	b.graphMu.Unlock()
	held := false
	if queues, err := b.ListQueues(ctx); err == nil {
		for _, q := range queues {
			if q.IsDLQ && q.Available > 0 {
				held = true
				break
			}
		}
	}
	b.graphMu.Lock()
	if b.graphGen == gen { // not over a drop that landed while this was being read
		b.dlqAt, b.dlqHeld = time.Now(), held
	}
	b.graphMu.Unlock()
	return held
}

// trouble is what the ring says about each service since now-window: "bad" for
// a call that failed (5xx: doze-aws or the service broke), "warn" for one a
// policy denied. A service with only good calls is absent, and so is one with
// only refusals: an app that sends a bad name now and then is being told no,
// as it should be, and a dot that stood for that would stand on every busy
// app and mean nothing. (It reads the stamp the service set, never a status
// or a code's spelling.)
func (rec *Recorder) trouble(now time.Time, window time.Duration) map[string]string {
	out := map[string]string{}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := rec.head
	if rec.full {
		n = trafficCap
	}
	for i := 0; i < n; i++ {
		e := &rec.buf[i]
		if now.Sub(e.At) > window {
			continue
		}
		switch {
		case e.Status >= 500:
			out[e.Service] = "bad"
		case e.Denied != "" && e.Status >= 400 && out[e.Service] == "":
			out[e.Service] = "warn"
		}
	}
	return out
}
