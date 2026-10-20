package console_test

// A2: the line under a refusal says what happened, and what happened is read
// from the stamps the server put on its answer (X-Doze-Denied,
// X-Doze-Not-Built, X-Doze-Local-Limit, X-Amz-Function-Error), never from a
// status or the spelling of a code. Each kind is driven through the real
// stamping function (awshttp.StampHeaders / awshttp.NotBuilt) into a console
// action and into the wire's inspector, and the text on the page is compared
// whole, so a sentence that drifts, or one kind reading as another, fails.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/console"
	"github.com/doze-dev/doze-aws/peers"
)

// answer is what the fake service says to the next call.
type answer struct {
	err    *awshttp.APIError // stamped through the product's own function
	header [2]string         // a header the product sets outside APIError (Lambda's)
	status int
	body   string
}

func stampedService(a *answer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		if a.err != nil {
			awshttp.StampHeaders(w, a.err)
			w.WriteHeader(a.err.Status)
			w.Write([]byte(`{"__type":"` + a.err.Code + `","message":"` + a.err.Message + `"}`))
			return
		}
		if a.header[0] != "" {
			w.Header().Set(a.header[0], a.header[1])
		}
		w.WriteHeader(a.status)
		w.Write([]byte(a.body))
	})
}

const (
	whyRefused     = "doze-aws refused the request: it breaks a rule of the service. The input is what to change."
	whyRefusedHere = "AWS would accept this. doze-aws refuses it because of a limit of its own."
	whyDenied      = "A policy decided this, not the input: the request was well formed. The fix is the policy. Access log →"
	whyFailed      = "This is doze-aws failing rather than refusing. A 5xx here is a bug in the emulator, not in the call, and is worth reporting."
)

func TestTheWhyLineIsThatKindsOwn(t *testing.T) {
	cases := []struct {
		kind    string
		a       answer
		inline  string // the console action's inline failure, whole
		drawer  string // the wire inspector's panel, whole
		drawerH string // the inspector's chip words
	}{
		{
			kind:    "refused",
			a:       answer{err: &awshttp.APIError{Code: "InvalidParameterValue", Status: 400, Message: "queue name is not valid"}},
			inline:  "Refused InvalidParameterValue queue name is not valid " + whyRefused,
			drawer:  "InvalidParameterValue queue name is not valid",
			drawerH: "Refused",
		},
		{
			// A 403 with no stamp is not a denial: S3's BlockPublicPolicy is one.
			kind:    "refused, a 403 no policy decided",
			a:       answer{err: &awshttp.APIError{Code: "AccessDenied", Status: 403, Message: "public policies are blocked"}},
			inline:  "Refused AccessDenied public policies are blocked " + whyRefused,
			drawer:  "AccessDenied public policies are blocked",
			drawerH: "Refused",
		},
		{
			kind:    "denied",
			a:       answer{err: &awshttp.APIError{Code: "AccessDeniedException", Status: 403, Message: "no identity-based policy allows sqs:CreateQueue", Denied: "iam"}},
			inline:  "Denied AccessDeniedException no identity-based policy allows sqs:CreateQueue " + whyDenied,
			drawer:  "AccessDeniedException no identity-based policy allows sqs:CreateQueue " + whyDenied,
			drawerH: "Denied",
		},
		{
			kind:    "a limit of doze-aws's own",
			a:       answer{err: &awshttp.APIError{Code: "LimitExceededException", Status: 400, Message: "too many queues for a local stack", Local: true}},
			inline:  "Refused here LimitExceededException too many queues for a local stack " + whyRefusedHere,
			drawer:  "LimitExceededException too many queues for a local stack " + whyRefusedHere,
			drawerH: "Refused here",
		},
		{
			kind: "not built, declined",
			a:    answer{err: awshttp.NotBuilt(awshttp.Declined, "ListQueueTags", "tags are not kept")},
			inline: "Not built NotImplemented ListQueueTags is not built in doze-aws: tags are not kept " +
				"doze-aws declines this operation: building it would mean pretending to something that is not here. Nothing is wrong with the request.",
			drawer: "NotImplemented ListQueueTags is not built in doze-aws: tags are not kept " +
				"doze-aws declines this operation: building it would mean pretending to something that is not here. Nothing is wrong with the request.",
			drawerH: "Not built",
		},
		{
			kind: "not built, deferred",
			a:    answer{err: awshttp.NotBuilt(awshttp.Deferred, "ListQueueTags", "not yet")},
			inline: "Not built NotImplemented ListQueueTags is not built in doze-aws: not yet " +
				"doze-aws has not built this operation yet. Nothing is wrong with the request.",
			drawer: "NotImplemented ListQueueTags is not built in doze-aws: not yet " +
				"doze-aws has not built this operation yet. Nothing is wrong with the request.",
			drawerH: "Not built",
		},
		{
			kind:    "an older stub table's answer",
			a:       answer{err: (&awshttp.APIError{Code: "ValidationError", Status: 400, Message: "ListStackSets is not implemented"}).Stub()},
			inline:  "Not built ValidationError ListStackSets is not implemented doze-aws does not build this operation, and says so rather than pretending. Nothing is wrong with the request.",
			drawer:  "ValidationError ListStackSets is not implemented doze-aws does not build this operation, and says so rather than pretending. Nothing is wrong with the request.",
			drawerH: "Not built",
		},
		{
			kind:    "doze-aws failing",
			a:       answer{err: &awshttp.APIError{Code: "InternalFailure", Status: 500, Message: "the store broke"}},
			inline:  "Failed InternalFailure the store broke " + whyFailed,
			drawer:  "InternalFailure the store broke " + whyFailed,
			drawerH: "Failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			a := tc.a
			rec := console.NewRecorder(stampedService(&a), awsident.Default())
			c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil }), Recorder: rec})
			if err != nil {
				t.Fatal(err)
			}
			// The wire: a client's call, recorded and then opened.
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"QueueName":"orders"}`))
			r.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
			r.Header.Set("Content-Type", "application/x-amz-json-1.0")
			rec.ServeHTTP(httptest.NewRecorder(), r)
			entry := req(t, c, "GET", "/_console/traffic/entry?seq=1", nil).Body.String()
			panel := section(t, entry, `<div class="td-callout td-`)
			if got := flatText(panel); got != tc.drawer {
				t.Errorf("the inspector says\n  %q\nwant\n  %q", got, tc.drawer)
			}

			// The page: the console's own action, refused by the same service.
			// (The console reaches the service by the same stamped answer.)
			c2, err := console.New(console.Options{Peers: peers.InProcess(func(s string) http.Handler {
				if s == "sqs" {
					return stampedService(&a)
				}
				return nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			frag := req(t, c2, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}}).Body.String()
			if got := flatText(section(t, frag, `<div class="err `)); got != tc.inline {
				t.Errorf("the page says\n  %q\nwant\n  %q", got, tc.inline)
			}
		})
	}
}

