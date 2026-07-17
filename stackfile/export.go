package stackfile

// Export orchestration: the service-by-service walk over the running stack.
// Per-service export helpers live in the svc_*.go files.

import (
	"context"
	"net/http"
)

// Export reads the running stack and renders it as a Stack — the inverse of
// Apply, so a team can click a stack together in the console and commit the
// file. Secret and SecureString values are deliberately NOT exported; the
// header comment in Marshal explains the blank.
func Export(ctx context.Context, gateway http.Handler) (*Stack, error) {
	c := newClient(gateway)
	s := &Stack{}

	if err := exportQueues(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportTables(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportBuckets(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportFunctions(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportTopics(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportRules(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportKeys(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportSecrets(ctx, c, s); err != nil {
		return nil, err
	}
	if err := exportParameters(ctx, c, s); err != nil {
		return nil, err
	}
	return s, nil
}
