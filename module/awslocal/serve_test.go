package awslocal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/doze-dev/doze-sdk/engine"
)

// TestServeHealthAndHandler proves the foundation harness end to end: Serve
// mounts the automatic health endpoint, serves the registered handler over a
// unix socket, and shuts down (closing the service) on SIGTERM.
func TestServeHealthAndHandler(t *testing.T) {
	closed := make(chan struct{})
	RegisterServer("echotest", func(o ServeOptions) (http.Handler, io.Closer, error) {
		datadir := o.DataDir
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello from "+datadir)
		})
		return h, closerFunc(func() error { close(closed); return nil }), nil
	})

	dir := t.TempDir()
	socket := filepath.Join(dir, "echotest.sock")

	done := make(chan error, 1)
	go func() { done <- Serve("echotest", socket, ServeOptions{DataDir: filepath.Join(dir, "data")}) }()

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

// The name doze gave the instance reaches the service: Plan passes it, and
// ServeFromArgs hands it to the factory.
func TestTheGrantedNameReachesTheService(t *testing.T) {
	d := BaseDriver{Name: "namedtest"}
	dir := t.TempDir()
	inst := engine.Instance{Name: "aws", SocketDir: filepath.Join(dir, "s"), DataDir: filepath.Join(dir, "d"),
		Grant: engine.Grant{Host: "aws.shop.doze", Listeners: map[string]engine.GrantedListener{
			engine.MainListener: {Addr: "aws.shop.doze:4566", URL: "http://aws.shop.doze"}}}}
	plan, err := d.Plan(context.Background(), inst, engine.Toolchain{})
	if err != nil {
		t.Fatal(err)
	}
	args := plan.Specs[0].Args
	if !slices.Contains(args, "--suffix") || !slices.Contains(args, "aws.shop.doze") || !slices.Contains(args, "--endpoint") || !slices.Contains(args, "http://aws.shop.doze") {
		t.Fatalf("args = %v", args)
	}
	// No name: neither flag.
	plain, _ := d.Plan(context.Background(), engine.Instance{Name: "aws", SocketDir: inst.SocketDir, DataDir: inst.DataDir}, engine.Toolchain{})
	if slices.Contains(plain.Specs[0].Args, "--suffix") {
		t.Errorf("an unnamed instance was given a suffix: %v", plain.Specs[0].Args)
	}

	got := make(chan ServeOptions, 1)
	RegisterServer("namedtest", func(o ServeOptions) (http.Handler, io.Closer, error) {
		got <- o
		return nil, nil, errors.New("stop here")
	})
	_ = ServeFromArgs(append([]string{"bin"}, args...))
	if o := <-got; o.Suffix != "aws.shop.doze" || o.Endpoint != "http://aws.shop.doze" || o.DataDir != inst.DataDir {
		t.Errorf("the service got %+v", o)
	}
}