// A function that raised is a 200 with a header: the call was served. The
// inspector names the function, because "your function" alone, on a page of
// twenty, does not say which.
func TestAFunctionErrorNamesTheFunction(t *testing.T) {
	a := answer{header: [2]string{"X-Amz-Function-Error", "Unhandled"}, status: 200, body: `{"errorMessage":"boom","errorType":"Error"}`}
	rec := console.NewRecorder(stampedService(&a), awsident.Default())
	c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil }), Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/2015-03-31/functions/orders-worker/invocations", strings.NewReader(`{}`))
	rec.ServeHTTP(httptest.NewRecorder(), r)
	entry := req(t, c, "GET", "/_console/traffic/entry?seq=1", nil).Body.String()
	got := flatText(section(t, entry, `<div class="td-callout td-`))
	want := "Error boom The call was served and the function orders-worker raised. Lambda answers 200 and flags the error in X-Amz-Function-Error; the body says what it threw. Nothing here is a bug in doze-aws."
	if got != want {
		t.Errorf("the inspector says\n  %q\nwant\n  %q", got, want)
	}
}

// The console reaches a service through the same transport a client does, and
// answers 503 itself for one the stack was started without. That is a choice
// about this stack: it says so, and does not claim AWS would accept the call
// "because of a limit of doze-aws".
func TestAServiceThatIsNotRunningSaysSo(t *testing.T) {
	c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	frag := req(t, c, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}}).Body.String()
	got := flatText(section(t, frag, `<div class="err `))
	want := "Refused here ServiceUnavailable the sqs service is not running This stack was started without this service, so nothing answers for it. Start it with the service enabled; the request is not what to change."
	if got != want {
		t.Errorf("the page says\n  %q\nwant\n  %q", got, want)
	}
}

// A failure the console itself found (a form validated before any call) has no
// code and no stamp: it is a refusal of the input and has nothing to explain
// beyond its own sentence.
func TestAFailureWithNoCodeHasNoWhyLine(t *testing.T) {
	c, err := console.New(console.Options{Peers: peers.InProcess(func(string) http.Handler { return nil })})
	if err != nil {
		t.Fatal(err)
	}
	frag := req(t, c, "POST", "/_console/cw/create-alarm", url.Values{"name": {""}}).Body.String()
	got := flatText(section(t, frag, `<div class="err `))
	if want := "Refused an alarm needs a name"; got != want {
		t.Errorf("the page says %q, want %q", got, want)
	}
}

// section returns the element that starts at marker, to the end of its div.
func section(t *testing.T, html, marker string) string {
	t.Helper()
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("no %q in\n%s", marker, html)
	}
	depth, j := 0, i
	for j < len(html) {
		switch {
		case strings.HasPrefix(html[j:], "<div"):
			depth++
			j += 4
		case strings.HasPrefix(html[j:], "</div>"):
			depth--
			j += 6
			if depth == 0 {
				return html[i:j]
			}
		default:
			j++
		}
	}
	t.Fatalf("unbalanced section at %q", marker)
	return ""
}
