package console_test

// B4's ratchet. A number field with a bound is a setting, and a setting has a
// record (internal/settings: its unit, range, default, tier and what enforces
// it), which the kit's setting_field reads, so the page cannot teach a number
// the service does not hold. SQS's fields are all built that way. The other
// services' bounded number inputs are still hand-written in their templates,
// and each service's stage turns them into records and setting_field calls.
//
// Until it does, this test freezes how many each template has, in both
// directions: a new bounded `<input type="number" min= max=>` written by hand
// fails (make it a setting_field with a record), and so does one that has been
// converted without the count being lowered here, so the list only shrinks and
// a stage's progress is a number going down in a diff.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// handWrittenBounded is the frozen count of `<input type="number">` with a min
// or a max, per template file (2026-10-20). kit.html's own is setting_field
// itself and is not counted.
var handWrittenBounded = map[string]int{
	"apigw.html":   2,
	"ddb.html":     2,
	"eb.html":      1,
	"eb_http.html": 2,
	"iam.html":     2,
	"kinesis.html": 4,
	"kms.html":     1,
	"lambda.html":  5,
	"s3.html":      1,
	"sm.html":      1,
	"sns.html":     1,
}

var numberInput = regexp.MustCompile(`(?s)<input[^>]*?type="number"[^>]*?>`)

func TestNoBoundedNumberInputIsHandWrittenBeyondTheFrozenCount(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) < 20 {
		t.Fatalf("found %d templates: %v", len(files), err)
	}
	got := map[string]int{}
	for _, f := range files {
		name := filepath.Base(f)
		if name == "kit.html" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range numberInput.FindAllString(string(src), -1) {
			if strings.Contains(tag, " min=") || strings.Contains(tag, " max=") {
				got[name]++
			}
		}
	}
	var names []string
	seen := map[string]bool{}
	for n := range got {
		names = append(names, n)
		seen[n] = true
	}
	for n := range handWrittenBounded {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		want := handWrittenBounded[n]
		switch {
		case got[n] > want:
			t.Errorf("%s has %d hand-written bounded number inputs, %d are frozen: a number with a bound is a setting; add its record under internal/settings and draw it with setting_field", n, got[n], want)
		case got[n] < want:
			t.Errorf("%s has %d hand-written bounded number inputs, %d are frozen: lower the count in handWrittenBounded (this is the progress being recorded)", n, got[n], want)
		}
	}
}
