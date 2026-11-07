package main

// Where doze-aws answers: an address, 127.0.0.1:4566 unless --listen says
// otherwise. The standalone binary needs nothing set up on the machine, takes
// no part in .doze names, and writes nothing outside its data directory.
//
// Names are doze's. Run as a doze module, the same stack answers at its
// instance's name, aws.<stack>.doze, and every AWS-shaped host under it
// (sqs.<region>.aws.<stack>.doze, <bucket>.s3.<region>.aws.<stack>.doze), and
// at sync-aws.<stack>.doze for Step Functions' sync calls; doze gives it that
// name (see module/). Standalone, AWS-shaped hostnames need a name someone
// else owns: --suffix, the proxy-in-front case. Without one the URLs doze-aws
// hands back use path shapes, and an SDK needs path-style S3 and, for Step
// Functions' StartSyncExecution and TestState, its host prefix off
// (docs/endpoints.md).

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/doze-dev/doze-aws/internal/config"
)

// binding is the address doze-aws answers on.
type binding struct {
	ln  net.Listener
	url string // what to tell a user to connect to
}

// listeners is everything doze-aws is answering on.
type listeners struct {
	all []binding
	// endpoint is the URL a child process (a Lambda function) should dial.
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

// openListeners binds the configured address. The error from net.Listen is
// returned verbatim — "address already in use" on an address the person can
// see needs no interpretation; --listen picks another.
func openListeners(cfg config.Config) (listeners, error) {
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return listeners{}, err
	}
	url := "http://" + reachableHost(ln.Addr().String())
	return listeners{all: []binding{{ln: ln, url: url}}, endpoint: url}, nil
}

// instanceAddress is where a configuration says this instance answers, and the
// suffix its AWS-shaped hostnames sit under (only an explicit --suffix). The
// commands that do not run the server, env and doctor, use it so they give the
// answer the server does.
func instanceAddress(cfg config.Config) (url, suffix string) {
	return "http://" + reachableHost(cfg.ListenAddr), cfg.Suffix
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
