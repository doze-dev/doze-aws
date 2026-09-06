package lambda_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
