package awslocal

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doze-dev/doze-sdk/engine"
)

// stack serves what the local AWS stack's console serves, on a unix socket.
func stack(t *testing.T, glance, resources string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "al")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_console/api/glance", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(glance)) })
	mux.HandleFunc("/_console/api/resources", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(resources)) })
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

const glanceJSON = `{
  "services": [
    {"svc": "sqs", "label": "2 queues", "state": "3 msgs", "calls": 12},
    {"svc": "s3", "label": "1 bucket", "calls": 4},
    {"svc": "ddb", "label": "1 table", "calls": 0}
  ],
  "wire": [
    {"seq": 7, "t": "12:00:01", "svc": "sqs", "action": "SendMessage", "res": "orders", "code": 200, "ms": 3.2},
    {"seq": 8, "t": "12:00:02", "svc": "s3", "action": "GetObject", "res": "uploads/x", "code": 404, "ms": 1.1},
    {"seq": 9, "t": "12:00:03", "svc": "ddb", "action": "PutItem", "res": "sessions", "code": 500, "ms": 9.0, "err": true}
  ],
  "recorder": true,
  "somethingNew": {"added": "by the server later"}
}`

const resourcesJSON = `[
  {"s": "sqs", "n": "orders", "u": "/_console/sqs/orders"},
  {"s": "s3", "n": "uploads", "u": "/_console/s3/uploads"},
  {"s": "sqs", "n": "emails", "u": "/_console/sqs/emails"}
]`

func TestConsoleResourcesAreServicesThenResources(t *testing.T) {
	sock := stack(t, glanceJSON, resourcesJSON)
	rs, err := ConsoleResources(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	var services, resources []engine.Resource
	for _, r := range rs {
		switch r.Kind {
		case "service":
			services = append(services, r)
		case "resource":
			resources = append(resources, r)
		}
	}
	if len(services) != 3 || services[0].Name != "dynamodb" || services[2].Name != "sqs" {
		t.Fatalf("services (display names, sorted) = %+v", services)
	}
	if services[2].Status != "2 queues" || services[2].Info["calls"] != "12" || services[2].Info["state"] != "3 msgs" {
		t.Errorf("sqs = %+v", services[2])
	}
	if len(resources) != 3 || resources[0].Name != "uploads" || resources[1].Name != "emails" || resources[1].Info["service"] != "sqs" {
		t.Fatalf("resources (by service, then name) = %+v", resources)
	}
}

func TestTheWireIsStreamedOnceEachAndToned(t *testing.T) {
	sock := stack(t, glanceJSON, resourcesJSON)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := make(chan engine.Row, 16)
	done := make(chan error, 1)
	go func() { done <- TailWire(ctx, sock, rows) }()

	var got []engine.Row
	for len(got) < 3 {
		select {
		case r := <-rows:
			got = append(got, r)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d rows arrived", len(got))
		}
	}
	if got[0].Cells["action"] != "SendMessage" || got[0].Tone != "" || got[1].Tone != "warn" || got[2].Tone != "bad" {
		t.Fatalf("rows = %+v", got)
	}
	// The ring is read again each second; nothing is sent twice.
	select {
	case r := <-rows:
		t.Fatalf("a call was sent twice: %+v", r)
	case <-time.After(2500 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSpecIsAllData(t *testing.T) {
	spec := ConsoleSpec()
	kinds := map[string]string{}
	for _, p := range spec.Panels {
		kinds[p.ID] = p.Kind
	}
	if kinds["services"] != "counters" || kinds["resources"] != "table" || kinds["wire"] != "stream" || spec.WebURL == "" {
		t.Fatalf("spec = %+v", spec)
	}
}
