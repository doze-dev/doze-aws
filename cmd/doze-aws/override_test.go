package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// env builds an overrideEnv with a scripted answer and a captured transcript.
func env(t *testing.T, tty bool, answer string, yes bool) (overrideEnv, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return overrideEnv{
		isTTY:     func() bool { return tty },
		in:        strings.NewReader(answer),
		out:       &out,
		assumeYes: yes,
	}, &out
}

// A flag beating the config file is the documented precedence and is right — a
// throwaway --data-dir is a reasonable thing to want. What is NOT right is
// doing it without asking, because four of them produce a running instance
// that, from the outside, looks like something went wrong:
//
//	--data-dir  a different directory is an empty instance
//	--services  the dropped services keep their data, it is just not served
//	--name      every URL minted under the old name stops resolving
//	--region    unqualified requests land somewhere else
//
// Measured before this existed: `--data-dir /tmp/elsewhere` against a config
// naming ./store served zero queues, and the only clue was one INFO line
// naming the new path. A warning would not have helped either — by the time it
// prints, the instance is up and the queues already look gone. Hence a
// question, asked before anything is opened.
func TestAConsequentialOverrideAsksFirst(t *testing.T) {
	for _, tc := range []struct{ what, key, want string }{
		{"data-dir", "data-dir", "directory the file names"},
		{"services", "services", "not served"},
		{"name", "name", "stop resolving"},
		{"region", "region", "old one's resources stay"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			e, out := env(t, true, "n\n", false)
			err := confirmOverrides(e, "doze-aws.toml",
				map[string]bool{tc.key: true}, map[string]bool{tc.key: true})

			if !errors.Is(err, errOverrideDeclined) {
				t.Fatalf("answering no must stop the start, got %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "--"+tc.key) {
				t.Errorf("the prompt does not name the flag:\n%s", got)
			}
			// The consequence is the point — "a flag overrode the file" alone
			// gives a reader nothing to decide on.
			if !strings.Contains(got, tc.want) {
				t.Errorf("the prompt does not say what it costs (%q):\n%s", tc.want, got)
			}
			if !strings.Contains(got, "doze-aws.toml") {
				t.Errorf("the prompt does not name the file being overruled:\n%s", got)
			}
		})
	}
}

// Enter alone must be the SAFE answer. dns-setup defaults to yes because
// saying yes is what the user came for; this one can look like data loss, so
// the default is no.
func TestTheDefaultAnswerIsNo(t *testing.T) {
	// "" is absent deliberately: nothing typed at all is EOF, which means
	// nobody was there rather than someone saying no. See
	// TestEOFWithNothingTypedIsNobodyThereNotNo. "\n" is a real keypress.
	for _, answer := range []string{"\n", "no\n", "N\n", "maybe\n"} {
		e, _ := env(t, true, answer, false)
		err := confirmOverrides(e, "doze-aws.toml",
			map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true})
		if !errors.Is(err, errOverrideDeclined) {
			t.Errorf("answer %q should decline, got %v", answer, err)
		}
	}
	for _, answer := range []string{"y\n", "Y\n", "yes\n", " yes \n"} {
		e, _ := env(t, true, answer, false)
		if err := confirmOverrides(e, "doze-aws.toml",
			map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true}); err != nil {
			t.Errorf("answer %q should proceed, got %v", answer, err)
		}
	}
}

// No terminal means no question. A prompt nobody can answer is a server that
// hangs at boot, and CI passes --data-dir deliberately in a script somebody
// wrote on purpose — there is nobody there to educate.
func TestWithNoTerminalItSaysSoAndProceeds(t *testing.T) {
	e, out := env(t, false, "", false)
	if err := confirmOverrides(e, "doze-aws.toml",
		map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true}); err != nil {
		t.Fatalf("must not block without a terminal: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "--data-dir") || !strings.Contains(got, "directory the file names") {
		t.Errorf("it should still say what happened:\n%s", got)
	}
	if strings.Contains(got, "Continue?") {
		t.Errorf("it must not ask a question nobody can answer:\n%s", got)
	}
}

