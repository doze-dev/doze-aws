package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/config"
	names "github.com/doze-dev/doze-names"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Two instances on one machine is the whole reason names are per-instance.
// "The name is the address" would mean one doze-aws per machine if the name
// were aws.doze; it is aws.<instance>.doze, and each instance gets its own
// loopback address, so both bind the same port and neither notices the other.
func TestTwoInstancesGetTheirOwnNamesAndAddresses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	harbour := joinZone(ctx, quietLogger(), "harbour")
	defer harbour.close()
	atlas := joinZone(ctx, quietLogger(), "atlas")
	defer atlas.close()

	if harbour.own == nil || atlas.own == nil {
		t.Fatalf("both instances must get their own name: harbour=%v atlas=%v", harbour.own, atlas.own)
	}
	if got, want := harbour.own.Name.Host, "aws.harbour.doze"; got != want {
		t.Errorf("harbour answers on %q, want %q", got, want)
	}
	if got, want := atlas.own.Name.Host, "aws.atlas.doze"; got != want {
		t.Errorf("atlas answers on %q, want %q", got, want)
	}
	// The addresses are what actually let both bind :4566. Same address would
	// mean the second instance fails with EADDRINUSE however the names read.
	if harbour.own.IP.Equal(atlas.own.IP) {
		t.Errorf("both instances landed on %s — they cannot both bind a port there", harbour.own.IP)
	}
	// An instance's address must not be the apex's either — 127.0.0.2 is
	// reserved for aws.doze and going into people's /etc/hosts.
	if apex := net.ParseIP("127.0.0.2"); harbour.own.IP.Equal(apex) || atlas.own.IP.Equal(apex) {
		t.Errorf("an instance name took the apex address %s", apex)
	}
}

// aws.doze is never claimed. It used to be taken as a machine-wide shorthand
// for whichever instance started first, which made a URL under it mean a
// different instance depending on boot order — unwritable-down, and quietly
// wrong the day a colleague starts their own project. One way to reach an
// instance, not two.
//
// Asserting it stays unclaimed is the only way this holds: the shorthand was
// four lines, and four lines are easy to add back "as a convenience".
func TestTheMachineWideApexIsNeverClaimed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	z := joinZone(ctx, quietLogger(), "harbour")
	defer z.close()

	for host := range names.Open(names.Home(), "doze-aws").Snapshot() {
		if host == "aws.doze" || host == "sync-aws.doze" {
			t.Errorf("%s was claimed; every instance must be named", host)
		}
	}
	// And nothing is listening for it, which is the part a user would notice.
	binds, err := openListeners(config.Default(), z, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer binds.close()
	for _, b := range binds.all {
		if strings.Contains(b.url, "//aws.doze") {
			t.Errorf("a listener advertises the apex: %+v", b)
		}
	}
}

// Step Functions sends StartSyncExecution to sync-<endpoint host>. That host
// is a SIBLING of the instance name, not a descendant, so subtree resolution
// does not reach it and it has to be claimed alongside.
func TestTheSyncTwinIsClaimedForTheInstanceName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	z := joinZone(ctx, quietLogger(), "harbour")
	defer z.close()

	var hosts []string
	for _, l := range z.sync {
		hosts = append(hosts, l.Name.Host)
	}
	found := false
	for _, h := range hosts {
		if h == "sync-aws.harbour.doze" {
			found = true
		}
	}
	if !found {
		t.Errorf("sync-aws.harbour.doze was not claimed; claimed: %v", hosts)
	}
	// It has to resolve to the SAME address, or the prefixed request reaches
	// nothing even though the name exists.
	for _, l := range z.sync {
		if l.Name.Host == "sync-aws.harbour.doze" && !l.IP.Equal(z.own.IP) {
			t.Errorf("sync twin is at %s, instance is at %s", l.IP, z.own.IP)
		}
	}
}

// Losing your name to another live instance has a different answer from having
// no names at all — run under another name, or take an address — so the two
// cases must not share one message. The remedies are the message: a startup
// failure that only says "cannot listen" leaves the reader to guess.
func TestAHeldNameSaysWhoHasItAndWhatToDo(t *testing.T) {
	z := &zone{held: &names.ErrHeld{Host: "aws.harbour.doze", PID: 4242, Owner: "doze-aws"}}
	_, err := openListeners(config.Default(), z, quietLogger())
	if err == nil {
		t.Fatal("a held name with no --listen must not start")
	}
	for _, want := range []string{"aws.harbour.doze", "4242", "--name", "--listen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the held-name error is missing %q:\n%s", want, err)
		}
	}

	// With no name at all it is a DNS problem, and --name would not help.
	_, err = openListeners(config.Default(), &zone{}, quietLogger())
	if err == nil {
		t.Fatal("no name and no --listen must not start")
	}
	if strings.Contains(err.Error(), "--name") {
		t.Errorf("suggesting --name when no name resolves at all sends the reader the wrong way:\n%s", err)
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("the no-names error should point at doctor:\n%s", err)
	}
}

