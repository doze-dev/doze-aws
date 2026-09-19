package main

// The block a developer reads once the server is up.
//
// # Why there is one at all
//
// Until this, a successful start produced slog key=value lines on stderr and
// nothing else. Everything they said was true and none of it answered the two
// questions somebody actually has in the first minute: how do I point my code
// at this, and where is the thing I can look at. The answers existed — in
// `doze-aws env`, in the README, on the console's own Connect page — which is
// three places that all require knowing they exist.
//
// The console is the sharper half of that. It is the largest single piece of
// this project and the one thing a local AWS can offer that real AWS cannot,
// and it was discoverable only from one log line among eight.
//
// # Why stdout
//
// The log lines stay exactly as they are, on stderr, in logfmt. One of them —
// `msg=listening` — is parsed by the e2e suite and by anything wrapping this
// binary, and main.go says so at the line itself. So this goes to STDOUT
// instead, which nothing on the server path writes to. The split is worth
// having for its own sake: logs on stderr, the summary for a person on stdout,
// and `doze-aws 2>/dev/null` shows just the summary.

import (
	"fmt"
	"io"
	"strings"
)

// ready is what to tell someone once the server is listening.
type ready struct {
	endpoint string   // the primary URL
	console  string   // "" when the console is switched off
	services []string // enabled, in gateway order
	region   string
	account  string
}

// write prints the block, or nothing at all when there is no endpoint to name.
func (r ready) write(w io.Writer) {
	if r.endpoint == "" {
		return
	}
	var b strings.Builder
	b.WriteString("\n  doze-aws is up.\n\n")
	fmt.Fprintf(&b, "    endpoint  %s\n", r.endpoint)
	if r.console != "" {
		// Annotated, not just linked. "Console" means the AWS console to most
		// people; what this one opens on is a live view of the calls your own
		// app is making, and that is the part worth knowing before clicking.
		fmt.Fprintf(&b, "    console   %s   what your app is doing, live\n", r.console)
	}
	fmt.Fprintf(&b, "    serving   %s in %s, account %s\n",
		countServices(r.services), r.region, r.account)

	// Command first, explanation second. The command is the part that gets
	// copied, so it wants to be at a predictable left edge rather than at the
	// end of a sentence of varying length.
	b.WriteString("\n")
	fmt.Fprintf(&b, "    %-24s point this shell at it\n", `eval "$(doze-aws env)"`)
	fmt.Fprintf(&b, "    %-24s when something looks wrong\n\n", "doze-aws doctor")

	io.WriteString(w, b.String()) //nolint:errcheck // a banner on stdout
}

// countServices words the service count, naming them when there are few enough
// to be worth naming. "17 services" is the useful form for the default; when
// somebody passed --services they chose a handful and want to see the handful.
func countServices(svcs []string) string {
	switch {
	case len(svcs) == 0:
		return "no services"
	case len(svcs) == 1:
		return svcs[0]
	case len(svcs) <= 4:
		return strings.Join(svcs[:len(svcs)-1], ", ") + " and " + svcs[len(svcs)-1]
	}
	return fmt.Sprintf("%d services", len(svcs))
}
