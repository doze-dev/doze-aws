package main

// Confirming a flag that overrules the config file.
//
// # Why these ask rather than warn
//
// Flags beat the config file, and that is right — a throwaway --data-dir is a
// reasonable thing to want. But four of them produce a running instance that,
// from the outside, looks like something went wrong:
//
//	--data-dir   a different directory is an EMPTY instance
//	--services   the dropped services keep their data; it is simply not served
//	--name       every URL minted under the old name stops resolving
//	--region     unqualified requests land somewhere else
//
// A warning about those is a warning nobody can act on: by the time it is
// printed, the instance is already up and the queues already look gone. A
// question can be acted on, and it costs one keystroke in the case where the
// answer is yes.
//
// --account-id is deliberately NOT here. It is refused outright by the data's
// own stamp, because stored ARNs embed the account and every cross-resource
// reference would break at fire time. That is not a question.
//
// # Why only on a terminal
//
// The same reasoning as dnsready.go, and it matters more here: a prompt with
// nobody to answer it is a server that hangs at boot. CI passes --data-dir
// deliberately, in a script somebody wrote on purpose, and there is no one
// there to educate. Off a terminal these warn and proceed.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// consequential are the flags whose silent override of a config file looks,
// from the outside, like damage — mapped to what it actually costs.
var consequential = map[string]string{
	"data-dir": "the resources you had are in the directory the file names, not here",
	"services": "the services you dropped keep their data on disk; it is simply not served",
	"name":     "URLs minted under the old name stop resolving — nothing claims it once you rename",
	"region":   "unqualified requests land in the new region; the old one's resources stay under its own folder",
}

// overrideEnv is the terminal, injected so this is testable without one.
type overrideEnv struct {
	isTTY func() bool
	in    io.Reader
	out   io.Writer
	// assumeYes is --yes: the escape for someone who does this daily and for
	// a script that wants the prompt skipped rather than the warning silenced.
	assumeYes bool
}

func liveOverrideEnv(assumeYes bool) overrideEnv {
	return overrideEnv{isTTY: stdinIsTTY, in: os.Stdin, out: os.Stderr, assumeYes: assumeYes}
}

// errOverrideDeclined is the sentinel for "the user said no", so the caller can
// exit quietly rather than reporting a failure.
var errOverrideDeclined = fmt.Errorf("doze-aws: cancelled")

// overriddenKeys are the consequential flags that overrule something the file
// actually set, sorted so the prompt is stable.
//
// Only keys the file SET count. A flag filling in a blank overrules no
// decision, and asking about it would be the kind of noise that teaches people
// to answer without reading.
func overriddenKeys(fileKeys, given map[string]bool) []string {
	var out []string
	for name := range given {
		if consequential[name] != "" && fileKeys[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// confirmOverrides asks before letting a flag overrule the config file.
func confirmOverrides(e overrideEnv, path string, fileKeys, given map[string]bool) error {
	keys := overriddenKeys(fileKeys, given)
	if len(keys) == 0 {
		return nil
	}

	fmt.Fprintf(e.out, "\nThese flags overrule %s:\n\n", path)
	for _, k := range keys {
		fmt.Fprintf(e.out, "  --%-10s %s\n", k, consequential[k])
	}
	fmt.Fprintln(e.out)

	// No terminal: say it and carry on. A server that stops to ask a question
	// nobody can answer is worse than one that does what it was told.
	if !e.isTTY() || e.assumeYes {
		if !e.assumeYes {
			fmt.Fprintln(e.out, "Proceeding — there is no terminal to ask on. Pass --yes to silence this.")
		}
		return nil
	}

	// Default NO. dns-setup defaults to yes because saying yes is what the user
	// came for; this one can look like data loss, so the safe answer is the one
	// you get by pressing enter.
	fmt.Fprint(e.out, "Continue? [y/N] ")
	answer, err := bufio.NewReader(e.in).ReadString('\n')

	// EOF with nothing typed means there was nobody to ask after all, and
	// declining would be refusing to start for lack of an answer nobody could
	// give. `doze-aws &` is the case that proves the char-device check is not
	// enough on its own: stdin is still a terminal, but the shell has
	// disconnected it, so the read returns immediately with nothing.
	if err != nil && strings.TrimSpace(answer) == "" {
		fmt.Fprintln(e.out, "\nProceeding — nothing answered. Pass --yes to skip this.")
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	fmt.Fprintln(e.out, "Nothing was started. Drop the flag, or edit "+path+".")
	return errOverrideDeclined
}
