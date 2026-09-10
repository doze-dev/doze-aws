// Package bg contains panics on background goroutines.
//
// doze-aws runs about thirty goroutines that no request is waiting on:
// retention sweepers, the CloudWatch alarm evaluator, the Step Functions
// driver, Lambda's event-source pollers, the log and metric shippers, and a
// handful of fire-and-forget deliveries. net/http recovers a panic in a
// handler, so a request can only take itself down — but nothing recovers
// these, and a panic on any one of them kills the process and every one of
// the seventeen services with it.
//
// That is the wrong failure for a development tool. The stack trace names a
// goroutine the developer never started, the emulator disappears mid-test,
// and the actual cause — one malformed payload, one nil map — is three
// frames from anything they wrote.
//
// # Containment differs by what the goroutine is
//
// There is no single right answer, so this package offers three, and the
// caller picks by shape:
//
//	Tick     a ticker loop — sweepers, janitors, the evaluator. The body is
//	         idempotent and runs again in a minute, so a panic is logged and
//	         the loop carries on. Losing one sweep is not worth losing the
//	         process.
//
//	Go       a one-shot task — an alarm action, an S3 notification, an
//	         asynchronous invoke. Nothing retries it and nothing is waiting,
//	         so the panic is logged and that single delivery is lost.
//
//	Recover  a long-lived worker draining a queue. It cannot simply resume:
//	         its channel would go unserviced forever while producers kept
//	         filling it. So the caller passes what to do about that, and the
//	         convention in this tree is to mark the worker dead so enqueue
//	         starts REPORTING drops rather than silently swallowing them.
//
// # Why Recover has to be registered second
//
// Deferred calls run last-registered-first. A queue worker's body opens with
// `defer close(f.done)`, which is what Close waits on — and that fires during
// a panic unwind just as happily as during a clean return. Register Recover
// AFTER it:
//
//	defer close(f.done)                        // runs second
//	defer bg.Recover(f.logf, "logs: fan-out", f.die)  // runs first
//
// so the panic is contained and the worker marked dead before Close is told
// the worker finished. The other order compiles, runs, and reports a clean
// shutdown of a goroutine that just died.
package bg

import "runtime/debug"

// logger is the log function every service already carries as its `logf`
// field. Both spellings in this tree — `func(format string, args ...any)` and
// `func(string, ...any)` — are assignable to it.
type logger func(string, ...any)

// Recover contains a panic and runs each cleanup, in order. Use it as a
// deferred call at the top of a goroutine body; see the package comment for
// why its position among the other defers matters.
//
// name is the goroutine, in the tree's usual "<service>: <what>" form — it is
// the only thing tying the report back to something the reader recognises.
func Recover(logf logger, name string, cleanup ...func()) {
	r := recover()
	if r == nil {
		return
	}
	report(logf, name, r)
	for _, fn := range cleanup {
		fn()
	}
}

// Go launches fn on a goroutine whose panic is logged rather than fatal. For
// work nothing is waiting on and nothing will retry.
func Go(logf logger, name string, fn func()) {
	go func() {
		defer Recover(logf, name)
		fn()
	}()
}

// Tick runs one iteration of a loop, containing a panic so the loop survives
// it. Called from inside the loop, not around it: the point is that the next
// tick still happens.
func Tick(logf logger, name string, fn func()) {
	defer Recover(logf, name)
	fn()
}

// report writes the panic where the reader will see it. The stack is included
// in full and deliberately: a contained panic produces no crash dump, so this
// line is the only record that it happened at all.
func report(logf logger, name string, r any) {
	if logf == nil {
		return
	}
	logf("%s: recovered from a panic — this is a bug in doze-aws, please report it: %v\n%s",
		name, r, debug.Stack())
}
