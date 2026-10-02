//go:build unix

package lambdaruntime

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// The function's output, read in step with the function.
//
// A child's output and its result travel by different roads: bytes down a
// pipe, the result over HTTP to the Runtime API. Given cmd.Stdout as a plain
// writer, os/exec reads the pipe on a goroutine of its own, and nothing made
// that goroutine finish before the result was acted on. So END could be
// written, the sink flushed and Invoke returned while the function's last
// lines were still in the pipe — and "Invoke returned" was supposed to mean
// "the lines are queryable". Usually the copy won. Under load it lost: the
// lines went missing from their own invocation, or turned up under the next
// one, since attribution is decided when a line is emitted.
//
// The pump owns the pipe instead. Reading and delivering happen under one
// lock, so there is never a byte that has been read and not yet delivered,
// and drain can empty the pipe on demand: everything the function wrote
// before it reported its result is in the kernel's buffer by then, and a
// non-blocking read takes all of it. The Runner drains before it answers the
// function's result, which is before the function can fetch its next piece of
// work — so a line is always emitted while its own invocation is current.
type outputPump struct {
	mu     sync.Mutex // serialises read-then-deliver; guards closed. Never held across r.Close.
	r      *os.File
	w      *os.File // the child's end; closed here once the child holds it
	fd     int
	out    *lineSplitter
	closed bool
	buf    []byte
}

// newOutputPump points both of cmd's output streams at one pipe.
func newOutputPump(cmd *exec.Cmd, out *lineSplitter) (*outputPump, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	rc, err := r.SyscallConn()
	if err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	p := &outputPump{r: r, w: w, out: out, buf: make([]byte, 32<<10)}
	// The descriptor number, without os.File.Fd's side effect of making it
	// blocking: drain depends on reads that return instead of waiting.
	if err := rc.Control(func(fd uintptr) { p.fd = int(fd) }); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	// One file for both, so stdout and stderr interleave as written.
	cmd.Stdout, cmd.Stderr = w, w

	go func() {
		for {
			var n int
			var rerr error
			err := rc.Read(func(fd uintptr) bool {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.closed {
					n = 0
					return true
				}
				n, rerr = syscall.Read(int(fd), p.buf)
				if rerr == syscall.EAGAIN || rerr == syscall.EINTR {
					return false // nothing yet: wait until there is
				}
				if n > 0 {
					_, _ = p.out.Write(p.buf[:n])
				}
				return true
			})
			if err != nil || rerr != nil || n <= 0 {
				return // closed, failed, or every writer has gone
			}
		}
	}()
	return p, nil
}

// started releases the parent's copy of the child's end, so the pipe reaches
// end-of-file when the child (and anything it spawned) has let go of it.
func (p *outputPump) started() {
	if p != nil && p.w != nil {
		_ = p.w.Close()
		p.w = nil
	}
}

// drain delivers whatever the pipe holds right now, and returns.
func (p *outputPump) drain() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	for {
		n, err := syscall.Read(p.fd, p.buf)
		if err == syscall.EINTR {
			continue
		}
		if n <= 0 || err != nil {
			return // empty (EAGAIN), end-of-file, or broken: nothing more to take
		}
		_, _ = p.out.Write(p.buf[:n])
	}
}

// close ends the pump. Whatever was still in the pipe should be drained first.
func (p *outputPump) close() {
	if p == nil {
		return
	}
	p.started()
	p.mu.Lock()
	was := p.closed
	p.closed = true
	p.mu.Unlock()
	// Closed OUTSIDE the lock. Closing a file waits for whoever is reading it
	// to let go, and the reader takes p.mu for every read: holding the lock
	// here is the reader waiting for close and close waiting for the reader.
	// The flag is what keeps drain off a descriptor that is going away.
	if !was {
		_ = p.r.Close()
	}
}
