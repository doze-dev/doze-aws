package console_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The map (plan/00-foundation.md D, boards Map and Map-Large): the wiring
// graph, drawn while it fits and as one card per flow once it does not.

// getMap is GET /_console/map<q> on h: the status and the body.
func getMap(t *testing.T, h http.Handler, q string) (int, string) {
	t.Helper()
	rec := req(t, h, "GET", "/_console/map"+q, nil)
	return rec.Code, rec.Body.String()
}

// wireSome builds two flows and one resource nothing is wired to:
//
//	orders --redrive--> orders-dlq
//	alerts (topic) --sub--> alerts-mail
//	assets (bucket)
func wireSome(t *testing.T, h http.Handler) {
	t.Helper()
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders-dlq"}})
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq": {"orders-dlq"}, "max_receive": {"5"}})
	create(t, h, "/_console/sqs/create", url.Values{"name": {"alerts-mail"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"alerts"}})
	if r := req(t, h, "POST", "/_console/sns/alerts/subscribe", url.Values{
		"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:alerts-mail"},
	}); r.Code != 200 {
		t.Fatalf("subscribe: %d\n%s", r.Code, r.Body)
	}
	create(t, h, "/_console/s3/create", url.Values{"name": {"assets"}})
}

// text is the visible words of a page, tags gone and whitespace folded.
func text(body string) string {
	s := regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`).ReplaceAllString(body, " ")
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func TestMapOfAnEmptyStackSaysNothingIsWired(t *testing.T) {
	h := newConsole(t)
	code, body := getMap(t, h, "")
	if code != 200 {
		t.Fatalf("GET /map: %d\n%s", code, body)
	}
	got := text(body)
	for _, want := range []string{"The map", "Nothing is wired yet.", "0 resources", "0 flows"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty map lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(body, `class="map-box"`) || strings.Contains(body, `class="map-card"`) {
		t.Errorf("an empty stack drew a node:\n%s", body)
	}
}

func TestMapDrawsTheWiringWhileItFits(t *testing.T) {
	h := newConsole(t)
	wireSome(t, h)
	code, body := getMap(t, h, "")
	if code != 200 {
		t.Fatalf("GET /map: %d\n%s", code, body)
	}
	got := text(body)
	for _, want := range []string{"4 resources", "2 flows", "1 unwired", "Wired to nothing", "assets"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
	// Each node is a link to its page, with the service's AWS icon.
	for _, n := range []struct{ svc, name string }{{"sqs", "orders"}, {"sqs", "orders-dlq"}, {"sns", "alerts"}, {"sqs", "alerts-mail"}} {
		re := regexp.MustCompile(`<a class="map-box"[^>]*href="/_console/` + n.svc + `/` + n.name + `"[^>]*><img class="aws-ic" src="/_console/static/aws/` + n.svc + `\.svg"`)
		if !re.MatchString(body) {
			t.Errorf("no node link with an icon for %s/%s:\n%s", n.svc, n.name, body)
		}
	}
	if !regexp.MustCompile(`<a class="pill mono map-chip"[^>]*href="/_console/s3/assets"`).MatchString(body) {
		t.Errorf("the unwired bucket is not a link:\n%s", body)
	}
	// The wires are labelled with what the wiring is.
	for _, kind := range []string{"redrive", "sub"} {
		if !regexp.MustCompile(`<span class="map-label[^"]*"[^>]*>` + kind + `</span>`).MatchString(body) {
			t.Errorf("no %q label on a wire:\n%s", kind, body)
		}
	}
	// The legend names all seven.
	for _, kind := range []string{"notify", "sub", "target", "esm", "redrive", "dlq", "dest"} {
		if !strings.Contains(got, kind) {
			t.Errorf("legend lacks %q", kind)
		}
	}
	if !strings.Contains(got, "Flow 1 · from") || !strings.Contains(got, "Flow 2 · from") {
		t.Errorf("flows are not named:\n%s", got)
	}
	if strings.Contains(got, "stops drawing wires") {
		t.Errorf("four resources are not 'at scale':\n%s", got)
	}
}

