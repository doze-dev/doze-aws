package aws

import (
	"context"
	"fmt"

	"github.com/doze-dev/doze-aws/module/awslocal"
	"github.com/doze-dev/doze-sdk/engine"
)

// Resources implements engine.Admin: the services in use and the resources of
// the stack, read from the stack's own console feed (see awslocal's console).
// The services come first, as counters; the resources follow, as a table.
func (Driver) Resources(ctx context.Context, _ engine.Instance, ep engine.Endpoint) ([]engine.Resource, error) {
	return awslocal.ConsoleResources(ctx, ep.Backend)
}

// Actions implements engine.Admin: none — the web console is the interaction
// surface for everything inside this instance.
func (Driver) Actions() []engine.Action { return nil }

// Run implements engine.Admin.
func (Driver) Run(context.Context, engine.Instance, engine.Endpoint, string, string, string) (string, error) {
	return "", fmt.Errorf("aws: use the web console (press o)")
}
