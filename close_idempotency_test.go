package dozeaws

// Every service must survive a second Close.
//
// The service packages are exported for direct embedding — dozeaws.go's own doc
// advertises it — so an embedder writes `defer svc.Close()` and then closes
// again on an error path, or two goroutines shut down at once. Most Close
// bodies did `close(s.stop)` unguarded, which is `panic: close of closed
// channel`, and Stack.Close hid it by closing each service exactly once.
//
// This drives st.build rather than a hand-written list, so a service added
// later is covered the day it appears in Implemented instead of the day
// somebody remembers to extend a table.

import (
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/gateway"
)

func TestASecondCloseIsNotAPanic(t *testing.T) {
	for _, name := range Implemented {
		t.Run(name, func(t *testing.T) {
			id := awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"}
			quiet := func(string, ...any) {}
			cfg := StackConfig{DataDir: t.TempDir(), Identity: id, Logf: quiet}
			st := &Stack{gw: gateway.New(gateway.Options{Logf: quiet, Identity: id}), id: id}

			_, closer, err := st.build(name, cfg, quiet)
			if err != nil {
				t.Fatalf("build %s: %v", name, err)
			}
			if closer == nil {
				t.Skipf("%s has no closer", name)
			}

			if err := closer.Close(); err != nil {
				t.Fatalf("first Close: %v", err)
			}
			// The second one is the test. A panic here fails the run; an error
			// does not — bbolt reports one for an already-closed DB on some
			// paths, and that is honest rather than a crash.
			_ = closer.Close()
		})
	}
}

// The same hazard from several goroutines at once.
//
// cloudwatch used to guard with `select { case <-s.stop: default: close(...) }`,
// which is check-then-act: two goroutines can both take the default and both
// close. It passes the sequential test above, which is why it survived.
//
// Be clear about what this test is worth: the window is a few nanoseconds wide,
// and against that old guard it took `-count=300` to hit (it did, reliably:
// `panic: close of closed channel`). One pass of this test is not evidence the
// guard is sound. TestASecondCloseIsNotAPanic is the deterministic one; this
// documents the concurrent requirement and will catch a regression eventually.
func TestConcurrentClosesDoNotPanic(t *testing.T) {
	for _, name := range Implemented {
		t.Run(name, func(t *testing.T) {
			id := awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"}
			quiet := func(string, ...any) {}
			cfg := StackConfig{DataDir: t.TempDir(), Identity: id, Logf: quiet}
			st := &Stack{gw: gateway.New(gateway.Options{Logf: quiet, Identity: id}), id: id}

			_, closer, err := st.build(name, cfg, quiet)
			if err != nil {
				t.Fatalf("build %s: %v", name, err)
			}
			if closer == nil {
				t.Skipf("%s has no closer", name)
			}

			const n = 8
			start := make(chan struct{})
			done := make(chan struct{}, n)
			for range n {
				go func() {
					defer func() { done <- struct{}{} }()
					<-start
					_ = closer.Close()
				}()
			}
			close(start)
			for range n {
				<-done
			}
		})
	}
}
