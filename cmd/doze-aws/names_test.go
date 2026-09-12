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
//
// Both zones here share a PID, so this cannot assert the apex CONTENTION — a
// process re-claiming its own name succeeds by design, which is what makes a
// restart work. What it does assert is the part that is doze-aws's own
// decision: which name each instance asks for, and that the answers differ.
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
	// An instance's address must not be the apex's either, or holding the
	// shorthand and holding your own name would be the same bind.
	if apex := net.ParseIP("127.0.0.2"); harbour.own.IP.Equal(apex) || atlas.own.IP.Equal(apex) {
		t.Errorf("an instance name took the apex address %s", apex)
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

// --listen is the escape from both: a held name is no longer fatal when there
// is an address to serve on.
func TestAnAddressServesEvenWhenTheNameIsHeld(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	z := &zone{held: &names.ErrHeld{Host: "aws.harbour.doze", PID: 4242, Owner: "doze-aws"}}

	binds, err := openListeners(cfg, z, quietLogger())
	if err != nil {
		t.Fatalf("--listen must serve regardless of the name: %v", err)
	}
	defer binds.close()
	if len(binds.all) != 1 || binds.primary().what != "address" {
		t.Fatalf("want one address binding, got %+v", binds.all)
	}
	if !strings.HasPrefix(binds.endpoint, "http://127.0.0.1:") {
		t.Errorf("endpoint = %q, want the bound address", binds.endpoint)
	}
}
