// Package emitter carries, on a context, what emitted the next piece of work
// doze-aws does: "s3:ObjectCreated:Put" for a bucket's notification.
//
// It exists for a caller that knows what emitted a delivery and whose delivery
// goes through a helper that does not: S3 calls the same SQS send an SNS fan-out
// does, and the wire should say "via s3:ObjectCreated:Put" on the one and "via
// sns:topic" on the other. trace.Step records it on the step's event when the
// event names none of its own, and starts the work inside the step with none, so
// the emitter never travels further than the step it was set for.
//
// It is internal, and not a part of package trace, because trace is public API
// and this is plumbing between two packages of this module.
package emitter

import "context"

type key struct{}

// With says what emitted the next step run under ctx.
func With(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, key{}, via)
}

// Of is what With said, or "" when nothing did.
func Of(ctx context.Context) string {
	v, _ := ctx.Value(key{}).(string)
	return v
}
