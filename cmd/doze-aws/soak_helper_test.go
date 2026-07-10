//go:build soak

package main_test

import (
	"net"
	"net/http"
	"testing"

	"github.com/doze-dev/doze-aws"
)

// serve starts the stack on a background loopback listener and returns its addr.
func serve(t *testing.T, stack *dozeaws.Stack) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: stack.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}
