package console_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/peers"
)

// One colour per state, everywhere a call is shown. The inspector's status chip
// took the red of a failure for any status of 400 or more, so a refusal (purple)
// and a denial (amber) had a red chip beside their own correctly coloured
// panel. The chip carries the call's state, as the row and the panel do, and
// the row says the state in a word: colour alone is not a reading.
func TestTheStatusChipIsTheColourOfItsState(t *testing.T) {
	// A service that answers each action the way one state is answered. The
	// state is read from the stamp on the answer, never from its status.
	answers := map[string]struct {
		status int
		header string
		value  string
		code   string
	}{
		"Served":      {200, "", "", ""},
		"Refused":     {400, "", "", "ValidationException"},
		"RefusedHere": {400, awshttp.LocalLimitHeader, "1", "LimitExceededException"},
		"Denied":      {403, awshttp.DeniedHeader, "iam", "AccessDeniedException"},
		"NotBuilt":    {501, awshttp.NotBuiltHeader, "deferred", "NotImplemented"},
		"Failed":      {500, "", "", "InternalFailure"},
	}
	svc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := answers[strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.")]
		if a.header != "" {
			w.Header().Set(a.header, a.value)
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(a.status)
		if a.code != "" {
			w.Write([]byte(`{"__type":"` + a.code + `","message":"said the service"}`))
		}
	})
	rec := console.NewRecorder(svc, awsident.Default())
	c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil }), Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	// In this order, so each call's seq is its place in the list below.
	want := []struct{ action, state, word string }{
		{"Served", "served", ""},
		{"Refused", "refused", "Refused"},
		{"RefusedHere", "refusedhere", "Refused here"},
		{"Denied", "denied", "Denied"},
		{"NotBuilt", "notbuilt", "Not built"},
		{"Failed", "error", "Failed"},
	}
	for _, w := range want {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"QueueName":"orders"}`))
		r.Header.Set("X-Amz-Target", "AmazonSQS."+w.action)
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		rec.ServeHTTP(httptest.NewRecorder(), r)
	}

	feed := req(t, c, "GET", "/_console/traffic/feed", nil).Body.String()
	rowRE := regexp.MustCompile(`(?s)<div class="tr-row[^"]*".*?</button>\s*</div>`)
	rows := rowRE.FindAllString(feed, -1)
	if len(rows) != len(want) {
		t.Fatalf("the feed drew %d rows, want %d:\n%s", len(rows), len(want), feed)
	}
	chipRE := regexp.MustCompile(`<span class="st ([a-z]+)"[^>]*>(\d+)</span>`)
	wordRE := regexp.MustCompile(`<span class="tr-state">([^<]*)</span>`)
	seqRE := regexp.MustCompile(`traffic/entry\?seq=(\d+)`)

	for _, w := range want {
		t.Run(w.action, func(t *testing.T) {
			var row string
			for _, r := range rows {
				if strings.Contains(r, ">"+w.action+"<") {
					row = r
				}
			}
			if row == "" {
				t.Fatalf("no row for %s", w.action)
			}
			status := answers[w.action].status

			// The row: its chip is the state's, and it says the state.
			chip := chipRE.FindStringSubmatch(row)
			if chip == nil || chip[1] != w.state {
				t.Errorf("row chip = %v, want class %q", chip, w.state)
			}
			word := wordRE.FindStringSubmatch(row)
			if w.word == "" {
				if word != nil {
					t.Errorf("a served call's row says %q; it has nothing to say", word[1])
				}
			} else if word == nil || word[1] != w.word {
				t.Errorf("row state word = %v, want %q", word, w.word)
			}

			// The inspector: one chip, the state's class and no other.
			seq := seqRE.FindStringSubmatch(row)
			if seq == nil {
				t.Fatalf("the row opens no inspector:\n%s", row)
			}
			drawer := req(t, c, "GET", "/_console/traffic/entry?seq="+seq[1], nil).Body.String()
			meta := regexp.MustCompile(`(?s)<div class="td-meta">.*?</div>`).FindString(drawer)
			dchip := chipRE.FindStringSubmatch(meta)
			if dchip == nil {
				t.Fatalf("the inspector has no status chip:\n%s", meta)
			}
			if dchip[1] != w.state {
				t.Errorf("inspector chip class = %q for a %d that is %s, want %q", dchip[1], status, w.action, w.state)
			}
			if strings.Contains(meta, `class="err"`) || strings.Contains(meta, `class="ok"`) {
				t.Errorf("the inspector still colours the chip by status alone:\n%s", meta)
			}
			if w.word != "" {
				if !strings.Contains(meta, w.word) {
					t.Errorf("the inspector's chip does not say %q:\n%s", w.word, meta)
				}
				if !strings.Contains(drawer, `class="td-sec td-`+w.state+`"`) {
					t.Errorf("the panel under the chip is not the same state (%s)", w.state)
				}
			}
		})
	}
}
