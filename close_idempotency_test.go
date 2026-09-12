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

// Stack and Shared are what an embedder and the binary actually hold, and both
// were a step worse than the services: nilling the closers slice made a second
// SEQUENTIAL close a clean no-op, so the obvious test passed, while two closes
// at once raced on that field outright. Confirmed as a real `WARNING: DATA
// RACE` before the fix — unlike the service-level concurrent case below, this
// one reproduced on the first run.
//
// Reachable the ordinary way: a signal handler closing the stack while the
// defer in main also closes it.
func TestTopLevelClosesAreSafeTwiceAndConcurrently(t *testing.T) {
	id := awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"}
	quiet := func(string, ...any) {}
	// Three services rather than all 17: this is about the closer loop, not the
	// services under it, and those have their own test above.
	cfg := func(t *testing.T) StackConfig {
		return StackConfig{DataDir: t.TempDir(), Identity: id,
			Services: []string{"s3", "sqs", "logs"}, Logf: quiet}
	}

	t.Run("stack twice", func(t *testing.T) {
		st, err := NewStack(cfg(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		_ = st.Close()
	})

	t.Run("stack concurrently", func(t *testing.T) {
		st, err := NewStack(cfg(t))
		if err != nil {
			t.Fatal(err)
		}
		closeFromManyGoroutines(func() { _ = st.Close() })
	})

	t.Run("shared concurrently", func(t *testing.T) {
		sh, err := NewShared(cfg(t))
		if err != nil {
			t.Fatal(err)
		}
		closeFromManyGoroutines(func() { _ = sh.Close() })
	})
}

// closeFromManyGoroutines runs close from several goroutines released together.
func closeFromManyGoroutines(closeFn func()) {
	const n = 8
	start := make(chan struct{})
	done := make(chan struct{}, n)
	for range n {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			closeFn()
		}()
	}
	close(start)
	for range n {
		<-done
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

			closeFromManyGoroutines(func() { _ = closer.Close() })
		})
	}
}
