package main

// Adoption of the shared .doze zone.
//
// # Two names, and why
//
// Every instance claims its OWN name — aws.<instance>.doze, where the instance
// defaults to the project directory. That name is what doze-aws is: it is the
// address it answers on, and the suffix every AWS-shaped URL it mints sits
// beneath, so sqs.ap-south-1.aws.harbour.doze and the same host under
// aws.atlas.doze are two different instances that never contend.
//
// On top of that it tries for aws.doze, the machine-wide shorthand. That one
// is first-come and is a CONVENIENCE, not an identity: whoever starts first
// gets it, everyone else simply doesn't, and nothing about an instance depends
// on having it. Minted URLs always use the instance's own name, because that
// is the one that stays right when a second instance appears.
//
// The loopback address each name resolves to is doze-names' business: apex
// names have fixed addresses (aws.doze is 127.0.0.2), qualified ones are
// hashed into a dynamic range, which is what lets two instances bind the same
// port on different addresses.
//
// It also joins the zone as a peer: if no other doze binary is serving DNS,
// this process serves it, answering for every peer's names and not only its
// own. That is what lets a machine with nothing but doze-aws installed still
// resolve .doze — and what lets doze-kafka's names work when doze-aws is the
// one that happens to be running.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/doze-dev/doze-aws/internal/bg"
	names "github.com/doze-dev/doze-names"
)

// apexPort is the port an apex name implies. http://aws.doze has to mean the
// same URL whether a standalone process or a doze stack is behind it, and the
// stack serves it port-less, so standalone binds 80 on its own address rather
// than exposing the configured high port under the name.
const apexPort = 80

// zone is doze-aws's participation in .doze.
type zone struct {
	// own is aws.<instance>.doze — this instance's identity in the zone.
	own *names.Lease
	// apex is aws.doze, the machine-wide shorthand, when this instance got it.
	apex *names.Lease
	// sync holds the sync-prefixed twin of each name above, for Step Functions.
	sync   []*names.Lease
	srv    *names.Server
	front  *names.Ingress
	extras []net.Listener
	reg    *names.Registry
	// held records who owns this instance's name when the claim lost, so
	// startup can name the process instead of reporting a nameless failure.
	held *names.ErrHeld
	// cfgAddr is the --listen address, when one was given — the answer to
	// "then what still works?" below, and empty in the ordinary case.
	cfgAddr string
}

// joinZone claims this instance's names and starts serving the zone if nobody
// else is. It never fails: a machine that has not run dns-setup simply has no
// names, which is a smaller thing than refusing to start — openListeners is
// what decides whether the result is servable.
func joinZone(ctx context.Context, logger *slog.Logger, instance string) *zone {
	z := &zone{}
	reg := names.Open(names.Home(), "doze-aws")
	z.reg = reg

	// The instance's own name. This is the one that matters: losing it means
	// there is no address that is THIS instance, so the holder is kept for the
	// startup message rather than only logged.
	if lease, err := reg.Claim(names.Qualified("aws", instance)); err == nil {
		z.own = lease
		z.claimSync(lease, logger)
	} else if held, ok := names.Held(err); ok {
		z.held = held
		logger.Info("zone: this instance's name is already served",
			"name", held.Host, "held_by_pid", held.PID, "owner", held.Owner)
	} else {
		logger.Debug("zone: could not claim the instance name", "err", err)
	}

	// The shorthand, on top. Not getting it is unremarkable — it means another
	// instance started first — so this is Info once and never an error.
	if lease, err := reg.Claim(names.Apex("aws")); err == nil {
		z.apex = lease
		z.claimSync(lease, logger)
	} else if held, ok := names.Held(err); ok {
		logger.Info("zone: the shorthand aws.doze belongs to another instance",
			"held_by_pid", held.PID, "owner", held.Owner)
	} else {
		logger.Debug("zone: could not claim the shorthand", "err", err)
	}

	// Info, not Debug: these lines are few, they happen at startup, and they are
	// the only warning that a name is not going to work.
	logf := func(format string, args ...any) { logger.Info(fmt.Sprintf(format, args...)) }
	z.srv = names.Serve(ctx, reg, logf)
	// The shared front door is what makes the name port-less. macOS will not
	// let an unprivileged process hold :80 on a specific address, only on the
	// wildcard, so one listener fronts every peer's names by Host header.
	z.front = names.ServeIngress(ctx, reg, logf)
	return z
}

