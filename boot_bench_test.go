package dozeaws_test

// How long does it take to start?
//
// Thirty-seven benchmarks existed before these and not one of them booted a
// stack — request_bench_test.go builds one outside the timed region, which is
// correct for what it measures and leaves this unmeasured. The only startup
// number anywhere was a line of prose in docs/performance.md ("237 ms before,
// 245 ms after"), hand-timed once and reproducible by no command in the repo.
//
// It matters for how the tool feels rather than how fast it is. A stack that
// starts in a quarter of a second can be started per test run, per branch, per
// experiment. One that takes ten seconds is one you leave running and forget
// the state of — which is a different product.
//
// These are published, not gated. .github/workflows/bench.yml already reasoned
// out why a wall-clock threshold on a shared runner fires on noise and gets
// muted, and it was right. The gate is the catastrophe ceiling in
// footprint_test.go, which only catches something blocking; a real slowdown is
// meant to be seen here, with benchstat.

import (
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// BenchmarkBootFullStack is the number a developer actually waits for.
func BenchmarkBootFullStack(b *testing.B) { benchBoot(b, nil) }

// BenchmarkBootTwoServices is the other end: what it costs when you ask for
// only what you need. The gap between the two is the argument for --services.
func BenchmarkBootTwoServices(b *testing.B) { benchBoot(b, []string{"sqs", "s3"}) }

// BenchmarkBootPerService localises the cost. A full-stack boot that got
// slower tells you to look at seventeen packages; this tells you which one.
func BenchmarkBootPerService(b *testing.B) {
	for _, svc := range dozeaws.Implemented {
		b.Run(svc, func(b *testing.B) { benchBoot(b, []string{svc}) })
	}
}

func benchBoot(b *testing.B, services []string) {
	b.ReportAllocs()
	for b.Loop() {
		// A fresh directory each iteration, and it is inside the timed region
		// on purpose: creating the bbolt files IS the boot cost, and a
		// benchmark that reuses a warm directory measures the second start,
		// which is not the one anybody waits for.
		st, err := dozeaws.NewStack(dozeaws.StackConfig{
			DataDir: b.TempDir(), Services: services, Logf: dozetest.Quiet(b)})
		if err != nil {
			b.Fatal(err)
		}
		if err := st.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
