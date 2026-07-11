package lambdaruntime

import (
	"context"
	"sync"
	"time"
)

// DefaultPoolMax is the concurrency ceiling for a function with no reserved
// concurrency configured — how many child processes can run its invocations at
// once. Kept modest so a local stack doesn't fork a process per request.
const DefaultPoolMax = 5

// DefaultIdleTimeout is how long a fully-idle pool keeps its warm child
// processes before reaping them to zero. Long enough that active development
// keeps warm-start latency, short enough that a walked-away-from stack releases
// the memory. AWS reaps idle execution environments on a similar horizon.
const DefaultIdleTimeout = 10 * time.Minute

// Pool runs a function's invocations across a growable set of Runners (one child
// process each), giving up to Max concurrent executions. It grows lazily to
// match observed concurrency and never exceeds Max — the local analogue of
// Lambda's per-function concurrency. Under serial load it stays at one Runner.
//
// When the pool goes fully idle (no invocation in flight) it scales back to
// zero after idleTimeout: every warm process is stopped and the next invoke
// spawns a fresh one. So a function you stop calling stops costing memory.
type Pool struct {
	spec Spec
	logf func(string, ...any)
	max  int

	mu           sync.Mutex
	runners      []*Runner
	rr           int
	inflight     int
	idle         time.Duration
	idleTimer    *time.Timer
	idleDeadline time.Time // when the armed timer will scale the pool to zero
	closed       bool
}

// NewPool builds a pool. max <= 0 uses DefaultPoolMax.
func NewPool(spec Spec, max int, logf func(string, ...any)) *Pool {
	if max <= 0 {
		max = DefaultPoolMax
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Pool{spec: spec, logf: logf, max: max, idle: DefaultIdleTimeout}
}

// SetIdleTimeout overrides how long a fully-idle pool stays warm before it
// scales to zero. A value <= 0 restores DefaultIdleTimeout. Safe to call before
// the pool is first used.
func (p *Pool) SetIdleTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultIdleTimeout
	}
	p.mu.Lock()
	p.idle = d
	p.mu.Unlock()
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
	// Any demand cancels a pending scale-to-zero.
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	p.idleDeadline = time.Time{}
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
	// Fully idle: arm the scale-to-zero timer. Reset on each drain to zero so
	// the window measures continuous idleness, not time since the last spawn.
	if p.inflight == 0 && !p.closed {
		if p.idleTimer != nil {
			p.idleTimer.Stop()
		}
		p.idleTimer = time.AfterFunc(p.idle, p.reapIdle)
		p.idleDeadline = time.Now().Add(p.idle)
	}
	p.mu.Unlock()
}

// reapIdle stops every warm runner if the pool is still idle when the timer
// fires. The next Invoke respawns lazily via acquire.
func (p *Pool) reapIdle() {
	p.mu.Lock()
	if p.closed || p.inflight != 0 {
		p.mu.Unlock()
		return
	}
	runners := p.runners
	idle := p.idle
	p.runners = nil
	p.rr = 0
	p.idleTimer = nil
	p.idleDeadline = time.Time{}
	p.mu.Unlock()
	for _, r := range runners {
		r.Stop()
	}
	if n := len(runners); n > 0 {
		p.logf("lambda %s: reaped %d idle runner(s) after %s", p.spec.Name, n, idle)
	}
}

// Stop tears down every child process in the pool.
func (p *Pool) Stop() {
	p.mu.Lock()
	p.closed = true
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	p.idleDeadline = time.Time{}
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

// IdleTimeout returns the pool's scale-to-zero window.
func (p *Pool) IdleTimeout() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idle
}

// SleepDeadline reports when the warm pool will scale to zero and whether a
// countdown is currently running (true only when warm and idle). It returns
// the zero time and false when the pool is cold or actively executing.
func (p *Pool) SleepDeadline() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.runners) == 0 || p.idleDeadline.IsZero() {
		return time.Time{}, false
	}
	return p.idleDeadline, true
}
