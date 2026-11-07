package main

// `doze-aws doctor` — where this instance answers, and whether something does.
//
// When an SDK says it cannot connect, those are the two things to know: the
// address the configuration names, and whether anything is listening there
// (a doze-aws started from another directory, with another --listen, is the
// usual surprise). It reads the same configuration a start would.

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/doze-dev/doze-aws/internal/config"
)

const doctorUsage = "say where this instance answers, and whether something answers there"

func runDoctor(args []string) int {
	st, err := loadConfig(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return doctorTo(os.Stdout, st.cfg, answers)
}

// answers says whether something accepts a TCP connection at addr.
func answers(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func doctorTo(w io.Writer, cfg config.Config, reach func(addr string) bool) int {
	id := cfg.Identity()
	addr := reachableHost(cfg.ListenAddr)

	fmt.Fprintf(w, "doze-aws %s\n\n", version)
	fmt.Fprintln(w, "this instance")
	fmt.Fprintf(w, "  name        %s\n", cfg.InstanceName())
	fmt.Fprintf(w, "  answers on  %s\n", addr)
	fmt.Fprintf(w, "  region      %s\n", id.RegionName())
	fmt.Fprintf(w, "  account     %s\n", id.Account())
	fmt.Fprintf(w, "  data        %s\n", cfg.DataDir)
	if cfg.Suffix != "" {
		fmt.Fprintf(w, "  suffix      %s (--suffix)\n", cfg.Suffix)
	} else {
		fmt.Fprintln(w, "  suffix      (none — URLs use path shapes; see --suffix)")
	}

	fmt.Fprintln(w)
	if reach(addr) {
		fmt.Fprintf(w, "✓ something answers at %s.\n", addr)
		return 0
	}
	fmt.Fprintf(w, "✗ nothing answers at %s.\n", addr)
	fmt.Fprintln(w, "    doze-aws                    start it here")
	fmt.Fprintln(w, "    doze-aws --listen host:port start it on another address")
	// Not an error exit: being told what is missing is the command working.
	return 0
}
