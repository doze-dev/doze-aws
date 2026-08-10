package main

// Adoption of the shared .doze zone.
//
// doze-aws claims aws.doze and, if the machine has been set up, serves on the
// address that name points at — in addition to the configured listen address,
// never instead of it. 127.0.0.1:4566 is a permanent contract (docs/endpoints.md);
// a name is something extra, and everything here degrades to a log line.
//
// It also joins the zone as a peer: if no other doze binary is serving DNS,
// this process serves it, answering for every peer's names and not only its
// own. That is what lets a machine with nothing but doze-aws installed still
// resolve .doze — and what lets doze-kafka's names work when doze-aws is the
// one that happens to be running.

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	names "github.com/doze-dev/doze-names"
)

// apexPort is the port an apex name implies. http://aws.doze has to mean the
// same URL whether a standalone process or a doze stack is behind it, and the
// stack serves it port-less, so standalone binds 80 on its own address rather
// than exposing the configured high port under the name.
const apexPort = 80

// zone is doze-aws's participation in .doze.
type zone struct {
	lease *names.Lease
	srv   *names.Server
	front *names.Ingress
	extra net.Listener
	reg   *names.Registry
}

// joinZone claims aws.doze and starts serving the zone if nobody else is.
// It never fails: a machine that has not run dns-setup simply has no names,
// which is a smaller thing than refusing to start.
func joinZone(ctx context.Context, logger *slog.Logger) *zone {
	z := &zone{}
	reg := names.Open(names.Home(), "doze-aws")

	lease, err := reg.Claim(names.Apex("aws"))
	switch {
	case err == nil:
		z.lease = lease
	default:
		if held, ok := names.Held(err); ok {
			// First-come, and the holder keeps it. Saying which process has it
			// turns "my name stopped working" into something answerable.
			logger.Info("zone: apex name already claimed",
				"name", held.Host, "held_by_pid", held.PID, "owner", held.Owner)
		} else {
			logger.Debug("zone: could not claim the apex name", "err", err)
		}
	}

	z.reg = reg
	logf := func(format string, args ...any) { logger.Debug(fmt.Sprintf(format, args...)) }
	z.srv = names.Serve(ctx, reg, logf)
	// The shared front door is what makes the name port-less. macOS will not
	// let an unprivileged process hold :80 on a specific address, only on the
	// wildcard, so one listener fronts every peer's names by Host header.
	z.front = names.ServeIngress(ctx, reg, logf)
	return z
}

// listen returns an extra listener on the apex address.
//
// Port 80 is tried first, because http://aws.doze with no port is the whole
// point of the name — but macOS refuses a privileged port on a SPECIFIC
// address even though it allows one on the wildcard, so this usually fails
// there and falls back to the configured port. A name that answers on
// :<port> is worth more than a name that answers nowhere.
//
// The port-less form needs a wildcard-bound, Host-routed front door shared
// between the binaries, the way doze core already fronts aws.<stack>.doze.
func (z *zone) listen(logger *slog.Logger, port string) net.Listener {
	if z == nil || z.lease == nil || port == "" {
		return nil
	}
	// The service binds its own address on its ordinary port; :80 is not
	// attempted here, because macOS refuses a privileged port on a specific
	// address. The front door supplies the port-less form.
	addr := net.JoinHostPort(z.lease.IP.String(), port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Info("zone: name claimed but not served",
			"name", z.lease.Name.Host, "addr", addr, "err", err,
			"hint", "run `doze-aws dns-setup` once to alias the loopback pool")
		return nil
	}
	z.extra = ln
	// Publish where the front door should send this name.
	if err := z.lease.Route(addr); err != nil {
		logger.Debug("zone: could not publish the route", "err", err)
	}
	return ln
}

// url is what to tell the user to connect to — port-less when the front door
// is up, with the port when it is not, so the line printed at startup is one
// that actually works.
func (z *zone) url() string {
	if z == nil || z.lease == nil || z.extra == nil {
		return ""
	}
	return z.reg.URLFor(z.lease.Name.Host)
}

// close releases the name and stops serving the zone. The registry would prune
// this process's entries anyway once it exits, so this only makes the name
// available again immediately rather than after the next peer's sweep.
func (z *zone) close() {
	if z == nil {
		return
	}
	if z.extra != nil {
		_ = z.extra.Close()
	}
	if z.lease != nil {
		_ = z.lease.Release()
	}
	if z.srv != nil {
		z.srv.Close()
	}
	z.front.Close()
}

// serveExtra runs srv on an additional listener until it closes.
func serveExtra(srv *http.Server, ln net.Listener, logger *slog.Logger) {
	if ln == nil {
		return
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Debug("zone: extra listener stopped", "err", err)
		}
	}()
}

// runDNSSetup is `doze-aws dns-setup`. The same command exists in doze and
// doze-kafka and does the same work, because the machine setup belongs to the
// zone rather than to any one binary.
func runDNSSetup(args []string) int {
	fs := flag.NewFlagSet("dns-setup", flag.ExitOnError)
	check := fs.Bool("check", false, "report what is set up, without sudo")
	uninstall := fs.Bool("uninstall", false, "remove everything dns-setup installed")
	print := fs.Bool("print", false, "print the privileged script instead of running it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	switch {
	case *check:
		st := names.Check()
		fmt.Printf("%s\n%s", st.Platform, st)
		if !st.OK() {
			fmt.Printf("\nrun `doze-aws dns-setup` to finish. Without it, %s still works —\n"+
				"names are additive, never a replacement.\n", "127.0.0.1:4566")
			return 1
		}
		return 0
	case *uninstall:
		if err := names.Uninstall(names.Options{Print: *print}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	default:
		if err := names.Install(names.Options{Print: *print}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}