// With several instances running, every one of them has an "aws." entry in the
// registry, and `apply` picking whichever came back first from a map iteration
// is a coin toss over somebody else's data — a stack converged into the wrong
// project, reported as success. This instance's own name must come first.
func TestApplyLooksForItsOwnInstanceFirst(t *testing.T) {
	reg := names.Open(names.Home(), "doze-aws")

	// Two instances, routed to distinguishable addresses.
	for _, tc := range []struct{ name, target string }{
		{"atlas", "127.0.0.90:4566"},
		{"harbour", "127.0.0.91:4566"},
		{"zenith", "127.0.0.92:4566"},
	} {
		l, err := reg.Claim(names.Qualified("aws", tc.name))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Release() //nolint:errcheck
		if err := l.Route(tc.target); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Name = "harbour"
	got := liveCandidates(cfg)
	if len(got) == 0 {
		t.Fatal("no candidates at all")
	}
	if got[0] != "127.0.0.91:4566" {
		t.Errorf("first candidate = %q, want harbour's own address — %v", got[0], got)
	}
	// The others still get tried, because a single instance that was started
	// under a different name is better found than not.
	if len(got) != 3 {
		t.Errorf("every instance should remain a candidate, got %v", got)
	}
}

// --listen and the name are EXCLUSIVE. With an address given, run() never
// joins the zone at all, so openListeners is handed a nil zone — and must
// serve the address without touching it.
//
// This test used to assert that --listen was the escape from a HELD name,
// passing a zone with held set. That state can no longer occur: nothing claims
// a name under --listen, so nothing can lose one. The nil zone is the real
// contract now.
func TestListenServesTheAddressAndNeverTouchesTheZone(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"

	binds, err := openListeners(cfg, nil, quietLogger())
	if err != nil {
		t.Fatalf("--listen must serve with no zone at all: %v", err)
	}
	defer binds.close()

	if len(binds.all) != 1 {
		t.Fatalf("want exactly one binding, got %+v", binds.all)
	}
	if got := binds.primary().what; got != "address" {
		t.Errorf("binding is %q, want %q", got, "address")
	}
	// binds.endpoint is what a child Lambda gets as AWS_ENDPOINT_URL. It used
	// to be set from the NAME branch first and only fall back to the address,
	// so under --listen a function was handed a .doze URL that resolves to
	// nothing on a machine where dns-setup never ran.
	if !strings.HasPrefix(binds.endpoint, "http://127.0.0.1:") {
		t.Errorf("endpoint = %q, want the bound address — a Lambda child dials this", binds.endpoint)
	}
	if strings.Contains(binds.endpoint, ".doze") {
		t.Errorf("endpoint = %q — a name under --listen resolves to nothing", binds.endpoint)
	}
}

// A taken port is not a startup failure. Under DNS the port is an internal
// detail — the front door serves the name port-less, and the registry carries
// whatever port was actually bound — so refusing to start over one would be
// refusing over something nobody types.
//
// Skips rather than fails where the loopback pool is not aliased: binding
// 127.0.0.x needs `dns-setup` on macOS, and a machine without it cannot
// exercise this at all.
func TestATakenPortTakesAnotherOne(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	z := joinZone(ctx, quietLogger(), "portclash")
	defer z.close()
	if z.own == nil {
		t.Skip("no name claimed")
	}

	// Hold the preferred port on this instance's own address.
	blocker, err := net.Listen("tcp", net.JoinHostPort(z.own.IP.String(), namePort))
	if err != nil {
		t.Skipf("cannot bind %s (loopback pool not aliased?): %v", z.own.IP, err)
	}
	defer blocker.Close() //nolint:errcheck

	ln := z.listenOn(z.own, quietLogger(), namePort)
	if ln == nil {
		t.Fatal("a taken port must not stop the name from being served")
	}
	if _, port, _ := net.SplitHostPort(ln.Addr().String()); port == namePort {
		t.Fatalf("bound the blocked port %s", port)
	}

	// The registry must carry what was actually bound. If it still said :4566
	// the front door would forward the name straight at the blocker.
	got := z.reg.Snapshot()[z.own.Name.Host].Target
	if got != ln.Addr().String() {
		t.Errorf("registry routes %s to %q, but we are listening on %q",
			z.own.Name.Host, got, ln.Addr().String())
	}
}

// The name is claimed only when --listen was NOT given. Asserting on the
// registry rather than on a log line, because the registry is what another
// process reads: a stray entry means `apply` in a sibling directory could pick
// this instance as its target.
func TestListenClaimsNoName(t *testing.T) {
	reg := names.Open(names.Home(), "doze-aws")
	before := len(reg.Snapshot())

	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	binds, err := openListeners(cfg, nil, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer binds.close()

	if after := len(reg.Snapshot()); after != before {
		t.Errorf("registry grew from %d to %d entries under --listen: %v",
			before, after, reg.Snapshot())
	}
}
