package console_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/peers"
	"github.com/doze-dev/doze-aws/trace"
)

var (
	anyTagRE = regexp.MustCompile(`<(?:[^<>"']|"[^"]*"|'[^']*')*>`)
	blankRE  = regexp.MustCompile(`\s+`)
)

// textContent is what a reader gets from a piece of markup with the tags gone:
// the page's words in the order they are read, entities decoded, runs of white
// space as one. A screen reader announces the words of sibling elements with
// the white space the markup left between them, so two spans that touch in the
// source are one word to it ("DeniedAccessDenied").
func textContent(markup string) string {
	return strings.TrimSpace(blankRE.ReplaceAllString(html.UnescapeString(anyTagRE.ReplaceAllString(markup, "")), " "))
}

// wireWords serves one answer per action, in the states the wire draws, and
// returns the console that shows them.
func wireWords(t *testing.T) (http.Handler, *console.Recorder) {
	t.Helper()
	svc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		switch strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.") {
		case "Denied":
			w.Header().Set(awshttp.DeniedHeader, "iam")
			w.WriteHeader(403)
			w.Write([]byte(`{"__type":"AccessDenied","message":"no identity-based policy allows it"}`))
		case "DeniedBare":
			w.Header().Set(awshttp.DeniedHeader, "iam")
			w.WriteHeader(403)
			w.Write([]byte(`{"__type":"AccessDenied"}`))
		case "Refused":
			w.WriteHeader(400)
			w.Write([]byte(`{"__type":"InvalidParameterValue","message":"that is not a valid value"}`))
		case "Threw":
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
			w.Write([]byte(`{"errorMessage":"'unit_price'","errorType":"KeyError"}`))
		}
	})
	rec := console.NewRecorder(svc, awsident.Default())
	c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil }), Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"Denied", "DeniedBare", "Refused", "Threw"} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"QueueName":"orders"}`))
		r.Header.Set("X-Amz-Target", "AmazonSQS."+a)
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		rec.ServeHTTP(httptest.NewRecorder(), r)
	}
	return c, rec
}

// A row, the inspector and the dock say a call's answer and the reason for it as
// separate words. The answer is a span and the code or the sentence is another,
// and nothing between them in the source made "denied" and "AccessDenied" one
// word for a screen reader and for any text that reads the page.
func TestAnAnswerAndItsReasonAreSeparateWords(t *testing.T) {
	c, rec := wireWords(t)

	// A cascade row names what emitted it beside the resource.
	rec.EmitCascade(trace.Event{Self: rec.ReserveCascade(), Cause: 1, Service: "sqs", Action: "SendMessage", Resource: "dispatch", Via: "s3:ObjectCreated:Put"})

	feed := req(t, c, "GET", "/_console/traffic/feed", nil).Body.String()
	rowRE := regexp.MustCompile(`(?s)<div id="r-\d+" class="tr-row[^"]*".*?<span class="lane">.*?</span>\s*</div>`)
	rows := map[string]string{}
	for _, r := range rowRE.FindAllString(feed, -1) {
		rows[regexp.MustCompile(`aria-label="Inspect ([A-Za-z]+) `).FindStringSubmatch(r)[1]+"/"+regexp.MustCompile(`data-seq="(\d+)"`).FindStringSubmatch(r)[1]] = textContent(r)
	}
	find := func(action string) (string, string) {
		for k, v := range rows {
			if strings.HasPrefix(k, action+"/") {
				return v, k[strings.Index(k, "/")+1:]
			}
		}
		t.Fatalf("no row for %s in %v", action, rows)
		return "", ""
	}

	t.Run("the wire's rows", func(t *testing.T) {
		for _, w := range []struct{ action, want string }{
			{"Denied", "orders no identity-based policy allows it 403 denied"},
			{"DeniedBare", "orders AccessDenied 403 denied"},
			{"Refused", "orders that is not a valid value 400 refused"},
			{"Threw", "orders KeyError: 'unit_price' it threw"},
		} {
			got, _ := find(w.action)
			if !strings.Contains(got, w.want) {
				t.Errorf("%s: the row reads %q, want it to contain %q", w.action, got, w.want)
			}
		}
		// The resource and what emitted it are two words as well.
		if got, _ := find("SendMessage"); !strings.Contains(got, "dispatch via s3:ObjectCreated:Put") {
			t.Errorf("the cascade row reads %q, want \"dispatch via s3:ObjectCreated:Put\"", got)
		}
	})

	t.Run("the inspector", func(t *testing.T) {
		for _, w := range []struct{ action, head, callout string }{
			{"Denied", "sqs Denied 403 denied", "AccessDenied no identity-based policy allows it"},
			{"Refused", "sqs Refused 400 refused", "InvalidParameterValue that is not a valid value"},
			{"Threw", "sqs Threw it threw", "KeyError 'unit_price'"},
		} {
			_, seq := find(w.action)
			drawer := req(t, c, "GET", "/_console/traffic/entry?seq="+seq, nil).Body.String()
			head := textContent(regexp.MustCompile(`(?s)<div class="td-h".*?</div>`).FindString(drawer))
			if !strings.HasPrefix(head, w.head) {
				t.Errorf("%s: the inspector's head reads %q, want it to start %q", w.action, head, w.head)
			}
			callout := textContent(regexp.MustCompile(`(?s)<div class="td-callout.*?</div>`).FindString(drawer))
			if !strings.HasPrefix(callout, w.callout) {
				t.Errorf("%s: the callout reads %q, want it to start %q", w.action, callout, w.callout)
			}
		}
	})

	t.Run("the dock under a resource", func(t *testing.T) {
		dock := req(t, c, "GET", "/_console/traffic/for?svc=sqs&res=orders&noun=queue", nil).Body.String()
		got := map[string]string{}
		for _, r := range regexp.MustCompile(`(?s)<div class="rw-row.*?</div>`).FindAllString(dock, -1) {
			got[strings.Fields(textContent(r))[1]] = textContent(r)
		}
		for action, want := range map[string]string{
			"Denied":  "no identity-based policy allows it 403 Denied",
			"Refused": "that is not a valid value 400 Refused",
			"Threw":   "'unit_price' 200 It threw",
		} {
			if !strings.Contains(got[action], want) {
				t.Errorf("%s: the dock row reads %q, want it to contain %q", action, got[action], want)
			}
		}
	})
}

