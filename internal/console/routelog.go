//go:build !e2e

package console

import "net/http"

// noteRoute records which route served a request, for the e2e suite's route
// gate (routelog_e2e.go). A release build records nothing.
func (c *Console) noteRoute(*http.Request) {}
