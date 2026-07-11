package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/config"
	"github.com/doze-dev/doze-aws/stackfile"
)

// runApply implements `doze-aws apply [--var k=v ...] [stack.yaml]`.
func runApply(args []string) int {
	var file string
	var flags []string
	vars := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--var" || a == "-var":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "apply: --var needs name=value")
				return 2
			}
			i++
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				fmt.Fprintln(os.Stderr, "apply: --var needs name=value, got", args[i])
				return 2
			}
			vars[k] = v
		case strings.HasPrefix(a, "--var="):
			k, v, ok := strings.Cut(strings.TrimPrefix(a, "--var="), "=")
			if !ok {
				fmt.Fprintln(os.Stderr, "apply: --var needs name=value, got", a)
				return 2
			}
			vars[k] = v
		case len(a) > 0 && a[0] != '-' && file == "":
			file = a
		default:
			flags = append(flags, a)
		}
	}
	if file == "" {
		file = config.DefaultStackFile
	}
	st, err := loadConfig(flags)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	s, err := stackfile.ParseWithVars(data, vars)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	gw, closer, live, err := gatewayFor(st.cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	defer closer()
	if live {
		fmt.Fprintf(os.Stderr, "applying %s to the running server at %s\n", file, st.cfg.ListenAddr)
	} else {
		fmt.Fprintf(os.Stderr, "applying %s to the data dir at %s (no server running)\n", file, st.cfg.DataDir)
	}
	rep, err := stackfile.Apply(context.Background(), gw, s)
	printReport(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	c, u, k := rep.Counts()
	fmt.Fprintf(os.Stderr, "✓ converged: %d created · %d updated · %d already in place\n", c, u, k)
	return 0
}

// runExport implements `doze-aws export` — the running stack, as a stack.yaml.
func runExport(args []string) int {
	st, err := loadConfig(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	gw, closer, _, err := gatewayFor(st.cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	defer closer()
	s, err := stackfile.Export(context.Background(), gw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	out, err := stackfile.Marshal(s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	os.Stdout.Write(out)
	return 0
}

func printReport(rep *stackfile.Report) {
	if rep == nil {
		return
	}
	for _, a := range rep.Actions {
		mark := map[string]string{"created": "+", "updated": "~", "skipped": "="}[a.Op]
		if mark == "" {
			mark = "?"
		}
		line := fmt.Sprintf("  %s %s", mark, a.Resource)
		if a.Detail != "" {
			line += "  (" + a.Detail + ")"
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

// gatewayFor returns an http.Handler for the stack: the RUNNING server when
// one is listening (bbolt is single-writer, so the data dir can't be opened
// alongside it), otherwise a stack booted from the data dir.
func gatewayFor(cfg config.Config) (h http.Handler, closer func(), live bool, err error) {
	if conn, derr := net.DialTimeout("tcp", cfg.ListenAddr, 400*time.Millisecond); derr == nil {
		conn.Close()
		return proxyHandler{base: "http://" + cfg.ListenAddr}, func() {}, true, nil
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir: cfg.DataDir, Services: cfg.Services, S3Host: cfg.S3Host,
	})
	if err != nil {
		return nil, nil, false, err
	}
	return stack.Handler(), func() { stack.Close() }, false, nil
}

// proxyHandler adapts a base URL to http.Handler so stackfile's in-process
// client can converge a live server over loopback.
type proxyHandler struct{ base string }

func (p proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, p.base+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
