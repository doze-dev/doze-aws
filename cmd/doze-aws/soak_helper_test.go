//go:build soak

package main_test

import (
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
)

// soakServer serves whichever Stack is currently installed, on an address that
// does not change when the stack is replaced.
//
// The stable address is the point. A restart that re-listened would get a new
// port, and the soak workload's state machine has the SQS queue URL — host and
// port included — baked into its ASL definition at creation. Swapping the
// handler under one listener restarts every service and reopens every bbolt
// store while leaving every URL a client or a definition is holding still
// valid, which is what makes the chaos mode test persistence rather than
// re-addressing.
type soakServer struct {
	addr string
	h    atomic.Pointer[http.Handler]
}

func (s *soakServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load()).ServeHTTP(w, r)
}

// install points the server at a stack.
func (s *soakServer) install(stack *dozeaws.Stack) {
	h := stack.Handler()
	s.h.Store(&h)
}

// serve starts a background loopback listener in front of stack.
func serve(t *testing.T, stack *dozeaws.Stack) *soakServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &soakServer{addr: ln.Addr().String()}
	s.install(stack)
	srv := &http.Server{Handler: s}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return s
}
