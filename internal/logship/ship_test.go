package logship

// What the shipper does with a batch once it has one: it puts it in time order
// (PutLogEvents refuses a batch that is not, whole), it makes the group and the
// stream again if somebody deleted them, it says when it fails, and it flushes
// by itself, without anyone calling Flush.

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

// logsServer is a logs service that records every call by operation and
// answers PutLogEvents from a script: each entry is the error code to answer
// with ("" for success), consumed in order, and success after the script ends.
type logsServer struct {
	mu     sync.Mutex
	calls  []string
	puts   [][]Event
	script []string
}

func (l *logsServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.Header.Get("X-Amz-Target")
		op = op[strings.LastIndex(op, ".")+1:]
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		l.mu.Lock()
		defer l.mu.Unlock()
		l.calls = append(l.calls, op)
		if op != "PutLogEvents" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req struct {
			LogEvents []struct {
				Timestamp int64
				Message   string
			} `json:"logEvents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var got []Event
		for _, e := range req.LogEvents {
			got = append(got, Event{Timestamp: e.Timestamp, Message: e.Message})
		}
		l.puts = append(l.puts, got)
		if len(l.script) > 0 {
			code := l.script[0]
			l.script = l.script[1:]
			if code != "" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"__type":"` + code + `","message":"scripted"}`))
				return
			}
		}
		_, _ = w.Write([]byte(`{}`))
	})
}

func (l *logsServer) count(op string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c == op {
			n++
		}
	}
	return n
}

// noLogs is a stack started without the logs service.
type noLogs struct{}

func (noLogs) Endpoint(string) (peers.Endpoint, bool) { return peers.Endpoint{}, false }

type lines struct {
	mu sync.Mutex
	s  []string
}

func (l *lines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.s = append(l.s, format)
}

func (l *lines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.s...)
}

func TestABatchIsShippedInTimeOrderAndEqualTimesKeepTheirArrivalOrder(t *testing.T) {
	logs := &logsServer{}
	srv := httptest.NewServer(logs.handler())
	defer srv.Close()
	s := New("test", fakeLogs{srv.URL}, (&lines{}).logf)
	s.Put("/g", "s",
		Event{Timestamp: 30, Message: "c"}, Event{Timestamp: 10, Message: "a1"},
		Event{Timestamp: 20, Message: "b"}, Event{Timestamp: 10, Message: "a2"}, Event{Timestamp: 10, Message: "a3"})
	s.Flush()
	s.Close()
	if len(logs.puts) != 1 {
		t.Fatalf("%d puts, want one", len(logs.puts))
	}
	var got []string
	for _, e := range logs.puts[0] {
		got = append(got, e.Message)
	}
	if want := "a1 a2 a3 b c"; strings.Join(got, " ") != want {
		t.Errorf("shipped %v, want %q", got, want)
	}
}

// A group or a stream deleted underneath the shipper is made again, once, and
// the batch goes out; it is not dropped, and nothing is said about it.
func TestAStreamDeletedUnderTheShipperIsMadeAgain(t *testing.T) {
	logs := &logsServer{script: []string{"ResourceNotFoundException"}}
	srv := httptest.NewServer(logs.handler())
	defer srv.Close()
	said := &lines{}
	s := New("test", fakeLogs{srv.URL}, said.logf)
	s.Put("/g", "s", Event{Timestamp: 1, Message: "kept"})
	s.Flush()
	s.Close()
	if n := logs.count("PutLogEvents"); n != 2 {
		t.Errorf("%d PutLogEvents calls, want 2 (refused, then again)", n)
	}
	if n := logs.count("CreateLogStream"); n != 2 {
		t.Errorf("%d CreateLogStream calls, want 2 (first use, then after the deletion)", n)
	}
	if len(logs.puts) == 2 && (len(logs.puts[1]) != 1 || logs.puts[1][0].Message != "kept") {
		t.Errorf("the second put carried %v, want the same batch", logs.puts[1])
	}
	if got := said.all(); len(got) != 0 {
		t.Errorf("a recovered shipment said %v", got)
	}
}

// When the second try fails as well, that is said once, with the error; a
// shipment that worked says nothing.
func TestAFailedShipmentIsSaidOnceAndASuccessfulOneNotAtAll(t *testing.T) {
	logs := &logsServer{script: []string{"ResourceNotFoundException", "ResourceNotFoundException"}}
	srv := httptest.NewServer(logs.handler())
	defer srv.Close()
	said := &lines{}
	s := New("test", fakeLogs{srv.URL}, said.logf)
	s.Put("/g", "s", Event{Timestamp: 1, Message: "lost"})
	s.Flush()
	s.Close()
	got := said.all()
	if len(got) != 1 || got[0] != "%s: shipping logs: %v" {
		t.Errorf("said %v, want one shipping-logs line", got)
	}
	if n := logs.count("PutLogEvents"); n != 2 {
		t.Errorf("%d PutLogEvents calls, want 2 (not a loop)", n)
	}

	ok := &logsServer{}
	srv2 := httptest.NewServer(ok.handler())
	defer srv2.Close()
	quiet := &lines{}
	s2 := New("test", fakeLogs{srv2.URL}, quiet.logf)
	s2.Put("/g", "s", Event{Timestamp: 1, Message: "fine"})
	s2.Flush()
	s2.Close()
	if got := quiet.all(); len(got) != 0 {
		t.Errorf("a shipment that worked said %v", got)
	}
}

// The logger given to New is the one used, and none at all is allowed.
func TestTheLoggerGivenIsTheOneUsed(t *testing.T) {
	said := &lines{}
	s := New("test", noLogs{}, said.logf)
	s.Put("/g", "s", Event{Timestamp: 1, Message: "x"})
	s.Flush()
	s.Close()
	got := said.all()
	if len(got) != 1 || got[0] != "%s: the logs service is not enabled; log lines are dropped" {
		t.Errorf("said %v, want the one 'not enabled' line", got)
	}

	// No logger: nothing to print to, and nothing to panic on.
	n := New("test", noLogs{}, nil)
	n.Put("/g", "s", Event{Timestamp: 1, Message: "x"})
	n.Flush()
	n.Close()
}

// Events are shipped on their own within the flush interval, with nobody
// calling Flush: the first Put arms the timer.
func TestEventsFlushThemselves(t *testing.T) {
	logs := &logsServer{}
	srv := httptest.NewServer(logs.handler())
	defer srv.Close()
	s := New("test", fakeLogs{srv.URL}, (&lines{}).logf)
	defer s.Close()
	s.Put("/g", "s", Event{Timestamp: 1, Message: "by itself"})
	deadline := time.Now().Add(5 * time.Second)
	for logs.count("PutLogEvents") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nothing was shipped without a Flush")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
