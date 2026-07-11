package lambdaruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// sleepBootstrap is a Runtime API client that sleeps before responding, so
// concurrent invocations are observable by wall-clock time.
const sleepBootstrap = `package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	for {
		resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next")
		if err != nil { os.Exit(1) }
		reqID := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		time.Sleep(700 * time.Millisecond)
		http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+reqID+"/response",
			"application/json", bytes.NewReader([]byte(` + "`" + `{"ok":true}` + "`" + `)))
	}
}
`

func buildSleepBootstrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(sleepBootstrap), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module bootstrap\n\ngo 1.26\n"), 0o644)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v\n%s", err, out)
	}
	return dir
}

// TestPoolConcurrency proves the pool runs invocations in parallel: three
// invocations held in flight together grow the pool to three runners. Size()==3
// is a deterministic concurrency proof — the pool only grows past one runner
// when inflight exceeds the current pool size, which can't happen if the
// invocations serialized (cf. TestPoolSerialStaysSmall). It's independent of
// wall-clock timing, so it's stable under parallel test load.
func TestPoolConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildSleepBootstrap(t)
	p := NewPool(Spec{
		Name:    "sleeper",
		Command: []string{"./bootstrap"},
		Dir:     dir,
		Timeout: 10 * time.Second,
	}, 5, nil)
	defer p.Stop()

	const n = 3
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each invocation holds a runner for 700ms (the sleep), so all three
			// overlap — driving inflight to 3 and the pool to 3 runners.
			_, errs[i] = p.Invoke(context.Background(), []byte(`{}`))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
	}
	if sz := p.Size(); sz != n {
		t.Fatalf("pool size = %d, want %d — invocations did not run concurrently", sz, n)
	}
}

// TestPoolSerialStaysSmall proves the pool doesn't over-provision: serial
// invocations reuse a single runner.
func TestPoolSerialStaysSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildSleepBootstrap(t)
	p := NewPool(Spec{Name: "s", Command: []string{"./bootstrap"}, Dir: dir, Timeout: 5 * time.Second}, 5, nil)
	defer p.Stop()

	for i := 0; i < 3; i++ {
		if _, err := p.Invoke(context.Background(), []byte(`{}`)); err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
	}
	if sz := p.Size(); sz != 1 {
		t.Fatalf("serial pool grew to %d runners, want 1", sz)
	}
}

// TestPoolScalesToZeroWhenIdle proves a warm pool reaps its child process after
// the idle window, then respawns lazily on the next invoke — so an unused
// function stops holding memory.
func TestPoolScalesToZeroWhenIdle(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a lambda process")
	}
	dir := buildSleepBootstrap(t)
	p := NewPool(Spec{Name: "idler", Command: []string{"./bootstrap"}, Dir: dir, Timeout: 5 * time.Second}, 5, nil)
	defer p.Stop()
	p.SetIdleTimeout(150 * time.Millisecond)

	if _, err := p.Invoke(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	if sz := p.Size(); sz != 1 {
		t.Fatalf("after invoke, pool size = %d, want 1 (warm)", sz)
	}

	// Wait past the idle window; the reaper scales the pool to zero.
	deadline := time.Now().Add(3 * time.Second)
	for p.Size() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("idle pool never scaled to zero, size = %d", p.Size())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A fresh invoke respawns a runner.
	if _, err := p.Invoke(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("invoke after reap: %v", err)
	}
	if sz := p.Size(); sz != 1 {
		t.Fatalf("after respawn, pool size = %d, want 1", sz)
	}
}
