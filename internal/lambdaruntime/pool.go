package lambdaruntime

import (
	"context"
	"sync"
)

// DefaultPoolMax is the concurrency ceiling for a function with no reserved
// concurrency configured — how many child processes can run its invocations at
// once. Kept modest so a local stack doesn't fork a process per request.
const DefaultPoolMax = 5

// Pool runs a function's invocations across a growable set of Runners (one child
// process each), giving up to Max concurrent executions. It grows lazily to
// match observed concurrency and never exceeds Max — the local analogue of
// Lambda's per-function concurrency. Under serial load it stays at one Runner.
type Pool struct {
	spec Spec
	logf func(string, ...any)
	max  int

	mu       sync.Mutex
	runners  []*Runner
	rr       int
	inflight int
}

// NewPool builds a pool. max <= 0 uses DefaultPoolMax.
func NewPool(spec Spec, max int, logf func(string, ...any)) *Pool {
	if max <= 0 {
		max = DefaultPoolMax
	}
	return &Pool{spec: spec, logf: logf, max: max}
}

// Invoke runs one invocation on a pooled Runner, spawning another (up to Max)
// when concurrent demand exceeds the current pool size.
func (p *Pool) Invoke(ctx context.Context, payload []byte) (Result, error) {
	r := p.acquire()
	defer p.release()
	return r.Invoke(ctx, payload)
}

func (p *Pool) acquire() *Runner {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inflight++
	// Grow toward the current concurrency, capped at max. A serial caller keeps
	// the pool at one runner; N concurrent callers grow it to min(N, max).
	want := p.inflight
	if want > p.max {
		want = p.max
	}
	for len(p.runners) < want {
		p.runners = append(p.runners, NewRunner(p.spec, p.logf))
	}
	r := p.runners[p.rr%len(p.runners)]
	p.rr++
	return r
}

func (p *Pool) release() {
	p.mu.Lock()
	p.inflight--
	p.mu.Unlock()
}

// Stop tears down every child process in the pool.
func (p *Pool) Stop() {
	p.mu.Lock()
	runners := p.runners
	p.runners = nil
	p.mu.Unlock()
	for _, r := range runners {
		r.Stop()
	}
}

// Size returns the current number of live runners (for tests/introspection).
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.runners)
}
