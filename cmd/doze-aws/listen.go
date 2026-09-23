package main

// Where doze-aws answers.
//
// # The inversion
//
// doze-aws used to bind 127.0.0.1:4566 and treat a .doze name as an extra —
// "additive, never a replacement", as the code said. That is now the other way
// round: the NAME is what doze-aws is, and an address is the opt-in.
//
// The name is per-INSTANCE — aws.harbour.doze, aws.atlas.doze — so "the name
// is the address" does not mean one doze-aws per machine. Each instance gets
// its own loopback address from doze-names and binds the same port on it.
// There is no unnamed instance and no machine-wide aws.doze: one way to reach
// an instance by name, not two.
//
// The reason is that an address cannot carry what a name carries. AWS puts the
// service and the region in the hostname —
// sqs.ap-south-1.amazonaws.com — and doze-aws mints the URLs it hands back
// from the host a request arrived on. Reached at an address, the best it can
// report is that address; reached at a name, it reports AWS's own shape with
// the suffix swapped. One address cannot be seventeen services in as many
// regions, and every workaround for that is a path shape AWS does not use.
//
// # --listen is not a fallback, and not an addition
//
// It is for the case a name genuinely cannot serve: a sibling container
// reaching this one over a compose network, where the address comes from
// Docker and .doze is not in play.
//
// It REPLACES the name rather than adding to it. An instance that answered on
// both would hand back two different URL shapes depending on which address you
// asked through, and would have to pick one of them for AWS_ENDPOINT_URL —
// which it did, badly: it preferred the name, so a Lambda child under --listen
// on a machine with no dns-setup was handed a URL that resolved to nothing.
// One instance, one way to reach it.
//
// --suffix still applies under --listen, and that is the containerised-behind-
// a-proxy case: the proxy owns the name, doze-aws owns the address.
//
// # What this costs
//
// 127.0.0.1:4566 is no longer bound by default, and docs/endpoints.md
// published it as a promise. That is withdrawn deliberately: it was the
// LocalStack drop-in, and doze-aws is not chasing LocalStack's users.
// `doze-aws --listen 127.0.0.1:4566` restores it exactly for anyone who wants
// it back.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/doze-dev/doze-aws/internal/config"
	names "github.com/doze-dev/doze-names"
)

// binding is one address doze-aws answers on, with how to describe it.
type binding struct {
	ln   net.Listener
	what string // "name" or "address", for the log line
	url  string // what to tell a user to connect to
}

// listeners is everything doze-aws is answering on.
type listeners struct {
	all []binding
	// endpoint is the URL a child process should dial: the name when there is
	// one, since that is the address that stays right if the port moves.
	endpoint string
}

func (l listeners) close() {
	for _, b := range l.all {
		_ = b.ln.Close()
	}
}

// primary is the address the console link and the "listening" line use.
func (l listeners) primary() binding { return l.all[0] }

// advertise returns the URL to put in front of a person: this binding's own,
// unless it is a name that does not resolve here, in which case the address it
// is actually listening on.
//
// # Why this asks DNS instead of asking the setup
//
// The setup knows a lot about whether names OUGHT to resolve — which resolver
// manager is present, whether the route is installed, whether the platform can
// do split DNS at all — and every one of those is a proxy for the thing that
// actually matters. A container makes the difference obvious: the install
// succeeds, apex names go into /etc/hosts and work, and
// aws.<project>.doze still does not resolve because a slim image has neither
// systemd-resolved nor dnsmasq. Reasoning from capability, the banner printed
// a URL nothing could reach; resolving it answers the question directly.
//
// This runs once, after the name is registered and the listener is up, and it
// is the last thing before the banner. A lookup of a name this machine is
// meant to serve goes to the local resolver and returns in microseconds; the
// timeout is there for the case where resolution is broken in a way that
// hangs, because a server that is already listening must not wait on DNS to
// say so.
// lookupFunc is net.Resolver.LookupHost, injected so the comparison below can
// be tested without arranging DNS. Same reason dnsready.go injects its check.
type lookupFunc func(context.Context, string) ([]string, error)

