package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A flag beating the config file is the documented precedence and is right —
// a throwaway --data-dir is a reasonable thing to want. What was wrong is that
// it happened WITHOUT A WORD, and three of these produce a running instance
// that looks like it lost data:
//
//	--data-dir  a different directory is an empty instance
//	--services  the dropped services keep their data, it is just not served
//	--name      every URL minted under the old name is now NXDOMAIN
//
// Measured before the fix: `--data-dir /tmp/elsewhere` against a config naming
// ./store served zero queues, and the only clue was one INFO line naming the
// new path. Nothing connected it to the file.
func TestAFlagOverridingTheFileSaysSo(t *testing.T) {
	for _, tc := range []struct {
		what     string
		file     map[string]bool
		given    map[string]bool
		wantFlag string
		wantWhy  string
	}{
		{
			what:     "data-dir",
			file:     map[string]bool{"data-dir": true, "name": true},
			given:    map[string]bool{"data-dir": true},
			wantFlag: "--data-dir",
			wantWhy:  "directory the file names",
		},
		{
			what:     "services",
			file:     map[string]bool{"services": true},
			given:    map[string]bool{"services": true},
			wantFlag: "--services",
			wantWhy:  "not served",
		},
		{
			what:     "name",
			file:     map[string]bool{"name": true},
			given:    map[string]bool{"name": true},
			wantFlag: "--name",
			wantWhy:  "no longer resolve",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			reportOverrides(logger, "doze-aws.toml", tc.file, tc.given)

			out := buf.String()
			if !strings.Contains(out, tc.wantFlag) {
				t.Errorf("no warning naming %s:\n%s", tc.wantFlag, out)
			}
			// The consequence is the point. "a flag overrode the config file"
			// alone tells a reader nothing they could act on.
			if !strings.Contains(out, tc.wantWhy) {
				t.Errorf("the warning does not say what it costs (%q):\n%s", tc.wantWhy, out)
			}
			if !strings.Contains(out, "doze-aws.toml") {
				t.Errorf("the warning does not name the file it overrode:\n%s", out)
			}
		})
	}
}

// Noise is the failure mode that gets warnings ignored, so the quiet cases are
// worth asserting as hard as the loud ones.
func TestOverridesStaySilentWhenNothingWasOverridden(t *testing.T) {
	for _, tc := range []struct {
		what  string
		file  map[string]bool
		given map[string]bool
	}{
		// A flag filling in a blank is not an override — the file said nothing
		// about it, so there is no decision being overruled.
		{"the file did not set it", map[string]bool{"name": true}, map[string]bool{"data-dir": true}},
		// Flags with no consequence worth a line.
		{"an inconsequential flag", map[string]bool{"listen": true}, map[string]bool{"listen": true}},
		{"console", map[string]bool{"data-dir": true}, map[string]bool{"console": true}},
		{"no flags at all", map[string]bool{"data-dir": true, "name": true}, map[string]bool{}},
		{"no file at all", map[string]bool{}, map[string]bool{"data-dir": true, "name": true}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			reportOverrides(logger, "doze-aws.toml", tc.file, tc.given)
			if buf.Len() != 0 {
				t.Errorf("expected silence, got:\n%s", buf.String())
			}
		})
	}
}

// The set of flags worth warning about is a judgement call, so it is asserted
// rather than left to drift. account-id is deliberately absent: it is REFUSED
// by the data's own stamp rather than warned about, because stored ARNs embed
// it — see instance.go.
func TestTheConsequentialSetIsDeliberate(t *testing.T) {
	for _, name := range []string{"data-dir", "services", "name", "region"} {
		if consequential[name] == "" {
			t.Errorf("%s should warn on override", name)
		}
	}
	if _, present := consequential["account-id"]; present {
		t.Error("account-id is refused by the instance stamp, not warned about")
	}
	for _, name := range []string{"listen", "console", "iam-mode", "lambda-idle", "config"} {
		if consequential[name] != "" {
			t.Errorf("%s is not destructive-looking; warning about it is noise", name)
		}
	}
}
