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
// # What is not traced, and why that is honest
//
// Only synchronous fan-out from a request is linked. A poller — an SQS event
// source mapping, a DynamoDB stream reader — runs on its own goroutine with no
// request context, so the invoke it eventually performs has no parent here and
// appears as a root. That is a real gap rather than a rendering choice: the
// causal chain genuinely breaks at a queue, because the message outlives the
// request that sent it. AWS closes that gap by carrying a trace header on the
// message itself, and doing the same is the next step, not something to fake by
// guessing which recent request probably caused which later delivery.
package trace

import (
	"context"
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