func (b binding) advertise(logger *slog.Logger) string {
	return b.advertiseWith(logger, net.DefaultResolver.LookupHost)
}

func (b binding) advertiseWith(logger *slog.Logger, lookup lookupFunc) string {
	if b.what != "name" {
		return b.url
	}
	host := b.url
	if u, err := url.Parse(b.url); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	// Resolving is not enough: it has to resolve HERE.
	//
	// A container inherits its host's DNS — Docker Desktop and Colima both
	// forward to it — so a developer running doze on their laptop and again in
	// a container gets an answer for aws.<project>.doze from the LAPTOP's
	// registry, naming a loopback address that means something else inside the
	// container. It happened while testing this: the name answered, and only
	// worked because both instances had been handed the same address.
	//
	// So the answer has to name the address this listener is on. Same lookup,
	// one comparison, and the check stops depending on a coincidence.
	mine, _, err := net.SplitHostPort(b.ln.Addr().String())
	if err != nil {
		return b.url
	}
	if resolvesHere(lookup, host, mine) {
		return b.url
	}
	addr := "http://" + b.ln.Addr().String()
	// Said once, plainly: the name is still registered and still correct on a
	// machine that can route the domain — it is this machine that cannot.
	logger.Info("the instance name does not resolve here, so the address is what to use",
		"name", host, "url", addr, "fix", "doze-aws doctor")
	return addr
}

// resolveWait bounds the lookup above. Generous for a local resolver and short
// enough that nobody notices it on a machine where the name is simply absent.
const resolveWait = 750 * time.Millisecond

// resolvesHere reports whether host resolves, on this machine, to wantIP.
//
// Both halves matter and the second is the one that is easy to skip. A
// container inherits its host's DNS, so an answer can come from another
// machine's registry and name an address that means something else here.
func resolvesHere(lookup lookupFunc, host, wantIP string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), resolveWait)
	defer cancel()
	addrs, err := lookup(ctx, host)
	return err == nil && slices.Contains(addrs, wantIP)
}

// serve starts every listener on srv.
func (l listeners) serve(srv *http.Server, logger *slog.Logger, errc chan<- error) {
	for _, b := range l.all {
		serveOn(srv, b.ln, logger, errc)
	}
}

// openListeners binds what the configuration asks for.
//
// Exactly one of the two arms runs. A nil zone means --listen was given, which
// is the caller's way of saying names are not in play at all — see run() in
// main.go, which is the only place that decision is made.
func openListeners(cfg config.Config, z *zone, logger *slog.Logger) (listeners, error) {
	var out listeners

	// --listen: this address, and nothing else. The error from net.Listen is
	// returned verbatim rather than reworded — "address already in use" on an
	// address the caller typed needs no interpretation from us.
	if cfg.ListenAddr != "" {
		ln, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return listeners{}, err
		}
		url := "http://" + reachableHost(ln.Addr().String())
		out.all = append(out.all, binding{ln: ln, what: "address", url: url})
		out.endpoint = url
		return out, nil
	}

	// The name's own loopback address. The port stays 4566 so a client that
	// wants to be explicit still can; the port-less form comes from the shared
	// :80 front door, which joinZone already runs.
	if ln := z.listenOn(z.own, logger, namePort); ln != nil {
		url := z.urlFor(z.own)
		if url == "" {
			url = "http://" + ln.Addr().String()
		}
		out.all = append(out.all, binding{ln: ln, what: "name", url: url})
		out.endpoint = url
	}

	if len(out.all) == 0 {
		// Losing the name to another live instance is a different problem from
		// having no names at all, and it has a different answer — so say which
		// one happened rather than one message for both.
		if z != nil && z.held != nil {
			return listeners{}, fmt.Errorf(
				"doze-aws: %s is already served by pid %d (%s).\n"+
					"  doze-aws --name <other>     run this instance under its own name\n"+
					"  doze-aws --listen host:port serve on an address instead",
				z.held.Host, z.held.PID, z.held.Owner)
		}
		// ensureNames already explained the DNS half; this is the other way in.
		return listeners{}, errNothingToListenOn
	}
	return out, nil
}