// A function that raised under an event source mapping is a served invoke whose
// function threw, as it is for a direct invoke: the row says "it threw" with
// the function's own error, the window counts a function error, and the
// inspector leads with what was raised. It used to read "500 failed" and call it
// a bug in doze-aws, because the cascade event had nowhere to say the function
// had answered.
func TestAnEventSourcesFunctionErrorReadsAsItThrewLikeADirectInvokesDoes(t *testing.T) {
	c, rec := wireWords(t)
	doc := `{"errorMessage":"'unit_price'","errorType":"KeyError"}`
	rec.EmitCascade(trace.Event{Self: rec.ReserveCascade(), Service: "lambda", Action: "Invoke (event source)",
		Resource: "harbour-order-validator", Via: "sqs:orders", FnError: "Unhandled", FnBody: doc,
		Detail: "START RequestId: r1", DetailURL: "/lambda/harbour-order-validator?tab=logs"})

	feed := req(t, c, "GET", "/_console/traffic/feed", nil).Body.String()
	rowRE := regexp.MustCompile(`(?s)<div id="r-(\d+)" class="tr-row[^"]*"(?:[^>]|"[^"]*")*>(?:.*?)<span class="lane">.*?</span>\s*</div>`)
	var row, seq string
	for _, m := range rowRE.FindAllStringSubmatch(feed, -1) {
		if strings.Contains(m[0], "harbour-order-validator") {
			row, seq = m[0], m[1]
		}
	}
	if row == "" {
		t.Fatalf("no row for the invoke:\n%s", feed)
	}
	if !strings.Contains(row, `class="tr-row threw"`) {
		t.Errorf("the row is not a function-error row:\n%s", row)
	}
	if got := textContent(row); !strings.Contains(got, "harbour-order-validator via the queue's event source mapping KeyError: 'unit_price' it threw") {
		t.Errorf("the row reads %q", got)
	}
	// wireWords already holds one direct invoke that threw; this is the second.
	if !strings.Contains(feed, `<span class="m b bad">2</span>`) {
		t.Errorf("the window does not count the function error:\n%s", excerpt(feed, "Function errors"))
	}

	drawer := req(t, c, "GET", "/_console/traffic/entry?seq="+seq, nil).Body.String()
	if head := textContent(regexp.MustCompile(`(?s)<div class="td-h".*?</div>`).FindString(drawer)); !strings.HasPrefix(head, "lambda Invoke it threw") {
		t.Errorf("the inspector's head reads %q", head)
	}
	if callout := textContent(regexp.MustCompile(`(?s)<div class="td-callout.*?</div>`).FindString(drawer)); !strings.HasPrefix(callout, "KeyError 'unit_price'") {
		t.Errorf("the inspector's callout reads %q", callout)
	}
	// The function's lines are still on the row.
	if !strings.Contains(drawer, "START RequestId: r1") {
		t.Errorf("the log tail was lost:\n%s", drawer)
	}
}
