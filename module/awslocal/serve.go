package awslocal

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/doze-dev/doze-sdk/engine"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

// ServerFactory builds a service's HTTP handler over its data directory. The
// returned io.Closer (may be nil) is closed on shutdown so the service can flush
// and persist state before the process exits.
type ServerFactory func(datadir string) (http.Handler, io.Closer, error)

var factories = map[string]ServerFactory{}

// RegisterServer makes a service available to Serve under name. Service
// implementations call this from an init function; cmd/doze blank-imports them.
func RegisterServer(name string, f ServerFactory) { factories[name] = f }

// Services returns the registered service names, sorted (for diagnostics).
func Services() []string {
	out := make([]string, 0, len(factories))
	for n := range factories {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Serve runs the named service on a unix socket until it receives SIGINT or
// SIGTERM, then gracefully drains and closes it. It is the body of the hidden
// `doze __serve <service>` subcommand that BaseDriver.Spawn invokes.
func Serve(name, socket, datadir string) error {
	f, ok := factories[name]
	if !ok {
		return fmt.Errorf("unknown aws service %q (have %v)", name, Services())
	}
	if socket == "" {
		return fmt.Errorf("--socket is required")
	}
	if err := os.MkdirAll(datadir, 0o700); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}
	_ = os.Remove(socket) // clear any stale socket from a crash

	handler, closer, err := f(datadir)
	if err != nil {
		return err
	}

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", socket, err)
	}
	srv := &http.Server{Handler: withHealth(handler)}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-sig:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	if closer != nil {
		_ = closer.Close()
	}
	return nil
}

// PluginMain is the whole main() of a local-AWS engine plugin. The binary is
// dual-purpose: invoked plainly it speaks the plugin protocol (via
// plugin.Main, which gob-registers config and serves drv); invoked as
// `<plugin> __serve <service> …` (what BaseDriver.Plan spawns — the plugin
// self-execs os.Executable()) it runs the service itself via ServeFromArgs.
// A module's plugin/main.go collapses to a one-liner:
//
//	func main() { awslocal.PluginMain(sqs.New(), &sqs.Config{}) }
func PluginMain(drv engine.Driver, config any) {
	if len(os.Args) > 1 && os.Args[1] == "__serve" {
		if err := ServeFromArgs(os.Args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	dozeplugin.Main(drv, config)
}

// ServeFromArgs runs the `__serve` subcommand straight from a plugin's os.Args
// ([bin, "__serve", <name>, "--socket", s, "--datadir", d]). An engine plugin
// calls it so the same binary that speaks the plugin protocol can also be the
// service it self-execs to run (BaseDriver.Plan spawns os.Executable()).
func ServeFromArgs(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: __serve <service> --socket <path> --datadir <dir>")
	}
	name := args[2]
	fs := flag.NewFlagSet("__serve", flag.ContinueOnError)
	socket := fs.String("socket", "", "unix socket to listen on")
	datadir := fs.String("datadir", "", "service data directory")
	if err := fs.Parse(args[3:]); err != nil {
		return err
	}
	return Serve(name, *socket, *datadir)
}

// withHealth answers HealthPath itself (200 "ok") and delegates everything else
// to the service handler, so WaitReady has a uniform readiness probe.
func withHealth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == HealthPath {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		h.ServeHTTP(w, r)
	})
}
