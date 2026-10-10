package console_test

import (
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"github.com/doze-dev/doze-aws/internal/settings"
	_ "github.com/doze-dev/doze-aws/sqs" // registers SQS's settings
)

// A form field that takes a number is held to the record of the setting it
// sets: the min and the max on the <input> are the service's own range, read
// from where the service enforces it. They were typed twice, and an <input>
// whose max is not the service's max either refuses a value AWS accepts or
// offers one the service then refuses.
//
// This is the worked example for SQS. A service's stage adds its own rows and
// every form field that shows a number has to be one of them.

// shownSettings: a form field, the setting it sets and the page it is on.
type shownSetting struct {
	svc, page, field, setting string
}

var shownSettings = []shownSetting{
	{"sqs", "/_console/sqs/orders?tab=config", "visibility", "VisibilityTimeout"},
	{"sqs", "/_console/sqs/orders?tab=config", "retention", "MessageRetentionPeriod"},
	{"sqs", "/_console/sqs/orders?tab=config", "delay", "DelaySeconds"},
	{"sqs", "/_console/sqs/orders?tab=config", "receive_wait", "ReceiveMessageWaitTimeSeconds"},
	{"sqs", "/_console/sqs/orders?tab=config", "max_bytes", "MaximumMessageSize"},
	{"sqs", "/_console/sqs/create", "visibility", "VisibilityTimeout"},
	{"sqs", "/_console/sqs/create", "delay", "DelaySeconds"},
	{"sqs", "/_console/sqs/create", "retention", "MessageRetentionPeriod"},
	{"sqs", "/_console/sqs/create", "max_bytes", "MaximumMessageSize"},
	{"sqs", "/_console/sqs/create", "receive_wait", "ReceiveMessageWaitTimeSeconds"},
}

func TestFormConstraintsAreTheServicesOwnRange(t *testing.T) {
	h := newConsole(t)
	req(t, h, "POST", "/_console/sqs/create", url.Values{"name": {"orders"}, "dlq_mode": {"none"}})
	pages := map[string]string{}
	for _, c := range shownSettings {
		if _, ok := pages[c.page]; !ok {
			pages[c.page] = req(t, h, "GET", c.page, nil).Body.String()
		}
	}
	for _, c := range shownSettings {
		t.Run(c.svc+"/"+c.setting+"@"+c.page+":"+c.field, func(t *testing.T) {
			rec, ok := settings.Lookup(c.svc, c.setting)
			if !ok {
				t.Fatalf("the form shows %s and no record describes it", c.setting)
			}
			if rec.Min == nil || rec.Max == nil {
				t.Fatalf("%s has no stated range to hold the form to", c.setting)
			}
			// Every <input> of that name on the RENDERED page must carry the
			// range, and at least one must exist: a field renamed in a template
			// fails here rather than going unchecked.
			input := regexp.MustCompile(`<input[^>]*\bname="` + c.field + `"[^>]*>`)
			found := input.FindAllString(pages[c.page], -1)
			if len(found) == 0 {
				t.Fatalf("no <input name=%q> on %s", c.field, c.page)
			}
			for _, tag := range found {
				for attr, want := range map[string]int64{"min": *rec.Min, "max": *rec.Max} {
					m := regexp.MustCompile(`\b` + attr + `="(\d+)"`).FindStringSubmatch(tag)
					if m == nil {
						t.Errorf("%s has no %s, the service's is %d", tag, attr, want)
						continue
					}
					got, _ := strconv.ParseInt(m[1], 10, 64)
					if got != want {
						t.Errorf("%s: %s=%d, the service's range says %d", tag, attr, got, want)
					}
				}
			}
		})
	}
}
