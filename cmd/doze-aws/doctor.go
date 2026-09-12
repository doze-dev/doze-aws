package main

// `doze-aws doctor` — what is in place, what is not, and what to run.
//
// The zone's failure modes are quiet ones. A name that does not resolve looks
// like a server that is down; a name that resolves to an address nothing is
// listening on looks the same; a peer holding the apex looks like your own
// instance being ignored. Each has a different remedy, and none of them is
// visible from the error the SDK reports.
//
// So this prints the state rather than guessing at it, and needs no privilege
// to do so — names.Check is deliberately unprivileged, which is what makes
// this safe to run first when something is wrong.

import (
	"fmt"
	"io"
	"os"

	"github.com/doze-dev/doze-aws/internal/config"
	names "github.com/doze-dev/doze-names"
)

const doctorUsage = "report what is set up for .doze names, and what is not"

func runDoctor(args []string) int {
	// The ordinary config flags, not a set of its own: doctor reports on the
	// instance a given invocation WOULD start, so it has to read the same
	// configuration that invocation would.
	st, err := loadConfig(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return doctorTo(os.Stdout, st.cfg, names.Check(), names.Open(names.Home(), "doze-aws"))
}

// registry is the slice of *names.Registry doctor reads, named so a test can
// supply one without a filesystem.
type registry interface {
	Snapshot() map[string]names.Entry
}

func doctorTo(w io.Writer, cfg config.Config, status names.Status, reg registry) int {
	id := cfg.Identity()

	fmt.Fprintf(w, "doze-aws %s\n\n", version)

	fmt.Fprintln(w, "this instance")
	fmt.Fprintf(w, "  name        %s\n", cfg.InstanceName())
	fmt.Fprintf(w, "  answers on  %s\n", names.Qualified("aws", cfg.InstanceName()).Host)
	fmt.Fprintf(w, "  region      %s\n", id.RegionName())
	fmt.Fprintf(w, "  account     %s\n", id.Account())
	fmt.Fprintf(w, "  data        %s\n", cfg.DataDir)
	// An explicit --suffix is worth distinguishing from the derived one,
	// because only the derived one follows the instance if it is renamed.
	switch {
	case cfg.Suffix != "":
		fmt.Fprintf(w, "  suffix      %s (--suffix)\n", cfg.Suffix)
	default:
		fmt.Fprintf(w, "  suffix      %s (from the name)\n", names.Qualified("aws", cfg.InstanceName()).Host)
	}

	fmt.Fprintf(w, "\n.doze on this machine (%s)\n", status.Platform)
	ready := status.OK()
	for _, step := range status.Steps {
		mark := "✗"
		if step.Done {
			mark = "✓"
		}
		fmt.Fprintf(w, "  %s %-22s %s\n", mark, step.Name, step.Detail)
	}
	if len(status.Steps) == 0 {
		fmt.Fprintln(w, "  (nothing reported — this platform has no setup to do)")
	}

	// Who holds which name matters: "my name stopped working" is almost always
	// another process holding it, and the registry knows which.
	if reg != nil {
		snap := reg.Snapshot()
		fmt.Fprintf(w, "\nnames registered (%d)\n", len(snap))
		if len(snap) == 0 {
			fmt.Fprintln(w, "  (none — no doze binary is running)")
		}
		for host, e := range snap {
			fmt.Fprintf(w, "  %-28s %-14s pid %d", host, e.Owner, e.PID)
			if e.Target != "" {
				fmt.Fprintf(w, "  -> %s", e.Target)
			}
			fmt.Fprintln(w)
		}
	}

	fmt.Fprintln(w)
	if ready {
		fmt.Fprintln(w, "✓ .doze resolves. Names will work.")
		return 0
	}
	fmt.Fprintln(w, "✗ .doze does not resolve yet.")
	fmt.Fprintln(w, "    doze-aws dns-setup          set it up (one sudo, once per machine)")
	fmt.Fprintln(w, "    doze-aws dns-setup --print  print the script instead, to run yourself")
	fmt.Fprintln(w, "    doze-aws --listen host:port serve on an address instead of a name")
	// Not an error exit: being told what is missing is the command working.
	return 0
}
