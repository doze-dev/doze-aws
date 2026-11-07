package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/doze-dev/doze-aws/internal/bg"
)

// serveOn runs srv on one listener until it closes, reporting a genuine
// failure on errc.
func serveOn(srv *http.Server, ln net.Listener, logger *slog.Logger, errc chan<- error) {
	if ln == nil {
		return
	}
	bg.Go(slogf(logger), "doze-aws: listener "+ln.Addr().String(), func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			select {
			case errc <- err:
			default: // another listener got there first; one report is enough
			}
		}
	})
}

// slogf adapts an *slog.Logger to the logf shape the rest of the tree uses,
// so cmd/ can hand one to internal/bg like any service does.
func slogf(logger *slog.Logger) func(string, ...any) {
	return func(format string, args ...any) {
		logger.Error(fmt.Sprintf(format, args...))
	}
}
