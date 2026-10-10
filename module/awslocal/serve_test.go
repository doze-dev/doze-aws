package awslocal

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestServeHealthAndHandler proves the foundation harness end to end: Serve
// mounts the automatic health endpoint, serves the registered handler over a
// unix socket, and shuts down (closing the service) on SIGTERM.
func TestServeHealthAndHandler(t *testing.T) {
	closed := make(chan struct{})
	RegisterServer("echotest", func(datadir string) (http.Handler, io.Closer, error) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello from "+datadir)
		})
		return h, closerFunc(func() error { close(closed); return nil }), nil
	})

	dir := t.TempDir()
	socket := filepath.Join(dir, "echotest.sock")

	done := make(chan error, 1)
	go func() { done <- Serve("echotest", socket, filepath.Join(dir, "data")) }()

	client := unixClient(socket)
	waitListening(t, client)

	// Auto health endpoint.
	if code, _ := get(t, client, HealthPath); code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", code)
	}
	// Registered handler.
	if code, body := get(t, client, "/anything"); code != http.StatusOK || body == "" {
		t.Fatalf("handler: got %d %q", code, body)
	}

	// SIGTERM -> graceful shutdown closes the service.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("service Closer was not called on shutdown")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func unixClient(socket string) *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
}

func waitListening(t *testing.T, c *http.Client) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ := get(t, c, HealthPath); code == http.StatusOK {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start listening")
}

func get(t *testing.T, c *http.Client, path string) (int, string) {
	t.Helper()
	resp, err := c.Get("http://unix" + path)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
