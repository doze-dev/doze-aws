package dozeaws

// Serving more than one region from one process.
//
// A Stack is one region. Regions runs several of them behind one address and
// picks per request, which is possible at all because a region is only a
// folder: each Stack opens its stores under <data-dir>/<region>/ and is
// otherwise the same code, unchanged and unaware.
//
// # Why the global services are built once and shared
//
// IAM and STS have no region — they mint arn:aws:iam::<account>:… with an empty
// region segment, and a user created "in" one region is the same user in every
// other. So their data lives in <data-dir>/_global/.
//
// That makes sharing them mandatory rather than merely tidy: bbolt is
// single-writer, so a second Stack opening the same _global/iam would block on
// the file lock forever. They are built once here and REGISTERED into every
// region's gateway, so a service in any region still resolves them through
// peers.InProcess exactly as before.
//
// # How a request finds its region
//
// In order:
//
//  1. The SigV4 credential scope. Every signed SDK request carries one
//     (AKID/date/REGION/service/aws4_request) and it is what the caller
//     actually asked for.
//  2. The configured default, for anything unsigned or SigV2-signed — a
//     browser opening a queue URL, a webhook, curl. SigV2 has no region in it
//     at all, so this is not a fallback for rare cases; it is the answer for a
//     whole protocol.
//
// A hostname label will come first once URLs carry one.

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/gateway"
	"github.com/doze-dev/doze-aws/internal/sigparse"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// Regions serves one Stack per region behind a single handler.
type Regions struct {
	cfg    StackConfig
	shared *Shared
	logf   func(string, ...any)

	// def is the region an unsigned request belongs to.
	def string
	// sink is remembered so a region created later gets it too.
	sink trace.Sink

	mu     sync.RWMutex
	stacks map[string]*stackEntry
}

// stackEntry caches the wrapped handler beside its stack: Stack.Handler builds
// a middleware chain each call, and this is the per-request path.
type stackEntry struct {
	stack *Stack
	h     http.Handler
}

// NewRegions builds a multi-region server. regions are created eagerly so they
// appear in the console from the first second rather than only once something
// touches them; anything else is created on first use.
//
// The configured identity's region is always served, whether or not it is
// listed, because it is where unsigned requests go.
func NewRegions(cfg StackConfig, regions []string) (*Regions, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	shared, err := NewShared(cfg)
	if err != nil {
		return nil, err
	}
	rs := &Regions{
		cfg:    cfg,
		shared: shared,
		logf:   logf,
		def:    cfg.Identity.RegionName(),
		stacks: map[string]*stackEntry{},
	}
	for _, region := range append([]string{rs.def}, regions...) {
		if _, err := rs.stackFor(region); err != nil {
			rs.Close()
			return nil, err
		}
	}
	return rs, nil
}

// Default is the region an unsigned request is served by.
func (rs *Regions) Default() string { return rs.def }

// Serving lists the regions with a live stack, in no particular order.
func (rs *Regions) Serving() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]string, 0, len(rs.stacks))
	for region := range rs.stacks {
		out = append(out, region)
	}
	return out
}

// Stack returns the stack serving a region, or nil if none is running.
func (rs *Regions) Stack(region string) *Stack {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if e, ok := rs.stacks[region]; ok {
		return e.stack
	}
	return nil
}

// RegionOf reports the region a request belongs to.
func (rs *Regions) RegionOf(r *http.Request) string {
	if scope, ok := sigparse.Parse(r); ok && scope.Region != "" {
		return scope.Region
	}
	return rs.def
}

func (rs *Regions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e, err := rs.stackFor(rs.RegionOf(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	e.h.ServeHTTP(w, r)
}

// stackFor returns the region's stack, creating it if this is its first use.
//
// Creation is announced at INFO rather than debug. A region appearing because
// a client's configuration has a typo in it produces an empty region that looks
// exactly like lost data, and the log line is the only thing that tells the two
// apart.
func (rs *Regions) stackFor(region string) (*stackEntry, error) {
	if region == "" {
		region = rs.def
	}
	rs.mu.RLock()
	e, ok := rs.stacks[region]
	rs.mu.RUnlock()
	if ok {
		return e, nil
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	// Another request may have created it between the two locks.
	if e, ok := rs.stacks[region]; ok {
		return e, nil
	}

	cfg := rs.cfg
	cfg.Identity.Region = region
	cfg.Shared = rs.shared
	st, err := NewStack(cfg)
	if err != nil {
		return nil, fmt.Errorf("dozeaws: region %s: %w", region, err)
	}
	if rs.sink != nil {
		st.SetTraceSink(rs.sink)
	}
	e = &stackEntry{stack: st, h: st.Handler()}
	rs.stacks[region] = e
	rs.logf("regions: serving %s (data under %s)", region, cfg.DataDir)
	return e, nil
}

// SetTraceSink passes the sink to every region, and to regions created later.
func (rs *Regions) SetTraceSink(sink trace.Sink) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.sink = sink
	for _, e := range rs.stacks {
		e.stack.SetTraceSink(sink)
	}
}

// Close stops every region, then the shared services.
func (rs *Regions) Close() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var firstErr error
	for _, e := range rs.stacks {
		if err := e.stack.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	rs.stacks = map[string]*stackEntry{}
	if err := rs.shared.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Shared holds the region-less services, built once and registered into every
// region's gateway. See the note at the top of this file for why this is
// required rather than an optimisation.
type Shared struct {
	handlers map[string]http.Handler
	iam      *iam.Server
	closers  []io.Closer
}

// NewShared builds the global services under <data-dir>/_global.
func NewShared(cfg StackConfig) (*Shared, error) {
	sh := &Shared{handlers: map[string]http.Handler{}}
	want := cfg.Services
	if want == nil {
		want = Implemented
	}
	// A bare Stack is used to construct them, so there is exactly one place in
	// this package that knows how to build a service.
	// A real (empty) gateway: build() hands each service peers.InProcess over
	// it. IAM accepts a directory only for constructor uniformity and never
	// calls it, and STS takes none — but depending on that would be a trap for
	// whoever adds the next global service.
	host := &Stack{id: cfg.Identity, gw: gateway.New(gateway.Options{Logf: cfg.Logf, Identity: cfg.Identity})}
	for _, name := range want {
		if !Global[name] {
			continue
		}
		h, closer, err := host.build(name, cfg, cfg.Logf)
		if err != nil {
			sh.Close()
			return nil, fmt.Errorf("dozeaws: start %s: %w", name, err)
		}
		sh.handlers[name] = h
		if closer != nil {
			sh.closers = append(sh.closers, closer)
		}
	}
	sh.iam = host.iam
	return sh, nil
}

// Close shuts the global services down.
func (sh *Shared) Close() error {
	if sh == nil {
		return nil
	}
	var firstErr error
	for _, c := range sh.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	sh.closers = nil
	return firstErr
}
