package lambda_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestServer starts an httptest server for h and returns its URL.
func newTestServer(t *testing.T, h http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

// buildIn compiles the bootstrap in dir (main.go + go.mod already written)
// and returns dir, the code directory a _local_ function points at.
func buildIn(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "bootstrap"), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build bootstrap: %v\n%s", err, out)
	}
	return dir
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// logCollector is a Logf that keeps every line. Function output reaches it
// from the sink's worker and the child's copier goroutines, so it locks.
type logCollector struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCollector) Logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

// String joins what was collected so far.
func (c *logCollector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// skipWithoutPython skips a test that runs a Python handler when no
// interpreter is on PATH.
func skipWithoutPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 on PATH")
	}
}
