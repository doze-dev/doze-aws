package main

// What the banner is allowed to advertise.
//
// The endpoint line is the first thing anyone reads and the thing they paste
// into a config, so a URL there that nothing can reach is worse than no banner
// at all. It happened: a container sets its names up successfully — the hosts
// block goes in, apex names resolve — and the per-instance name still does not
// resolve, because routing one domain needs systemd-resolved or dnsmasq and a
// slim image has neither. The banner said http://aws.work.doze and nothing on
// that machine could dial it.

import (
	"log/slog"
	"net"
	"strings"
	"testing"
)

func testBinding(t *testing.T, what, url string) binding {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return binding{ln: ln, what: what, url: url}
}

// .invalid is reserved by RFC 2606 precisely so that it never resolves, which
// makes it the one hostname this can rely on failing everywhere.
func TestAnUnresolvableNameIsAdvertisedAsItsAddress(t *testing.T) {
	b := testBinding(t, "name", "http://aws.nowhere.invalid")

	got := b.advertise(quietLogger())
	if got == b.url {
		t.Fatalf("advertised %s, a name that cannot resolve.\n"+
			"  The endpoint line is what people paste. A URL nothing can dial "+
			"is worse than a bare\n  address, which is always dialable.", got)
	}
	want := "http://" + b.ln.Addr().String()
	if got != want {
		t.Errorf("advertised %q, want the listener's own address %q", got, want)
	}
}

// The common case, and the one a regression would break silently: a machine
// where dns-setup has run keeps the name, because the name is the whole point
// — it is what makes the URLs doze-aws hands back AWS-shaped.
func TestAResolvableNameIsKept(t *testing.T) {
	b := testBinding(t, "name", "http://localhost")

	if got := b.advertise(quietLogger()); got != b.url {
		t.Errorf("advertised %q for a name that resolves, want %q.\n"+
			"  Falling back here would cost every instance its AWS-shaped "+
			"hostnames.", got, b.url)
	}
}

// --listen was the caller saying which address to serve on. Looking it up
// would be asking whether they meant it.
func TestAnAddressIsNeverLookedUp(t *testing.T) {
	b := testBinding(t, "address", "http://198.51.100.9:4566")

	if got := b.advertise(quietLogger()); got != b.url {
		t.Errorf("advertised %q, want the address the caller gave: %q", got, b.url)
	}
}

// The fallback is not silent: it names the address to use instead, and points
// at the command that explains the rest.
func TestTheFallbackSaysWhatToUseInstead(t *testing.T) {
	var sb strings.Builder
	logger := slog.New(slog.NewTextHandler(&sb, nil))
	b := testBinding(t, "name", "http://aws.nowhere.invalid")

	addr := b.advertise(logger)
	line := sb.String()
	for _, want := range []string{"does not resolve", "aws.nowhere.invalid", addr, "doze-aws doctor"} {
		if !strings.Contains(line, want) {
			t.Errorf("the fallback log line does not mention %q:\n  %s", want, line)
		}
	}
}
