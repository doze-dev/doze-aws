package console_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The wiring strip states only what the graph knows. The graph holds
// configuration (subscriptions, targets, triggers, notifications) and cannot see
// an SDK client, so a resource nothing in the stack is configured to feed is
// not a resource nothing feeds — and a stream no trigger reads is not a stream
// that drains nowhere, nor is a queue with no wiring one that nothing sends to.
func TestTheStripNeverClaimsNothingFeedsAResource(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	req(t, h, "POST", "/_console/sns/create", url.Values{"name": {"events"}})
	req(t, h, "POST", "/_console/s3/create", url.Values{"name": {"uploads"}})
	req(t, h, "POST", "/_console/kinesis/create", url.Values{"name": {"clicks"}, "shards": {"1"}})
	req(t, h, "POST", "/_console/eb/default/create-rule", url.Values{"name": {"on-order"}, "pattern": {`{"source":["shop"]}`}})

	for _, page := range []string{
		"/_console/sqs/orders", "/_console/sns/events", "/_console/s3/uploads",
		"/_console/kinesis/clicks", "/_console/eb/default/rule/on-order",
	} {
		t.Run(page, func(t *testing.T) {
			body := req(t, h, "GET", page, nil).Body.String()
			if !strings.Contains(body, `class="conn"`) {
				t.Fatalf("no wiring strip on the page:\n%.400s", body)
			}
			if m := unseenClientClaim.FindString(body); m != "" {
				t.Errorf("the strip claims %q; the graph holds configuration and cannot know that", m)
			}
			if !strings.Contains(body, `class="conn-hint"`) {
				t.Errorf("a resource with nothing downstream says nothing about it")
			}
		})
	}

	// The deck counts the resources no configuration connects. That is what it
	// can say of them, in the count and in the hover that names them.
	t.Run("deck", func(t *testing.T) {
		// The deck stays quiet about wiring until something is wired, so wire
		// one pair and leave the rest.
		req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"sink"}, "dlq_mode": {"none"}})
		req(t, h, "POST", "/_console/sns/create", url.Values{"name": {"wired"}})
		if sub := req(t, h, "POST", "/_console/sns/wired/subscribe", url.Values{
			"protocol": {"sqs"}, "endpoint": {"arn:aws:sqs:us-east-1:000000000000:sink"},
		}); sub.Code != 200 {
			t.Fatalf("subscribe: %d\n%s", sub.Code, sub.Body)
		}
		body := req(t, h, "GET", "/_console/deck", nil).Body.String()
		if !strings.Contains(body, `class="dk-unwired"`) {
			t.Fatalf("the deck shows no unwired count for a stack of unwired resources:\n%.600s", body)
		}
		if m := unseenClientClaim.FindString(body); m != "" {
			t.Errorf("the deck claims %q; the graph holds configuration and cannot know that", m)
		}
	})
}

// unseenClientClaim is a sentence about what does or does not use a resource,
// which only traffic could establish: an SDK client appears in no
// configuration. "No Lambda trigger reads this queue" names the configuration
// that is missing and passes; "nothing reads this queue" does not.
var unseenClientClaim = regexp.MustCompile(`(?i)\bnothing (?:feeds|sends|reads|consumes|drains|calls|writes to|publishes to)\b|\bdrains nowhere\b|\bsends? to nothing\b|\bnot connected to anything\b|\bnobody (?:reads|sends|calls|uses)\b`)

var templateComment = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// And no template says it anywhere else. Comments are the place to explain
// why, so they are not read.
func TestNoTemplateClaimsWhatOnlyTrafficCouldShow(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the line numbers: a comment becomes its own newlines.
		src := templateComment.ReplaceAllStringFunc(string(raw), func(c string) string {
			return strings.Repeat("\n", strings.Count(c, "\n"))
		})
		for i, line := range strings.Split(src, "\n") {
			if m := unseenClientClaim.FindString(line); m != "" {
				t.Errorf("%s:%d says %q; the console knows configuration, not who calls", f, i+1, m)
			}
		}
	}
}
