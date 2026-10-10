package aws

import (
	"context"

	"github.com/doze-dev/doze-aws/module/awslocal"
	"github.com/doze-dev/doze-sdk/engine"
)

// DescribeConsole implements engine.Console. The description and the calls it
// needs live in awslocal, where they are tested without the rest of the
// module: the stack's own console already serves the feed, and the dash used to
// read it itself.
func (Driver) DescribeConsole() engine.ConsoleSpec { return awslocal.ConsoleSpec() }

// Tail implements engine.Console: the wire, every API call as it happens.
func (Driver) Tail(ctx context.Context, _ engine.Instance, ep engine.Endpoint, panel, _ string, out chan<- engine.Row) error {
	if panel != "wire" {
		<-ctx.Done()
		return nil
	}
	return awslocal.TailWire(ctx, ep.Backend, out)
}
