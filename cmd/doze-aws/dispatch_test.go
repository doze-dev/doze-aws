package main

// The dispatch table, and the one property that matters: nothing except a
// deliberate start may end up booting a server.
//
// It used to. The old dispatch was a chain of positional `if os.Args[1] ==`
// checks that fell through to loadConfig for anything unrecognised, so
// `doze-aws help` bound :4566 and started serving AWS — as did any typo. The
// only way to notice was that your terminal did not come back.

import "testing"

func TestDispatchNeverFallsThroughToAStart(t *testing.T) {
	// handled=true means dispatch dealt with it and main exits; handled=false
	// means "this is a start", which must be true for exactly the inputs a
	// person would use to start the emulator.
	for _, tc := range []struct {
		args    []string
		handled bool
		why     string
	}{
		{nil, false, "bare doze-aws starts the emulator"},
		{[]string{"--listen", "127.0.0.1:4566"}, false, "flags start the emulator"},
		{[]string{"-listen=127.0.0.1:4566"}, false, "single-dash flags too"},

		{[]string{"help"}, true, "help prints help"},
		{[]string{"--help"}, true, "and so does --help"},
		{[]string{"-h"}, true, "and -h"},
		{[]string{"version"}, true, "version prints the version"},

		// The bug: anything unrecognised used to start a server.
		{[]string{"aply"}, true, "a typo is an error, not a boot"},
		{[]string{"serve"}, true, "a plausible-but-wrong command is an error"},
		{[]string{"config"}, true, "an incomplete two-word command is a usage error"},
		{[]string{"config", "dump"}, true, "as is a wrong second word"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			_, handled := dispatch(tc.args)
			if handled != tc.handled {
				t.Errorf("dispatch(%q) handled = %v, want %v — %s",
					tc.args, handled, tc.handled, tc.why)
			}
		})
	}
}

func TestDispatchExitCodes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"help"}, 0},
		{[]string{"version"}, 0},
		{[]string{"aply"}, 2},
		{[]string{"config"}, 2},
		{[]string{"config", "dump"}, 2},
	} {
		if code, _ := dispatch(tc.args); code != tc.code {
			t.Errorf("dispatch(%q) = %d, want %d", tc.args, code, tc.code)
		}
	}
}

// Every command in the table is reachable by its own name, which is what the
// table is for — the old chain could list a command in the docs and never
// dispatch it.
func TestEveryCommandIsReachable(t *testing.T) {
	for _, c := range commands() {
		if c.name == "" || c.desc == "" || c.run == nil {
			t.Errorf("incomplete command entry: %+v", c.name)
		}
	}
	if len(commands()) < 6 {
		t.Errorf("the table has %d commands; docs/cli.md documents six", len(commands()))
	}
}
