package console

import (
	"strings"
	"testing"
)

// A body shown on the page is the body the application sent, with whitespace
// moved. The expanded message and "Copy body" went through a decode and an
// encode: keys sorted, 12345678901234567890 became 12345678901234567000, 1.10
// became 1.1, "<b>" became "\u003cb\u003e" and the first of two "dup" keys was
// dropped.
func TestAPrettyBodyIsTheBodyWithOnlyItsWhitespaceMoved(t *testing.T) {
	in := `{"z":1,"a":{"y":2,"b":3},"big":12345678901234567890,"price":1.10,"html":"<b>&</b>","dup":1,"dup":2}`
	got := prettyJSON(in)
	for _, want := range []string{`"z": 1`, `"big": 12345678901234567890`, `"price": 1.10`, `"<b>&</b>"`, `"dup": 1`, `"dup": 2`} {
		if !strings.Contains(got, want) {
			t.Errorf("prettyJSON lost %s:\n%s", want, got)
		}
	}
	if strings.Index(got, `"z"`) > strings.Index(got, `"a"`) {
		t.Errorf("the keys were reordered:\n%s", got)
	}
	if compact := strings.Join(strings.Fields(got), ""); compact != strings.Join(strings.Fields(in), "") {
		t.Errorf("more than whitespace changed:\n%s", got)
	}
	if prettyJSON("not json") != "not json" || prettyJSON("") != "" {
		t.Error("a non-JSON body must pass through")
	}
}
