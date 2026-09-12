package main

// Adoption of the shared .doze zone.
//
// # One name per instance, and nothing else
//
// Every instance claims aws.<instance>.doze, where the instance defaults to
// the project directory. That name is what doze-aws is: it is the address it
// answers on, and the suffix every AWS-shaped URL it mints sits beneath, so
// sqs.ap-south-1.aws.harbour.doze and the same host under aws.atlas.doze are
// two different instances that never contend.
//
// aws.doze — the machine-wide apex — is deliberately NOT claimed. It existed
// as a shorthand for whichever instance started first, and that is the kind of
// choice that costs more than it gives: a URL under it means a different
// instance depending on boot order, so it cannot be written down, cannot be
// put in a config file, and quietly points somewhere else the day a colleague
// starts their own project. One rule is better than two. If you want a short
// name, name the instance something short.
//
// The loopback address a name resolves to is doze-names' business: qualified
// names are hashed into a dynamic range, which is what lets two instances bind
// the same port on different addresses.
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

// zone is doze-aws's participation in .doze.
type zone struct {
	// own is aws.<instance>.doze — this instance's identity in the zone, and
	// the only name it claims.
	own *names.Lease
	// sync holds the sync-prefixed twin of that name, for Step Functions.
	sync   []*names.Lease
	srv    *names.Server
	front  *names.Ingress
	extras []net.Listener
	reg    *names.Registry
	// held records who owns this instance's name when the claim lost, so
	// startup can name the process instead of reporting a nameless failure.
	held *names.ErrHeld
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
	if errors.Is(err, syscall.EADDRINUSE) {
		// The port is an internal detail. Nobody types it — the front door
		// serves the name port-less, and URLFor reads whatever port we publish
		// back out of the registry — so something else holding it is no reason
		// to refuse to start. Take any free port instead.
		//
		// Each instance has its own loopback address, so this is rare: it means
		// something else on this machine bound THIS address and port, not merely
		// that another doze-aws is running.
		var reerr error
		if ln, reerr = net.Listen("tcp", net.JoinHostPort(l.IP.String(), "0")); reerr == nil {
			logger.Info("zone: the preferred port was taken, took another",
				"name", l.Name.Host, "wanted", addr, "using", ln.Addr().String())
			addr, err = ln.Addr().String(), nil
		}
	}
	if err != nil {
		// Both remaining causes leave the name resolving to an address that
		// answers nothing, so say which one it is — the remedies are opposites.
		hint := "run `doze-aws dns-setup` once to alias the loopback pool"
		if errors.Is(err, syscall.EADDRINUSE) {
			hint = "something else already holds " + addr + "; free it, or that name will not work"
		}
		// There is nothing to fall back to. The zone is only joined when
		// --listen was NOT given, so a name that cannot bind means this
		// instance has no address at all — the hint is the whole message.
		logger.Warn("zone: the name resolves but nothing serves it",
			"name", l.Name.Host, "addr", addr, "err", err, "hint", hint)
		return nil
	}
	z.extras = append(z.extras, ln)
	// Publish where the front door should send this name — the address actually
	// bound, not the one asked for, or a fallback port would route nowhere.
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
			// This used to say "127.0.0.1:4566 still works — names are
			// additive, never a replacement". Both halves are now false, and
			// this is the DIAGNOSTIC command: it is read by someone already
			// stuck, who would have pointed an SDK at an address nothing binds
			// and concluded the tool was lying to them. It was.
			fmt.Print("\nrun `doze-aws dns-setup` to finish, or serve on an address instead:\n" +
				"  doze-aws --listen 127.0.0.1:4566\n")
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
