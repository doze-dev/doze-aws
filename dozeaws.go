// Package dozeaws assembles the doze-aws services into one embeddable stack:
// every enabled service constructed over a shared data root, wired to each
// other in-process, and fronted by the shared-endpoint gateway. This is what
// the doze-aws binary serves, and what a Go program embeds when it wants all
// of local AWS behind a single http.Handler:
//
//	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: "./data"})
//	defer stack.Close()
//	http.ListenAndServe("127.0.0.1:4566", stack.Handler())
//
// Programs that want a single service (their own process supervision, custom
// wiring) skip this package and construct the service directly — every service
// package (sts, sqs, ...) exports New(Options) returning an http.Handler +
// io.Closer.
package dozeaws

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/sts"
)

// Implemented lists the services this build of doze-aws can serve, in gateway
// order. It grows phase by phase; gateway.Services is the full roadmap set.
var Implemented = []string{"sts"}

// StackConfig configures a Stack.
type StackConfig struct {
	// DataDir is the root under which each service gets its own subdirectory.
	// Required once any stateful service is enabled; the Phase-1 services are
	// stateless and tolerate it empty.
	DataDir string
	// Services to enable; nil enables every implemented service. Unknown or
	// unimplemented names are an error.
	Services []string
	// Logf receives service and gateway log lines; nil discards.
	Logf func(format string, args ...any)
	// S3Host is the host under which virtual-hosted-style S3 bucket addressing
	// is detected (unused until the s3 service lands; reserved in config now
	// so files written today keep working).
	S3Host string
}

// Stack is a running set of services behind one gateway.
type Stack struct {
	gw      *gateway.Gateway
	closers []io.Closer
}

// NewStack constructs and wires the requested services.
func NewStack(cfg StackConfig) (*Stack, error) {
	names := cfg.Services
	if names == nil {
		names = Implemented
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	gw := gateway.New(gateway.Options{Logf: logf})
	st := &Stack{gw: gw}
	for _, name := range names {
		if !gateway.KnownService(name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: unknown service %q (known: %s)", name, strings.Join(gateway.Services, ", "))
		}
		if !slices.Contains(Implemented, name) {
			st.Close()
			return nil, fmt.Errorf("dozeaws: service %q is not implemented yet (implemented: %s)", name, strings.Join(Implemented, ", "))
		}
		h, closer, err := st.build(name, cfg, logf)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("dozeaws: start %s: %w", name, err)
		}
		gw.Register(name, h)
		if closer != nil {
			st.closers = append(st.closers, closer)
		}
	}
	return st, nil
}

// build constructs one service. Cross-service wiring uses peers.InProcess over
// the gateway's registry, so a service finds its siblings no matter the
// construction order.
func (st *Stack) build(name string, cfg StackConfig, logf func(string, ...any)) (http.Handler, io.Closer, error) {
	dataDir := ""
	if cfg.DataDir != "" {
		dataDir = filepath.Join(cfg.DataDir, name)
	}
	switch name {
	case "sts":
		s, err := sts.New(sts.Options{DataDir: dataDir, Logf: logf})
		return s, s, err
	}
	return nil, nil, fmt.Errorf("no constructor for %q", name)
}

// Handler returns the shared-endpoint gateway handler.
func (s *Stack) Handler() http.Handler { return s.gw }

// Service returns one service's handler (bypassing gateway routing), or nil if
// it isn't enabled — useful for mounting a service on its own listener.
func (s *Stack) Service(name string) http.Handler { return s.gw.Handler(name) }

// Close shuts every service down, releasing stores and background janitors.
func (s *Stack) Close() error {
	var firstErr error
	for _, c := range s.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.closers = nil
	return firstErr
}
