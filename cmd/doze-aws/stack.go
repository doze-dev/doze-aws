package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/internal/config"
	"github.com/doze-dev/doze-aws/internal/provision"
)

// splitApplyArgs separates `apply` arguments into the stack file, the --var
// overrides, and the pass-through server flags. A `--flag value` pair owns its
// next token (mirroring the flag package) so the value is never mistaken for
// the stack-file argument.
func splitApplyArgs(args []string) (file string, vars map[string]string, flags []string, err error) {
	vars = map[string]string{}
	setVar := func(kv string) error {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--var needs name=value, got %q", kv)
		}
		vars[k] = v
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--var" || a == "-var":
			if i+1 >= len(args) {
				return "", nil, nil, fmt.Errorf("--var needs name=value")
			}
			i++
			if err := setVar(args[i]); err != nil {
				return "", nil, nil, err
			}
		case strings.HasPrefix(a, "--var="), strings.HasPrefix(a, "-var="):
			if err := setVar(a[strings.Index(a, "=")+1:]); err != nil {
				return "", nil, nil, err
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			flags = append(flags, a)
			name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if !hasValue && flagTakesValue(name) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case file == "" && a != "":
			file = a
		default:
			return "", nil, nil, fmt.Errorf("unexpected argument %q (the stack file is already %q)", a, file)
		}
	}
	return file, vars, flags, nil
}

// flagTakesValue reports whether the named server flag consumes a separate
// value argument — i.e. it is registered and not a boolean flag.
func flagTakesValue(name string) bool {
	probe := config.Default()
	fs, _ := newFlagSet(&probe)
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !b.IsBoolFlag()
}

// runApply implements `doze-aws apply [--var k=v ...] [stack.yaml]`.
func runApply(args []string) int {
	file, vars, flags, err := splitApplyArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 2
	}
	if file == "" {
		for _, candidate := range config.DefaultTemplateFiles {
			if _, err := os.Stat(candidate); err == nil {
				file = candidate
				break
			}
		}
	}
	if file == "" {
		fmt.Fprintf(os.Stderr, "apply: no template given and none of %s found\n",
			strings.Join(config.DefaultTemplateFiles, ", "))
		return 2
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
	s, cfnRep, err := loadTemplate(data, file, vars)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	printTranspileReport(cfnRep)
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
	rep, err := provision.Apply(context.Background(), gw, s)
	printReport(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply:", err)
		return 1
	}
	c, u, k := rep.Counts()
	fmt.Fprintf(os.Stderr, "✓ converged: %d created · %d updated · %d already in place\n", c, u, k)
	if cfnRep != nil && len(cfnRep.Outputs) > 0 {
		fmt.Fprintln(os.Stderr, "\nOutputs:")
		for _, name := range sortedKeys(cfnRep.Outputs) {
			fmt.Fprintf(os.Stderr, "  %s = %s\n", name, cfnRep.Outputs[name])
		}
	}
	return 0
}

// loadTemplate parses a CloudFormation or SAM template into the resource graph
// apply converges. doze-aws once had its own stack.yaml dialect; it was
// removed, because a format only doze-aws speaks is one nobody wants to learn.
func loadTemplate(data []byte, file string, vars map[string]string) (*provision.Stack, *cloudformation.Report, error) {
	tmpl, err := cloudformation.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	// --var doubles as the template-parameter channel.
	s, rep, err := cloudformation.Transpile(tmpl, cloudformation.TranspileOptions{
		StackName:  name,
		Parameters: vars,
	})
	if err != nil {
		return nil, rep, err
	}
	return s, rep, nil
}

// printTranspileReport shows what the template mapped to — and, critically,
// what it did not. A silently skipped resource is the failure mode the whole
// transpiler is designed to avoid, so ignored entries are always printed.
func printTranspileReport(rep *cloudformation.Report) {
	if rep == nil {
		return
	}
	kind := "CloudFormation"
	if rep.SAM {
		kind = "SAM"
	}
	mapped, ignored, rejected := rep.Counts()
	fmt.Fprintf(os.Stderr, "%s template: %d resources mapped, %d skipped, %d unsupported\n",
		kind, mapped, ignored, rejected)
	for _, e := range rep.Entries {
		switch e.Kind {
		case cloudformation.Ignored:
			fmt.Fprintf(os.Stderr, "  ≈ %s (%s) — %s\n", e.LogicalID, e.Type, e.Reason)
		case cloudformation.Rejected:
			fmt.Fprintf(os.Stderr, "  ✗ %s (%s) — %s\n", e.LogicalID, e.Type, e.Reason)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	s, err := provision.Export(context.Background(), gw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	// The running stack comes back as a CloudFormation template, so what you
	// export is something the rest of your tooling can read.
	out, err := cloudformation.Emit(s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	os.Stdout.Write(out)
	return 0
}

func printReport(rep *provision.Report) {
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
