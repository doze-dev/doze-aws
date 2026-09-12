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
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/doze-dev/doze-aws/internal/config"
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
		return listeners{}, fmt.Errorf(
			"doze-aws: nothing to listen on — .doze gave no name and no --listen was set.\n" +
				"  doze-aws doctor             show what is missing\n" +
				"  doze-aws --listen host:port serve on an address instead")
	}
	return out, nil
}

// namePort is the port the name's own address binds. It is not the port anyone
// types — the front door serves the name port-less on :80 — but a listener
// needs one, and keeping 4566 means an explicit host:port still works.
const namePort = "4566"

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
