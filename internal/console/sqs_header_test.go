package console_test

// The SQS queue page's shell, as the boards draw it (Queue, Bad-DLQ, Sqs-10,
// Sqs-17): the header with its fact sentence, the neighbours strip, the tabs,
// and the wire pinned under the content. What a browser does with them is held
// in e2e/tests/sqs.spec.ts; what the page says is held here.

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func flatText(html string) string {
	return strings.Join(strings.Fields(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, " ")), " ")
}

func headerOf(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<div class="det-h`)
	j := strings.Index(page, `class="tabbar"`)
	if i < 0 || j < i {
		t.Fatalf("no header on the page")
	}
	return page[i:j]
}

func TestTheQueueHeaderIsTheBoardsSentence(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"new"}, "max_receive": {"3"}, "visibility": {"60"}})
	req(t, h, "POST", "/_console/sqs/orders/send", url.Values{"body": {`{"a":1}`}})
	req(t, h, "POST", "/_console/sqs/orders/send", url.Values{"body": {`{"a":2}`}})

	page := req(t, h, "GET", "/_console/sqs/orders", nil).Body.String()
	head := headerOf(t, page)
	sentence := flatText(head)
	for _, want := range []string{
		"2 waiting · 0 in flight · hidden for 60 s on receive · kept 4 days · dead letters to orders-dlq after 3",
	} {
		if !strings.Contains(sentence, want) {
			t.Errorf("the header's sentence:\n  got  %s\n  want %s", sentence, want)
		}
	}
	if !regexp.MustCompile(`<a class="s-link" href="[^"]*/sqs/orders-dlq">orders-dlq</a>`).MatchString(head) {
		t.Errorf("the dead-letter queue is not a link in the sentence:\n%s", head)
	}
	if strings.Contains(head, "factstrip") || strings.Contains(head, "VISIBILITY TIMEOUT") {
		t.Errorf("the strip is still on the SQS header:\n%s", head)
	}
	// Delete is beside the name, not under the chips and not in a menu.
	title := regexp.MustCompile(`(?s)<div class="det-title".*?</div>\s*</div>`).FindString(head)
	if !strings.Contains(title, "Delete queue") || !strings.Contains(title, "delete-queue") {
		t.Errorf("Delete queue is not beside the name:\n%s", title)
	}
	// No identifiers in the header: the boards draw none.
	if strings.Contains(head, "copychip") || strings.Contains(head, `class="chips"`) {
		t.Errorf("the ARN and URL chips are still under the name:\n%s", head)
	}

	// A queue with no dead-letter queue does not say where it dead-letters to.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"plain"}, "dlq_mode": {"none"}})
	plain := flatText(headerOf(t, req(t, h, "GET", "/_console/sqs/plain", nil).Body.String()))
	if strings.Contains(plain, "dead letters to") {
		t.Errorf("a queue with no redrive policy says it dead-letters:\n%s", plain)
	}
	if !strings.Contains(plain, "0 waiting · 0 in flight · hidden for 30 s on receive · kept 4 days") {
		t.Errorf("the plain queue's sentence:\n%s", plain)
	}
}

// The identifiers have moved, not gone: the Configuration tab carries the ARN
// and the URL as copy controls.
func TestTheQueueIdentifiersAreOnTheConfigurationTab(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"ident"}, "dlq_mode": {"none"}})
	cfg := req(t, h, "GET", "/_console/sqs/ident?tab=config", nil).Body.String()
	for _, label := range []string{"arn", "url"} {
		re := regexp.MustCompile(`<span class="copychip"><span class="cc-label">` + label + `</span><span class="cc-val" title="[^"]*ident"`)
		if !re.MatchString(cfg) {
			t.Errorf("the Configuration tab has no %s copy control", label)
		}
	}
}

// The tabs are text, as the boards draw them: the page used to put an icon on
// each, and no board does. The first carries the live count.
func TestTheQueueTabsAreTextWithACountOnTheFirst(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"tabs"}, "dlq_mode": {"none"}})
	page := req(t, h, "GET", "/_console/sqs/tabs", nil).Body.String()
	i := strings.Index(page, `class="tabbar">`) + len(`class="tabbar">`)
	j := strings.Index(page[i:], "</div>")
	bar := page[i : i+j]
	if strings.Contains(bar, "<svg") || strings.Contains(bar, `class="ic"`) {
		t.Errorf("the tabs carry icons:\n%s", bar)
	}
	if flatText(bar) != "Messages 0 Consume Configuration Tags" {
		t.Errorf("tabs = %q", flatText(bar))
	}
}

// The neighbours strip reads as the board's row and each neighbour is a link
// with its service's mark.
func TestTheQueueNeighboursReadAsTheBoardsRow(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"sink"}, "dlq_mode": {"none"}})
	create(t, h, "/_console/sns/create", url.Values{"name": {"events"}})
	if sub := req(t, h, "POST", "/_console/sns/events/subscribe", url.Values{
		"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:sink"},
	}); sub.Code != 200 {
		t.Fatalf("subscribe: %d", sub.Code)
	}
	page := req(t, h, "GET", "/_console/sqs/sink", nil).Body.String()
	i := strings.Index(page, `class="conn"`)
	if i < 0 {
		t.Fatalf("no neighbours strip")
	}
	strip := page[i:]
	strip = strip[:strings.Index(strip, "</div>")]
	if got := flatText(strip); !strings.Contains(got, "fed by events → this queue → no Lambda trigger reads this queue — wire one to close the loop") {
		t.Errorf("the strip reads %q", got)
	}
	if !regexp.MustCompile(`<a class="nb-link"[^>]*href="[^"]*/sns/events"[^>]*><img class="aws-ic sm"[^>]*/aws/sns.svg`).MatchString(strip) {
		t.Errorf("the topic is not a link with its mark:\n%s", strip)
	}
}

// The wire is pinned: its slot is a child of the pane, after the scrolling
// body, not inside it, so it cannot scroll away with the page.
func TestTheQueueWireIsPinnedOutsideTheScrollingBody(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"pinned"}, "dlq_mode": {"none"}})
	for _, tab := range []string{"", "?tab=consume", "?tab=config", "?tab=tags"} {
		page := req(t, h, "GET", "/_console/sqs/pinned"+tab, nil).Body.String()
		if !strings.Contains(page, `<section class="detail wired">`) {
			t.Errorf("%q: the pane does not say it carries a pinned wire", tab)
		}
		// The body closes, then the slot, then the pane closes: the slot is the
		// pane's last child, not the body's.
		if !regexp.MustCompile(`</div>\s*<div id="res-wire"[^>]*></div>\s*</section>`).MatchString(page) {
			t.Errorf("%q: the wire slot is not the pane's last child, after the scrolling body", tab)
		}
	}
}
