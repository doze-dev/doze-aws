// Package trace links an external API call to the internal work it sets off.
//
// doze-aws runs every service in one process, so when a PutObject fires a
// bucket notification that sends to a queue and invokes a function, all of it
// happens inside the request that started it. Nothing outside could reconstruct
// that: the notification never crosses the gateway, so the console's recorder
// cannot see it, and AWS itself cannot tell you either — CloudTrail records the
// API call and nothing about what it caused.
//
// That is the whole reason this exists. Owning the process is the one advantage
// a local emulator has over the real thing, and a cascade is what it buys.
//
// # Why the sink travels in the context
//
// The obvious design is a package-level sink installed at startup. It is also
// wrong here: the test suite boots many independent stacks in one process, and
// a global would let one stack's cascades land in another's recorder. Carrying
// the sink in the request context keeps each stack's tracing to itself, costs
// no locking, and makes "not traced" the natural default for any code path
// without a request behind it.
//
// # Across a queue
//
// Synchronous fan-out is linked by the request context. That is not enough on
// its own: a message outlives the request that sent it, so an SQS event source
// mapping picking it up later has no context to inherit and the chain would end
// at the queue — which is the most common shape there is, S3 to SQS to Lambda.
//
// So the cause travels ON the message, in the AWSTraceHeader system attribute,
// which is exactly what X-Ray does and why that attribute exists. A poller
// reads it back and continues the chain, and because it is a SYSTEM attribute
// it stays out of the application's own attributes and out of their MD5.
//
// # What is still not traced
//
// DynamoDB streams and Kinesis have no equivalent per-record place to put a
// header, so an invoke driven by one of those appears as a root. A message sent
// by something other than doze-aws carries no header and does the same. Both
// are honest outcomes: a root means "nothing told us what caused this", not
// "nothing did".
package trace

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Cause identifies the recorded external call that set this work in motion.
// Zero means "not caused by anything we recorded".
type Cause int64

// Event is one piece of internal work, attributed to the call that caused it.
type Event struct {
	// Self is this step's own id, reserved before the work runs so anything it
	// causes can name it as a parent. Without that, a two-hop chain flattens:
	// the second hop would still be pointing at the original request, because
	// the first hop's id does not exist until it finishes.
	Self     Cause
	Cause    Cause
	Service  string // the service doing the work: sqs, sns, lambda
	Action   string // SendMessage, Publish, Invoke
	Resource string // queue, topic or function name
	Via      string // what emitted it, e.g. "s3:ObjectCreated:Put"
	At       time.Time
	Millis   float64
	Err      string // non-empty when the delivery failed
}

// Sink receives cascade events. Implementations must be safe for concurrent
// use: a single request can fan out to several targets at once.
type Sink interface {
	// ReserveCascade allocates an id for work about to happen.
	ReserveCascade() Cause
	EmitCascade(Event)
}

type key struct{}

type carrier struct {
	sink  Sink
	cause Cause
}

// With attaches a sink and the causing call to a request's context. The
// recorder does this once, at the edge; everything downstream just emits.
func With(ctx context.Context, s Sink, c Cause) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, key{}, carrier{sink: s, cause: c})
}

// CauseOf reports the call this work descends from, or 0.
func CauseOf(ctx context.Context) Cause {
	if c, ok := ctx.Value(key{}).(carrier); ok {
		return c.cause
	}
	return 0
}

// Traced reports whether this context carries a sink — useful to skip building
// an event nobody will read.
func Traced(ctx context.Context) bool {
	_, ok := ctx.Value(key{}).(carrier)
	return ok
}

// Emit records one cascade event. It fills in Cause and At when unset, and is
// a no-op without a sink, so instrumented code needs no conditionals.
func Emit(ctx context.Context, e Event) {
	c, ok := ctx.Value(key{}).(carrier)
	if !ok {
		return
	}
	if e.Cause == 0 {
		e.Cause = c.cause
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	c.sink.EmitCascade(e)
}

// Step runs one piece of caused work, timing it and recording it as a child of
// whatever caused the current context.
//
// fn receives a DIFFERENT context, whose cause is this step. That is what makes
// depth work: an S3 put causes a publish, and the publish causes a send, so the
// send has to descend from the publish rather than from the put. Re-parenting
// here is also why the id is reserved up front — the work inside fn runs, and
// needs a parent to name, before this step has finished and been recorded.
func Step(ctx context.Context, e Event, fn func(context.Context) error) error {
	c, ok := ctx.Value(key{}).(carrier)
	if !ok {
		return fn(ctx)
	}
	self := c.sink.ReserveCascade()
	e.Self, e.Cause = self, c.cause
	start := time.Now()
	err := fn(With(ctx, c.sink, self))
	e.Millis = float64(time.Since(start)) / float64(time.Millisecond)
	if err != nil {
		e.Err = err.Error()
	}
	if e.At.IsZero() {
		e.At = start
	}
	c.sink.EmitCascade(e)
	return err
}

// ---- propagation across a queue ----

// A cause is only meaningful inside the process that issued it: ids restart at
// zero when doze-aws does. A message that outlived a restart would otherwise
// carry a number that now belongs to somebody else's call, and the wire would
// draw a confident, wrong line between two unrelated things. So the run is
// named alongside the parent, and a mismatched run is ignored rather than
// trusted.
//
// The shape mirrors X-Ray's AWSTraceHeader — Root and Parent — because that is
// the field this travels in and the format anything else reading it expects.
const headerFormat = "Root=%s;Parent=%d"

var headerRE = regexp.MustCompile(`^Root=([^;]+);Parent=(\d+)$`)

// Runner identifies the process that issued a cause. A Sink implements it so a
// consumer can tell its own ids from a previous run's.
type Runner interface {
	RunID() string
}

// Header renders the current cause for travel on a message, or "" when there is
// nothing to propagate.
func Header(ctx context.Context) string {
	c, ok := ctx.Value(key{}).(carrier)
	if !ok || c.cause == 0 {
		return ""
	}
	r, ok := c.sink.(Runner)
	if !ok {
		return ""
	}
	return fmt.Sprintf(headerFormat, r.RunID(), c.cause)
}

// Continue rebuilds a traced context from a header carried on a message.
//
// This is the seam a poller uses: it has no request context of its own, so the
// message is the only thing that knows what caused it. A header from another
// run is dropped, which shows the delivery as a root — the same honest outcome
// as no header at all.
func Continue(ctx context.Context, s Sink, header string) context.Context {
	if s == nil || header == "" {
		return ctx
	}
	m := headerRE.FindStringSubmatch(header)
	if m == nil {
		return ctx
	}
	r, ok := s.(Runner)
	if !ok || m[1] != r.RunID() {
		return ctx
	}
	parent, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil || parent == 0 {
		return ctx
	}
	return With(ctx, s, Cause(parent))
}
