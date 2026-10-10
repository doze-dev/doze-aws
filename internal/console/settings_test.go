package console_test

import (
	"os"
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

type shownSetting struct {
	svc, template, field, setting string
}

var shownSettings = []shownSetting{
	{"sqs", "templates/sqs.html", "visibility", "VisibilityTimeout"},
	{"sqs", "templates/sqs.html", "retention", "MessageRetentionPeriod"},
	{"sqs", "templates/create.html", "visibility", "VisibilityTimeout"},
	{"sqs", "templates/create.html", "delay", "DelaySeconds"},
	{"sqs", "templates/create.html", "retention", "MessageRetentionPeriod"},
	{"sqs", "templates/create.html", "max_bytes", "MaximumMessageSize"},
	{"sqs", "templates/create.html", "receive_wait", "ReceiveMessageWaitTimeSeconds"},
}

func TestFormConstraintsAreTheServicesOwnRange(t *testing.T) {
	for _, c := range shownSettings {
		t.Run(c.svc+"/"+c.setting+"@"+c.template+":"+c.field, func(t *testing.T) {
			rec, ok := settings.Lookup(c.svc, c.setting)
			if !ok {
				t.Fatalf("the form shows %s and no record describes it", c.setting)
			}
			if rec.Min == nil || rec.Max == nil {
				t.Fatalf("%s has no stated range to hold the form to", c.setting)
			}
			raw, err := os.ReadFile("../../internal/console/" + c.template)
			if err != nil {
				t.Fatal(err)
			}
			// Every <input> of that name in the file must carry the range, and at
			// least one must exist: a field renamed in the template fails here
			// rather than going unchecked.
			input := regexp.MustCompile(`<input[^>]*\bname="` + c.field + `"[^>]*>`)
			found := input.FindAllString(string(raw), -1)
			if len(found) == 0 {
				t.Fatalf("no <input name=%q> in %s", c.field, c.template)
			}
			for _, tag := range found {
				for attr, want := range map[string]int64{"min": *rec.Min, "max": *rec.Max} {
					m := regexp.MustCompile(`\b` + attr + `="(\d+)"`).FindStringSubmatch(tag)
					if m == nil {
						// A field with no bound on a side is a different bug (the
						// service would refuse what the page offered); name it.
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
