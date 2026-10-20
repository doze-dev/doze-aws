package docs

import (
	"strings"
	"testing"
)

// The fidelity dialog's "Differences from AWS" section is read from the
// ledger at request time. These hold the reading: every service has at least
// one difference, a bullet's bold lead is split from its body, markup does not
// leak into the text a person reads, and a bullet that wraps over several
// lines is one difference.

func TestEveryLedgerHasDifferencesTheDialogCanShow(t *testing.T) {
	for _, svc := range Services() {
		d := Differences(svc)
		if len(d) == 0 {
			t.Errorf("%s: no differences read from `### Differences from AWS`", svc)
		}
		for i, x := range d {
			if strings.TrimSpace(x.Lead+x.Text) == "" {
				t.Errorf("%s #%d: empty difference", svc, i)
			}
			for _, bad := range []string{"**", "`", "\n"} {
				if strings.Contains(x.Lead, bad) || strings.Contains(x.Text, bad) {
					t.Errorf("%s #%d: %q leaked into %q / %q", svc, i, bad, x.Lead, x.Text)
				}
			}
		}
	}
}

func TestADifferenceSplitsItsBoldLeadFromItsBody(t *testing.T) {
	d := Differences("sqs")
	var got *Difference
	for i := range d {
		if strings.HasPrefix(d[i].Lead, "Redrive and retention run on the receive path") {
			got = &d[i]
		}
	}
	if got == nil {
		t.Fatalf("SQS's redrive difference not found among %d: %+v", len(d), d)
	}
	if got.Lead != "Redrive and retention run on the receive path and a janitor" {
		t.Errorf("lead = %q", got.Lead)
	}
	want := ", rather than continuously server-side. A queue nobody reads is still swept, so the observable result converges; the timing of a move to the DLQ does not have to match AWS to the second."
	if got.Text != want {
		t.Errorf("text = %q\nwant   %q", got.Text, want)
	}
	// A lead that ends a sentence is followed by a space, not a comma.
	for _, x := range d {
		if x.Lead == "Delivery is in-process." && !strings.HasPrefix(x.Text, " A message fans out") {
			t.Errorf("text after %q = %q", x.Lead, x.Text)
		}
	}
}

// Lambda's section is bullets with no bold lead: each is a difference, with no
// lead, and its heading ("... in one place") is still found.
func TestADifferenceWithoutALeadIsAllBody(t *testing.T) {
	d := Differences("lambda")
	if len(d) != 6 {
		t.Fatalf("lambda has %d differences, the ledger lists 6: %+v", len(d), d)
	}
	if d[0].Lead != "" || !strings.HasPrefix(d[0].Text, "The interpreter is the host's; python3.12 means") {
		t.Errorf("first = %+v", d[0])
	}
}

// The emulator's own calls are rows with a dash for a tier. Rows leaves them out
// (they are not a fourth tier, and the ratchets over F, C and S must not move);
// Extensions reads exactly them.
func TestExtensionsAreTheDashRowsAndRowsLeavesThemOut(t *testing.T) {
	ext := Extensions("sqs")
	if len(ext) != 1 || ext[0].Ops != "DozePeek" || ext[0].Tier != ExtensionTier ||
		ext[0].Note != "doze extension: read-only full-queue inspection (no visibility/receive-count side effects)" {
		t.Fatalf("SQS extensions = %+v", ext)
	}
	for _, r := range Rows("sqs") {
		if r.Ops == "DozePeek" {
			t.Errorf("Rows kept the extension row %+v", r)
		}
	}
	if got := Extensions("iam"); len(got) != 1 || got[0].Ops != "DozeAccessLog / DozeGeneratePolicy" {
		t.Errorf("IAM extensions = %+v", got)
	}
	if got := Extensions("kms"); len(got) != 0 {
		t.Errorf("KMS has no extensions, got %+v", got)
	}
}

func TestNoLedgerNoDifferences(t *testing.T) {
	if d := Differences("nonesuch"); d != nil {
		t.Errorf("got %+v", d)
	}
}