// `doze-aws &` is the case the character-device check cannot catch: stdin is
// still a terminal, but the shell has disconnected it, so the read returns
// immediately with nothing. Declining there means a backgrounded server
// refusing to start for want of an answer nobody could give.
//
// Found on the real binary, not reasoned about — it printed the prompt and
// exited with "Nothing was started."
func TestEOFWithNothingTypedIsNobodyThereNotNo(t *testing.T) {
	// A "terminal" whose input is already closed.
	e, out := env(t, true, "", false)
	if err := confirmOverrides(e, "doze-aws.toml",
		map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true}); err != nil {
		t.Fatalf("EOF must not be read as a refusal: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "nothing answered") {
		t.Errorf("it should say why it went ahead:\n%s", got)
	}

	// But a typed "no" with no trailing newline is still a no — the difference
	// is whether anything was said, not whether it ended in \n.
	e, _ = env(t, true, "n", false)
	if err := confirmOverrides(e, "doze-aws.toml",
		map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true}); !errors.Is(err, errOverrideDeclined) {
		t.Errorf("a typed refusal must still decline, got %v", err)
	}
}

// --yes is the escape for someone who does this daily, and for a script that
// wants the prompt skipped rather than the explanation silenced.
func TestYesSkipsThePromptButNotTheExplanation(t *testing.T) {
	e, out := env(t, true, "", true)
	if err := confirmOverrides(e, "doze-aws.toml",
		map[string]bool{"data-dir": true}, map[string]bool{"data-dir": true}); err != nil {
		t.Fatalf("--yes must proceed: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Continue?") {
		t.Errorf("--yes should not ask:\n%s", got)
	}
	if !strings.Contains(got, "--data-dir") {
		t.Errorf("--yes should still say what it did:\n%s", got)
	}
}

// Noise is what teaches people to answer without reading, so the quiet cases
// are worth asserting as hard as the loud ones.
func TestNothingIsAskedWhenNothingWasOverridden(t *testing.T) {
	for _, tc := range []struct {
		what        string
		file, given map[string]bool
	}{
		// A flag filling in a blank overrules no decision: the file said
		// nothing about it.
		{"the file did not set it", map[string]bool{"name": true}, map[string]bool{"data-dir": true}},
		{"an inconsequential flag", map[string]bool{"listen": true}, map[string]bool{"listen": true}},
		{"console", map[string]bool{"data-dir": true}, map[string]bool{"console": true}},
		{"no flags at all", map[string]bool{"data-dir": true, "name": true}, map[string]bool{}},
		{"no file at all", map[string]bool{}, map[string]bool{"data-dir": true, "name": true}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			e, out := env(t, true, "n\n", false)
			if err := confirmOverrides(e, "doze-aws.toml", tc.file, tc.given); err != nil {
				t.Fatalf("expected no question, got %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("expected silence, got:\n%s", out.String())
			}
		})
	}
}

// The set is a judgement call, so it is asserted rather than left to drift.
// account-id is deliberately absent: the data's own stamp REFUSES a mismatch,
// because stored ARNs embed the account — that is not a question.
func TestTheConsequentialSetIsDeliberate(t *testing.T) {
	for _, name := range []string{"data-dir", "services", "name", "region"} {
		if consequential[name] == "" {
			t.Errorf("%s should be confirmed on override", name)
		}
	}
	if _, present := consequential["account-id"]; present {
		t.Error("account-id is refused by the instance stamp, not confirmed")
	}
	for _, name := range []string{"listen", "console", "iam-mode", "lambda-idle", "config", "yes"} {
		if consequential[name] != "" {
			t.Errorf("%s costs nothing anyone would mistake for damage; asking is noise", name)
		}
	}
}

// All of them at once, in a stable order, in ONE question — four prompts in a
// row is how you train someone to hold down y.
func TestSeveralOverridesAreOneStablePrompt(t *testing.T) {
	all := map[string]bool{"data-dir": true, "name": true, "region": true, "services": true}
	e, out := env(t, true, "y\n", false)
	if err := confirmOverrides(e, "doze-aws.toml", all, all); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if n := strings.Count(got, "Continue?"); n != 1 {
		t.Errorf("want exactly one question, got %d:\n%s", n, got)
	}
	if got, want := overriddenKeys(all, all), []string{"data-dir", "name", "region", "services"}; len(got) != 4 ||
		got[0] != want[0] || got[3] != want[3] {
		t.Errorf("keys = %v, want %v (sorted)", got, want)
	}
}