// scale wires n topics to a queue each: n flows of two resources.
func scale(t *testing.T, h http.Handler, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		q, tp := fmt.Sprintf("q-%02d", i), fmt.Sprintf("t-%02d", i)
		create(t, h, "/_console/sqs/create", url.Values{"name": {q}})
		create(t, h, "/_console/sns/create", url.Values{"name": {tp}})
		if r := req(t, h, "POST", "/_console/sns/"+tp+"/subscribe", url.Values{
			"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:" + q},
		}); r.Code != 200 {
			t.Fatalf("subscribe %s: %d", tp, r.Code)
		}
	}
}

func TestMapPastAHandfulIsOneCardPerFlow(t *testing.T) {
	h := newConsole(t)
	scale(t, h, 10) // 20 wired resources in ten flows
	create(t, h, "/_console/s3/create", url.Values{"name": {"assets"}})
	code, body := getMap(t, h, "")
	if code != 200 {
		t.Fatalf("GET /map: %d\n%s", code, body)
	}
	got := text(body)
	for _, want := range []string{"20 resources", "10 flows", "1 unwired",
		"Past 12 resources the page stops drawing wires. A flow is one card; open it to see its wires.",
		"3 more flows of 2 resources each", "Show them", "Wired to nothing · 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("large map lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(body, `class="map-card`); n != 7 {
		t.Errorf("%d cards before the fold, want 7", n)
	}
	if strings.Contains(body, `class="map-box"`) {
		t.Errorf("a large map drew wires:\n%s", body)
	}
	// A card names its flow, counts it, and lists the first resources by kind.
	if !strings.Contains(got, "from t-00 2 resources SNS t-00 → SQS q-00") {
		t.Errorf("the card is not the board's:\n%s", got)
	}
	// Show them: every flow, no fold.
	_, all := getMap(t, h, "?all=1")
	if n := strings.Count(all, `class="map-card`); n != 10 {
		t.Errorf("?all=1 shows %d cards, want 10", n)
	}
	if strings.Contains(text(all), "Show them") {
		t.Errorf("?all=1 still offers to show them")
	}
	// Find narrows to the flows holding a match, folded or not.
	_, found := getMap(t, h, "?q=q-09")
	if n := strings.Count(found, `class="map-card`); n != 1 || !strings.Contains(text(found), "from t-09") {
		t.Errorf("?q=q-09 shows %d cards:\n%s", n, text(found))
	}
	// Open the flow: its wires, drawn.
	card := regexp.MustCompile(`<a class="map-card[^"]*" href="([^"]*)"[^>]*>\s*<span[^>]*>from t-03`).FindStringSubmatch(body)
	if card == nil {
		t.Fatalf("no card links to its flow:\n%s", body)
	}
	_, one := getMap(t, h, strings.TrimPrefix(strings.ReplaceAll(card[1], "&amp;", "&"), "/_console/map"))
	for _, want := range []string{`href="/_console/sns/t-03"`, `href="/_console/sqs/q-03"`, `>sub</span>`} {
		if !strings.Contains(one, want) {
			t.Errorf("the flow page lacks %s:\n%s", want, one)
		}
	}
	if strings.Contains(one, `href="/_console/sns/t-04"`) {
		t.Errorf("the flow page drew another flow:\n%s", one)
	}
}

func TestMapHotOnlyAndAnUnknownFlow(t *testing.T) {
	h := newConsole(t)
	scale(t, h, 7)
	// No recorder in this console: nothing can be hot, and saying so beats
	// showing a list of everything under "Hot only".
	_, hot := getMap(t, h, "?hot=1")
	if !strings.Contains(text(hot), "No flow had a call in the last minute.") || strings.Contains(hot, `class="map-card`) {
		t.Errorf("hot only with nothing hot:\n%s", text(hot))
	}
	code, gone := getMap(t, h, "?flow=sqs:nobody")
	if code != 200 || !strings.Contains(text(gone), "That flow is not on the map any more.") {
		t.Errorf("an unknown flow: %d\n%s", code, text(gone))
	}
}

func TestMapIsReachedFromTheRailAndThePalette(t *testing.T) {
	h := newConsole(t)
	_, body := getMap(t, h, "")
	if !regexp.MustCompile(`<a href="/_console/map" class="ri on[^"]*" aria-label="The map"`).MatchString(body) {
		t.Errorf("the rail has no 'The map' entry, or it is not the current one:\n%s", body)
	}
	pal := req(t, h, "GET", "/_console/api/palette", nil).Body.String()
	if !strings.Contains(pal, `"n":"The map","u":"/_console/map"`) {
		t.Errorf("the palette cannot go to the map:\n%s", pal)
	}
}
