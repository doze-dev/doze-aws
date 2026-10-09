package logship

// The shutdown race, which this package had and metricship — its near-twin —
// did not, because metricship got a test and this did not.
//
// Flush runs on a timer goroutine, so it can be anywhere in its body when a
// service closes, including between releasing the lock and sending the batch
// on. Closing the work channel in Close leaves that window a panic: "send on
// closed channel", from a goroutine with no connection to whatever triggered
// the shutdown.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

func TestFlushRacingCloseDoesNotPanic(t *testing.T) {
	// peers.None() has no logs service, so shipping fails with ErrNoLogs —
	// the shape a stack without the logs service has, and it exercises the
	// shipper's own machinery rather than a service's.
	for range 50 {
		s := New("test", peers.None(), func(string, ...any) {})
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					s.Put("/g", "s", Event{Message: "line"})
					s.Flush()
				}
			}()
		}
		go s.Close()
		wg.Wait()
		s.Close() // idempotent, and the second call must not block
	}
}

func TestCloseIsIdempotentAndPutAfterCloseIsInert(t *testing.T) {
	s := New("test", peers.None(), func(string, ...any) {})
	s.Close()
	s.Close()
	// A producer writing a line on a shutdown path — a Step Functions
	// execution finishing as the stack closes — must not panic or block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Put("/g", "s", Event{Message: "late"})
		s.Flush()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Put or Flush blocked after Close")
	}
}

type fakeLogs struct{ url string }

func (f fakeLogs) Endpoint(string) (peers.Endpoint, bool) {
	return peers.Endpoint{Client: http.DefaultClient, BaseURL: f.url}, true
}

// PutLogEvents refuses an event whose message is empty, and refuses the batch
// that holds it. A blank line in a traceback must not take the rest with it.
func TestBlankLinesAreNotShipped(t *testing.T) {
	var mu sync.Mutex
	var puts [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("X-Amz-Target"), "PutLogEvents") {
			var req struct {
				LogEvents []struct{ Message string } `json:"logEvents"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var msgs []string
			for _, e := range req.LogEvents {
				msgs = append(msgs, e.Message)
			}
			mu.Lock()
			puts = append(puts, msgs)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	s := New("test", fakeLogs{srv.URL}, func(string, ...any) {})
	s.Put("/g", "s", Event{Timestamp: 1, Message: "first"}, Event{Timestamp: 2, Message: ""}, Event{Timestamp: 3, Message: "third"})
	s.Put("/g", "s2", Event{Timestamp: 1, Message: ""}) // nothing but a blank: no call at all
	s.Flush()
	s.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 || len(puts[0]) != 2 || puts[0][0] != "first" || puts[0][1] != "third" {
		t.Errorf("PutLogEvents calls = %v, want one call with first and third", puts)
	}
}
