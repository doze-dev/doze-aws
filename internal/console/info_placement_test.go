package console_test

// Where the fidelity button is (D12, board Fidelity): beside the name in a
// resource's header, and in the list pane's header when nothing is open. One
// of them, never both, so a person finds it in one place and a dialog opens
// once.

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var infoBtn = regexp.MustCompile(`class="info-btn"`)

func TestTheInfoButtonIsInTheListHeaderWhenNothingIsOpen(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}})

	home := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	if n := len(infoBtn.FindAllString(home, -1)); n != 1 {
		t.Fatalf("the SQS home carries %d info buttons, want 1", n)
	}
	lp := home[strings.Index(home, `class="lp-h"`):]
	lp = lp[:strings.Index(lp, `class="lp-menu"`)]
	if !infoBtn.MatchString(lp) {
		t.Errorf("with nothing open the button is not in the list pane's header:\n%s", lp)
	}
}

func TestTheInfoButtonIsBesideTheNameOnceAResourceIsOpen(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}})

	page := req(t, h, "GET", "/_console/sqs/orders", nil).Body.String()
	if n := len(infoBtn.FindAllString(page, -1)); n != 1 {
		t.Fatalf("a queue page carries %d info buttons, want 1", n)
	}
	lp := page[strings.Index(page, `class="lp-h"`):]
	lp = lp[:strings.Index(lp, `class="lp-menu"`)]
	if infoBtn.MatchString(lp) {
		t.Errorf("the button is in the list pane's header as well as the resource's:\n%s", lp)
	}
	title := regexp.MustCompile(`(?s)<div class="det-title".*?</div>\s*</div>`).FindString(page)
	i, j := strings.Index(title, "orders"), strings.Index(title, "info-btn")
	if i < 0 || j < i {
		t.Fatalf("the button is not after the name in the title row (name at %d, button at %d):\n%s", i, j, title)
	}
	if !strings.Contains(title, `aria-label="What of SQS is real here"`) {
		t.Errorf("the button's name is not \"What of SQS is real here\":\n%s", title)
	}
	// The dialog answers it, and sits outside the heading so the heading's name
	// is the queue's and not the dialog's.
	if strings.Contains(title, "what is real here") {
		t.Errorf("the dialog is inside the heading:\n%s", title)
	}
	if !strings.Contains(page, `@open-fidelity.window="open($event)"`) {
		t.Errorf("no dialog answers the header's button")
	}
}
