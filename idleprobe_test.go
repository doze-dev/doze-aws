package dozeaws_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"testing"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
)

// TestIdleFootprint is a probe, not an assertion: it boots the full stack, lets
// it settle, and reports where an idle process's memory and goroutines go.
// Skipped unless DOZE_IDLE_PROBE is set so it never runs in the normal suite.
func TestIdleFootprint(t *testing.T) {
	if os.Getenv("DOZE_IDLE_PROBE") == "" {
		t.Skip("set DOZE_IDLE_PROBE=1 to run the idle footprint probe")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()

	time.Sleep(3 * time.Second) // let every janitor arm and settle

	// What does a process with nothing to do actually spend its time on?
	cf, err := os.Create("/tmp/doze-idle-cpu.pprof")
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(cf); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	pprof.StopCPUProfile()
	cf.Close()
	fmt.Println("idle CPU profile -> /tmp/doze-idle-cpu.pprof (30s)")

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	mb := func(b uint64) string { return fmt.Sprintf("%.1f MB", float64(b)/1024/1024) }

	fmt.Println("=== idle footprint, all 17 services ===")
	fmt.Println("goroutines      ", runtime.NumGoroutine())
	fmt.Println("HeapAlloc       ", mb(m.HeapAlloc), "(live heap)")
	fmt.Println("HeapInuse       ", mb(m.HeapInuse))
	fmt.Println("HeapIdle        ", mb(m.HeapIdle), "(spans waiting to be reused)")
	fmt.Println("HeapReleased    ", mb(m.HeapReleased), "(given back to the OS)")
	fmt.Println("HeapSys         ", mb(m.HeapSys))
	fmt.Println("StackInuse      ", mb(m.StackInuse), "(goroutine stacks)")
	fmt.Println("MSpanInuse      ", mb(m.MSpanInuse))
	fmt.Println("MCacheInuse     ", mb(m.MCacheInuse))
	fmt.Println("GCSys           ", mb(m.GCSys), "(GC metadata)")
	fmt.Println("OtherSys        ", mb(m.OtherSys))
	fmt.Println("Sys TOTAL       ", mb(m.Sys), "(everything Go asked the OS for)")
	fmt.Println("NumGC           ", m.NumGC)

	// How much of the reservation is Go holding that the OS could have back?
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	debug.FreeOSMemory()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	fmt.Println("--- debug.FreeOSMemory() ---")
	fmt.Println("HeapReleased   ", mb(before.HeapReleased), "->", mb(after.HeapReleased))
	fmt.Println("HeapIdle       ", mb(before.HeapIdle), "->", mb(after.HeapIdle))
	fmt.Println("Sys            ", mb(before.Sys), "->", mb(after.Sys))
	fmt.Println("retained (Sys-Released)", mb(before.Sys-before.HeapReleased),
		"->", mb(after.Sys-after.HeapReleased))

	f, err := os.Create("/tmp/doze-idle-heap.pprof")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Fatal(err)
	}
	fmt.Println("heap profile -> /tmp/doze-idle-heap.pprof")

	gf, err := os.Create("/tmp/doze-idle-goroutine.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close()
	if err := pprof.Lookup("goroutine").WriteTo(gf, 1); err != nil {
		t.Fatal(err)
	}
	fmt.Println("goroutine dump -> /tmp/doze-idle-goroutine.txt")
}