// errNothingToListenOn is "this machine offered no name and no --listen was
// set" — a statement about the machine, not a failure of openListeners.
//
// A sentinel because a test needs to tell it apart from a real error. On a
// machine without .doze there are no listeners to assert anything about, and
// a CI runner is exactly that machine: the macOS leg failed on it for four
// days while the Linux leg, whose runner can write /etc/hosts as root, passed.
var errNothingToListenOn = errors.New(
	"doze-aws: nothing to listen on — .doze gave no name and no --listen was set.\n" +
		"  doze-aws doctor             show what is missing\n" +
		"  doze-aws --listen host:port serve on an address instead")

// namePort is the port the name's own address binds. It is not the port anyone
// types — the front door serves the name port-less on :80 — but a listener
// needs one, and keeping 4566 means an explicit host:port still works.
const namePort = "4566"

// instanceAddress is where a configuration says this instance answers, and the
// suffix its AWS-shaped hostnames sit under.
//
// It exists so the commands that do NOT run the server — env, doctor — give
// the same answer the server does. They used to work it out separately, and
// the separate answers were wrong in two ways at once: `doze-aws env` derived
// its endpoint from --listen alone, which now defaults to empty, so it printed
// `export AWS_ENDPOINT_URL=` and sent every SDK call to real AWS; and it never
// saw the suffix, because the suffix is derived during startup, so it always
// claimed AWS-shaped hostnames were unavailable.
//
// The registry is asked rather than DNS, and rather than assuming the port:
// a running instance publishes the address it actually bound (which may not be
// 4566 — see listenOn), and URLFor gives the port-less form when the shared
// front door is up. With nothing running there is nothing to ask, so the answer
// is the name the instance WILL claim, which is the useful thing to print.
func instanceAddress(cfg config.Config) (url, suffix string) {
	if cfg.ListenAddr != "" {
		// An address claims no name, so there is no derived suffix — only an
		// explicit --suffix, which is the behind-a-proxy case.
		return "http://" + reachableHost(cfg.ListenAddr), cfg.Suffix
	}
	host := names.Qualified("aws", cfg.InstanceName()).Host
	suffix = cfg.Suffix
	if suffix == "" {
		suffix = host
	}
	reg := names.Open(names.Home(), "doze-aws")
	if u := reg.URLFor(host); u != "" {
		// The same question the banner asks, asked by a different process.
		//
		// `doze-aws env` runs on its own, so it cannot look at the server's
		// listener — but the registry records where the instance is, which is
		// the same fact. Without this the banner correctly printed an address
		// while the command it tells you to run next exported the NAME, and
		// `eval "$(doze-aws env)"` in a container pointed every SDK at a
		// hostname that does not resolve. The banner was honest and the shell
		// was not.
		if e, ok := reg.Snapshot()[host]; ok && e.Target != "" {
			if ip, _, err := net.SplitHostPort(e.Target); err == nil &&
				!resolvesHere(net.DefaultResolver.LookupHost, host, ip) {
				// An explicit --suffix belongs to whoever set it — the proxy
				// case — so it survives. A suffix DERIVED from the instance
				// name does not: the per-service hostnames under it are the
				// same name that just failed to resolve, and printing
				// seventeen of them would be seventeen more dead endpoints.
				if cfg.Suffix != "" {
					return "http://" + e.Target, cfg.Suffix
				}
				return "http://" + e.Target, ""
			}
		}
		return u, suffix
	}
	return "http://" + host, suffix
}

// reachableHost turns a bind address into one a child process can dial: a
// wildcard or empty host becomes loopback, since "0.0.0.0" is an address to
// listen on rather than one to connect to.
func reachableHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