// claimSync claims the sync-prefixed twin of a name, at the same address.
//
// Step Functions' StartSyncExecution and TestState are the two AWS operations
// with a host prefix: every SDK sends them to sync-<endpoint host>, and the
// endpoint ruleset applies the prefix even to a custom endpoint. The prefixed
// host is a SIBLING of the name, not a descendant, so subtree resolution does
// not cover it and it has to be claimed rather than assumed.
func (z *zone) claimSync(l *names.Lease, logger *slog.Logger) {
	n := names.Name{Host: "sync-" + l.Name.Host, Tier: l.Name.Tier}
	if s, err := z.reg.ClaimAt(n, l.IP); err == nil {
		z.sync = append(z.sync, s)
	} else {
		logger.Debug("zone: could not claim the sync- name", "name", n.Host, "err", err)
	}
}

// listenOn returns a listener on one claimed name's own address.
//
// :80 is not attempted here even though the port-less form is the whole point
// of a name: macOS refuses a privileged port on a SPECIFIC address while
// allowing one on the wildcard, so the port-less form comes from the shared
// wildcard front door instead, which routes by Host header the way doze core
// already fronts aws.<stack>.doze.
func (z *zone) listenOn(l *names.Lease, logger *slog.Logger, port string) net.Listener {
	if z == nil || l == nil || port == "" {
		return nil
	}
	addr := net.JoinHostPort(l.IP.String(), port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Both causes leave the name resolving to an address that answers
		// nothing, so say which one it is — the remedies are opposites.
		hint := "run `doze-aws dns-setup` once to alias the loopback pool"
		if errors.Is(err, syscall.EADDRINUSE) {
			hint = "something else already holds " + addr + "; free it, or that name will not work"
		}
		// The name is the primary address now, so there is usually nothing
		// else to fall back to — saying "still works: http://" would be worse
		// than saying nothing. The hint is what matters here.
		attrs := []any{"name", l.Name.Host, "addr", addr, "err", err, "hint", hint}
		if z.cfgAddr != "" {
			attrs = append(attrs, "still_works", "http://"+z.cfgAddr)
		}
		logger.Warn("zone: the name resolves but nothing serves it", attrs...)
		return nil
	}
	z.extras = append(z.extras, ln)
	// Publish where the front door should send this name.
	if err := l.Route(addr); err != nil {
		logger.Debug("zone: could not publish the route", "err", err)
	}
	return ln
}

// urlFor is what to tell the user to connect to for one name — port-less when
// the front door is up, with the port when it is not, so the line printed at
// startup is one that actually works.
func (z *zone) urlFor(l *names.Lease) string {
	if z == nil || l == nil {
		return ""
	}
	// Wait briefly for a front door to appear before deciding how to print the
	// URL. Ours binds in a goroutine that races this line, and a peer's entry
	// may land a moment after we start — losing either race advertises a
	// port-ful URL for a name that is in fact port-less, which is how this read
	// on Linux while :80 was bound the whole time.
	//
	// The wait only runs while the answer is still the pessimistic one, so the
	// common case — already fronted — costs nothing.
	deadline := time.Now().Add(500 * time.Millisecond)
	host := l.Name.Host
	for {
		u := z.reg.URLFor(host)
		if u == "http://"+host || time.Now().After(deadline) {
			return u
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// close releases the name and stops serving the zone. The registry would prune
// this process's entries anyway once it exits, so this only makes the name
// available again immediately rather than after the next peer's sweep.
func (z *zone) close() {
	if z == nil {
		return
	}
	for _, ln := range z.extras {
		_ = ln.Close()
	}
	for _, l := range z.sync {
		_ = l.Release()
	}
	if z.apex != nil {
		_ = z.apex.Release()
	}
	if z.own != nil {
		_ = z.own.Release()
	}
	if z.srv != nil {
		z.srv.Close()
	}
	z.front.Close()
}

// serveOn runs srv on one listener until it closes, reporting a genuine
// failure on errc.
//
// Every listener reports, because none of them is "the extra" any more: a name
// that stops answering is as much a failure as an address that does.
func serveOn(srv *http.Server, ln net.Listener, logger *slog.Logger, errc chan<- error) {
	if ln == nil {
		return
	}
	bg.Go(slogf(logger), "doze-aws: listener "+ln.Addr().String(), func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			select {
			case errc <- err:
			default: // another listener got there first; one report is enough
			}
		}
	})
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

// slogf adapts an *slog.Logger to the logf shape the rest of the tree uses,
// so cmd/ can hand one to internal/bg like any service does.
func slogf(logger *slog.Logger) func(string, ...any) {
	return func(format string, args ...any) {
		logger.Error(fmt.Sprintf(format, args...))
	}
}
