//go:build !e2e

package console

import "net/http"

// recordRoute notes which route served a request, for the e2e suite's route
// gate (routelog_e2e.go). A release build records nothing.
func (c *Console) recordRoute(next http.Handler) http.Handler { return next }
