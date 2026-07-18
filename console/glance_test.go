package console_test

import (
	"encoding/json"
	"net/url"
	"testing"
)

// TestGlance: the one-call dashboard feed — services with counts and
// service-aware state, a DLQ raising attention, and honest recorder state.
func TestGlance(t *testing.T) {
	h := newConsole(t)

	create(t, h, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}})
	create(t, h, "/_console/s3/create", url.Values{"name": {"files"}})
	// park a message in the DLQ so attention fires
	req(t, h, "POST", "/_console/sqs/work-dlq/send", url.Values{"body": {`{"n":1}`}})

	body := req(t, h, "GET", "/_console/api/glance", nil).Body.Bytes()
	var g struct {
		Services []struct {
			Svc, Label, State string
			Warn              bool
		}
		Attention []struct{ Text, Slug string }
		Recorder  bool
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("glance is not JSON: %v\n%s", err, body)
	}
	if g.Recorder {
		t.Fatal("no recorder was wired — Recorder must be false so the wire reads as capture-off, not empty")
	}
	type svc struct {
		Svc, Label, State string
		Warn              bool
	}
	bySvc := map[string]svc{}
	for _, s := range g.Services {
		bySvc[s.Svc] = svc{s.Svc, s.Label, s.State, s.Warn}
	}
	if bySvc["s3"].Label != "1 bucket" {
		t.Errorf("s3 label = %q, want \"1 bucket\"", bySvc["s3"].Label)
	}
	if bySvc["sqs"].Label != "2 queues" {
		t.Errorf("sqs label = %q, want \"2 queues\"", bySvc["sqs"].Label)
	}
	if !bySvc["sqs"].Warn {
		t.Errorf("a non-empty DLQ must warn: %+v", bySvc["sqs"])
	}
	if len(g.Attention) == 0 || g.Attention[0].Slug != "sqs/work-dlq" {
		t.Errorf("attention should point at the DLQ's console page: %+v", g.Attention)
	}
}
