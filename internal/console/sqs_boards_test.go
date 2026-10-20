package console_test

// What the SQS pages owe the design's boards, where a Go test can hold it: the
// states a handler decides (the home that opens nothing, a refused create that
// leaves nothing behind) and the sentences the service's own behaviour makes
// true or false.

import (
	"net/url"
	"strings"
	"testing"
)

// The service's home lists the queues and opens none (board Sqs-02). It used
// to render the first queue's page, so the "nothing selected" state the design
// draws could not be reached.
func TestSQSHomeOpensNoQueue(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"alpha"}, "dlq_mode": {"none"}})
	create(t, h, "/_console/sqs/create", url.Values{"name": {"beta"}, "dlq_mode": {"none"}})
	home := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	for _, want := range []string{
		"Select a queue to read its messages without consuming them, send one, or set up redrive.",
		"The number is messages waiting. In-flight ones are not counted.",
		`data-name="alpha"`, `data-name="beta"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("the SQS home does not show %q", want)
		}
	}
	if strings.Contains(home, `class="li on"`) || strings.Contains(home, `id="message-panel-wrap"`) {
		t.Errorf("the SQS home opened a queue; it lists them and opens none")
	}
}

// A create the service refuses leaves nothing behind. "Create one alongside"
// makes the dead-letter queue first (the main queue has to name it), and a
// refusal of the main queue used to leave that queue standing.
func TestARefusedCreateLeavesNoDeadLetterQueueBehind(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})

	// Same name, another visibility timeout: QueueNameExists.
	refused := req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "visibility": {"45"}, "dlq_mode": {"new"}})
	if refused.Code != 400 || !strings.Contains(refused.Body.String(), "QueueNameExists") {
		t.Fatalf("the second create was not refused as expected: %d\n%s", refused.Code, refused.Body)
	}
	if list := req(t, h, "GET", "/_console/sqs", nil).Body.String(); strings.Contains(list, `data-name="orders-dlq"`) {
		t.Errorf("the refused create left orders-dlq behind")
	}

	// A dead-letter queue that was already there is somebody's, and stays.
	create(t, h, "/_console/sqs/create", url.Values{"name": {"orders-dlq"}, "dlq_mode": {"none"}})
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "visibility": {"45"}, "dlq_mode": {"new"}})
	if list := req(t, h, "GET", "/_console/sqs", nil).Body.String(); !strings.Contains(list, `data-name="orders-dlq"`) {
		t.Errorf("a refused create deleted a dead-letter queue it did not make")
	}
}

// A message is delivered maxReceiveCount times and the NEXT receive moves it
// (sqs/receive.go). No SQS page may say the Nth receive is the one that does.
func TestNoSQSPageSaysTheNthReceiveMovesAMessage(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"work"}, "dlq_mode": {"new"}, "max_receive": {"3"}})
	for _, page := range []string{"/_console/sqs/create", "/_console/sqs/work", "/_console/sqs/work?tab=consume", "/_console/sqs/work?tab=config", "/_console/sqs/work-dlq"} {
		body := req(t, h, "GET", page, nil).Body.String()
		for _, wrong := range []string{"set aside on its", "on its 3rd receive", "after 3 receives</", "instead of coming back"} {
			if strings.Contains(body, wrong) {
				t.Errorf("%s says %q: the receive AFTER the last delivery is the one that moves a message", page, wrong)
			}
		}
	}
	cfg := req(t, h, "GET", "/_console/sqs/work?tab=config", nil).Body.String()
	if !strings.Contains(cfg, "Gets a message that was received 3 times, on its next receive.") {
		t.Errorf("the Configuration tab does not say when a message is dead-lettered")
	}
}

// The numbers a queue page shows are in the units the design writes them in.
func TestQueueSettingsAreShownInTheBoardsUnits(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/sqs/create", url.Values{"name": {"units"}, "dlq_mode": {"none"}, "visibility": {"60"}})
	cfg := req(t, h, "GET", "/_console/sqs/units?tab=config", nil).Body.String()
	for _, want := range []string{">60 s<", ">4 days<", ">0 s<", "1 048 576", "default 4 days", "default 30 ", "default 1 048 576"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the Configuration tab does not show %q", want)
		}
	}
}

// The Create board (decision D4) draws the "Made here, kept here" card on every
// create page, and on SQS's it says of the queue what the board says. Sqs-03 to
// Sqs-05 omit the card; where a service board and the frame board differ the
// frame decision stands (plan/services/sqs.md, SQS-56).
func TestTheSQSCreatePageNamesTheQueueInTheMadeHereKeptHereCard(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sqs/create", nil).Body.String()
	for _, want := range []string{
		"Made here, kept here",
		"The queue exists as long as the data directory does. To have it every time doze-aws starts, put it in a template.yaml next to where you start it.",
		"The call this makes", "Copy the command",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the SQS create page lacks %q", want)
		}
	}
}
