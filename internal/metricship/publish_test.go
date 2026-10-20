package metricship

// What reaches CloudWatch: the observations that were put, in calls no larger
// than AWS allows, on their own within the flush interval, and one line when
// publishing fails. Until now the package's tests only held its shutdown race,
// and an observation that never left, or a publish whose deadline was zero,
// would have passed them.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/doze-dev/doze-aws/peers"
)

type fakeCW struct{ url string }

func (f fakeCW) Endpoint(string) (peers.Endpoint, bool) {
	return peers.Endpoint{Client: http.DefaultClient, BaseURL: f.url}, true
}

type cwServer struct {
	mu    sync.Mutex
	calls []int // MetricData length of each PutMetricData
	names []string
	// values and units are what each datum carried, in the order published.
	values []float64
	units  []string
	fail   bool
}

func (c *cwServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace  string
			MetricData []struct {
				MetricName string
				Value      float64
				Unit       string
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls = append(c.calls, len(req.MetricData))
		for _, m := range req.MetricData {
			c.names = append(c.names, req.Namespace+"/"+m.MetricName)
			c.values = append(c.values, m.Value)
			c.units = append(c.units, m.Unit)
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		if c.fail {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"__type":"InvalidParameterValue","message":"scripted"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
}

func (c *cwServer) seen() ([]int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.calls...), append([]string(nil), c.names...)
}

type said struct {
	mu sync.Mutex
	s  []string
}

func (l *said) logf(format string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.s = append(l.s, format)
}

func (l *said) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.s...)
}

func TestAnObservationIsPublished(t *testing.T) {
	cw := &cwServer{}
	srv := httptest.NewServer(cw.handler())
	defer srv.Close()
	log := &said{}
	s := New("test", fakeCW{srv.URL}, log.logf)
	s.Count("Shop", "Orders", map[string]string{"Stage": "prod"}, 3)
	s.Duration("Shop", "Latency", nil, 250*time.Millisecond)
	s.Flush()
	s.Close()
	calls, names := cw.seen()
	if len(calls) != 1 || calls[0] != 2 {
		t.Errorf("PutMetricData calls %v, want one carrying both", calls)
	}
	if len(names) != 2 || names[0] != "Shop/Orders" || names[1] != "Shop/Latency" {
		t.Errorf("published %v", names)
	}
	if got := log.all(); len(got) != 0 {
		t.Errorf("a publish that worked said %v", got)
	}
	// A count is a Count of the value given; a duration is MILLISECONDS, so a
	// quarter of a second is 250.
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if len(cw.values) != 2 || cw.values[0] != 3 || cw.units[0] != "Count" || cw.values[1] != 250 || cw.units[1] != "Milliseconds" {
		t.Errorf("published values %v units %v, want 3 Count then 250 Milliseconds", cw.values, cw.units)
	}
}

// Nothing is sent for an empty Put or an empty Flush.
func TestNothingPutIsNothingSent(t *testing.T) {
	cw := &cwServer{}
	srv := httptest.NewServer(cw.handler())
	defer srv.Close()
	s := New("test", fakeCW{srv.URL}, (&said{}).logf)
	s.Put("Shop")
	s.Flush()
	s.Close()
	if calls, _ := cw.seen(); len(calls) != 0 {
		t.Errorf("calls %v, want none", calls)
	}
}

// AWS takes at most 1000 data per call; a burst is cut there, in order, and
// every observation is still sent.
func TestABurstIsCutAtTheCeilingPerCall(t *testing.T) {
	cw := &cwServer{}
	srv := httptest.NewServer(cw.handler())
	defer srv.Close()
	s := New("test", fakeCW{srv.URL}, (&said{}).logf)
	data := make([]Datum, 2500)
	for i := range data {
		data[i] = Datum{MetricName: "M", Value: float64(i), Unit: "Count"}
	}
	s.Put("Burst", data...)
	s.Flush()
	s.Close()
	calls, _ := cw.seen()
	if len(calls) != 3 || calls[0] != 1000 || calls[1] != 1000 || calls[2] != 500 {
		t.Errorf("calls %v, want 1000, 1000, 500", calls)
	}
}

// A publish that fails is said once with the error, and the rest of the burst
// is not tried against the same failure.
func TestAFailedPublishIsSaidAndStopsTheBurst(t *testing.T) {
	cw := &cwServer{fail: true}
	srv := httptest.NewServer(cw.handler())
	defer srv.Close()
	log := &said{}
	s := New("test", fakeCW{srv.URL}, log.logf)
	data := make([]Datum, 1500)
	for i := range data {
		data[i] = Datum{MetricName: "M", Value: 1}
	}
	s.Put("Burst", data...)
	s.Flush()
	s.Close()
	if got := log.all(); len(got) != 1 || got[0] != "%s: publishing metrics: %v" {
		t.Errorf("said %v, want one publishing-metrics line", got)
	}
	if calls, _ := cw.seen(); len(calls) != 1 {
		t.Errorf("calls %v, want one: the burst stops at the first failure", calls)
	}
}

// The first Put arms the timer, so observations leave on their own.
func TestObservationsPublishThemselves(t *testing.T) {
	cw := &cwServer{}
	srv := httptest.NewServer(cw.handler())
	defer srv.Close()
	s := New("test", fakeCW{srv.URL}, (&said{}).logf)
	defer s.Close()
	s.Count("Shop", "Orders", nil, 1)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if calls, _ := cw.seen(); len(calls) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing was published without a Flush")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The logger given to New is the one used; none at all is allowed.
func TestTheLoggerGivenIsTheOneUsed(t *testing.T) {
	log := &said{}
	s := New("test", peers.None(), log.logf)
	s.Count("Shop", "Orders", nil, 1)
	s.Flush()
	s.Close()
	if got := log.all(); len(got) != 1 || got[0] != "%s: the cloudwatch service is not enabled; metrics are dropped" {
		t.Errorf("said %v, want the one 'not enabled' line", got)
	}
	n := New("test", peers.None(), nil)
	n.Count("Shop", "Orders", nil, 1)
	n.Flush()
	n.Close()
}
