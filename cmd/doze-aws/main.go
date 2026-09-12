// Command doze-aws serves local, from-scratch emulations of the AWS services a
// development stack leans on — one shared endpoint, real wire protocols, both
// AWS SDK generations, no Docker, no JVM.
//
// Zero-config: `doze-aws` listens on 127.0.0.1:4566 (the port LocalStack
// standardized, so existing AWS_ENDPOINT_URL setups work unchanged) and stores
// data under ./data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/console"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/config"
	"github.com/doze-dev/doze-aws/peers"
	"github.com/doze-dev/doze-aws/provision"
)

// version is the build version, injected by the release tooling
// (-ldflags "-X main.version=..."). It defaults to "dev" for local builds.
var version = "dev"

// commands are the subcommands, in the order help lists them. Each takes the
// arguments after its own name and returns an exit code.
//
// A table rather than a chain of positional ifs. The chain FELL THROUGH to
// booting the server for anything it did not recognise, so `doze-aws help`
// silently bound :4566 and served AWS instead of printing help — and a typo
// like `doze-aws aply` did the same. An unrecognised subcommand is an error
// now, and bare `doze-aws` with flags is still the way you start it.
type command struct {
	name string
	desc string
	run  func(args []string) int
}

// A function, not a package-level var: help both appears in the table and
// prints it, which as a var is an initialisation cycle.
func commands() []command {
	return []command{
		{"apply", "converge the resources a CloudFormation/SAM template declares", runApply},
		{"export", "write what is running as a CloudFormation template", runExport},
		{"config print", "print the effective configuration as TOML, ready to edit", runConfigPrint},
		{"dns-setup", "prepare this machine for .doze names (idempotent)", runDNSSetup},
		{"version", "print the version and the services this build serves", runVersion},
		{"help", "print this message", runHelp},
	}
}

// runHelp is a named function rather than a closure in the table: a closure
// calling usage, which ranges over the table, is an initialisation cycle.
func runHelp([]string) int { usage(os.Stdout); return 0 }

func runVersion([]string) int {
	fmt.Printf("doze-aws %s — local AWS services: %s\n",
		version, strings.Join(dozeaws.Implemented, ", "))
	return 0
}

// runConfigPrint writes the effective configuration — defaults, then the
// config file, then flags — as TOML.
func runConfigPrint(args []string) int {
	st, err := loadConfig(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := config.WriteTOML(os.Stdout, st.cfg); err != nil {
		fmt.Fprintln(os.Stderr, "config print:", err)
		return 1
	}
	return 0
}

// usage prints the commands AND the flags. The flags alone were all `--help`
// showed, so five subcommands documented in docs/cli.md were invisible to
// anyone who looked in the obvious place.
func usage(w *os.File) {
	fmt.Fprintf(w, `doze-aws %s — local AWS services, one binary.

usage:
  doze-aws [flags]              start the emulator (the default)
  doze-aws <command> [args]

commands:
`, version)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-14s %s\n", c.name, c.desc)
	}
	fmt.Fprint(w, "\nflags:\n")
	fs, _ := newFlagSet(&config.Config{})
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprint(w, "\nExamples:\n"+
		"  doze-aws                                  everything, on 127.0.0.1:4566\n"+
		"  doze-aws --services s3,sqs,lambda         just those three\n"+
		"  doze-aws --data-dir /tmp/doze             a throwaway data directory\n"+
		"  doze-aws apply template.yaml              deploy a template into a running stack\n"+
		"\nDocs: https://github.com/doze-dev/doze-aws/tree/main/docs\n")
}

// dispatch runs a subcommand if args names one. ok is false when this is a
// plain `doze-aws [flags]` start.
func dispatch(args []string) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "-h", "--help", "-help":
		usage(os.Stdout)
		return 0, true
	}
	for _, c := range commands() {
		// "config print" is two words; the rest are one.
		name, sub, twoWord := strings.Cut(c.name, " ")
		if args[0] != name {
			continue
		}
		if twoWord {
			if len(args) < 2 || args[1] != sub {
				fmt.Fprintf(os.Stderr, "usage: doze-aws %s [flags]\n", c.name)
				return 2, true
			}
			return c.run(args[2:]), true
		}
		return c.run(args[1:]), true
	}
	// A bare flag is a start, not a typo. Anything else is a typo, and saying
	// so beats starting a server the caller did not ask for.
	if strings.HasPrefix(args[0], "-") {
		return 0, false
	}
	fmt.Fprintf(os.Stderr, "doze-aws: unknown command %q\n\n", args[0])
	usage(os.Stderr)
	return 2, true
}

func main() {
	if code, handled := dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}

	st, err := loadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("doze-aws", "version", version)
	if st.configFile != "" {
		logger.Info("loaded config file", "path", st.configFile)
	}

	if err := run(st.cfg, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// startup holds everything resolved from the command line before serving.
type startup struct {
	cfg        config.Config
	configFile string // the config file actually loaded, or "" if none.
}

// loadConfig resolves the effective configuration with flags > file > defaults
// precedence. It parses once to discover --config (falling back to
// ./doze-aws.toml if present), overlays that file onto the defaults, then
// parses the flags again on top so any flag still wins.
func loadConfig(args []string) (startup, error) {
	probe := config.Default()
	configPath := parseFlags(args, &probe)
	if configPath == "" {
		if _, err := os.Stat(config.DefaultConfigFile); err == nil {
			configPath = config.DefaultConfigFile
		}
	}

	c := config.Default()
	if configPath != "" {
		if err := config.LoadFile(configPath, &c); err != nil {
			return startup{}, err
		}
	}
	parseFlags(args, &c)
	return startup{cfg: c, configFile: configPath}, nil
}

// newFlagSet binds the flags onto dst and returns the set plus the --config
// value. Because the flags' defaults are dst's current field values, keys
// already set from a config file survive unless the flag is explicitly passed.
func newFlagSet(dst *config.Config) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("doze-aws", flag.ExitOnError)
	cp := fs.String("config", "", "path to a TOML config file (default: ./doze-aws.toml if present)")
	fs.StringVar(&dst.ListenAddr, "listen", dst.ListenAddr, "host:port for the shared endpoint")
	fs.StringVar(&dst.DataDir, "data-dir", dst.DataDir, "root directory for service data")
	fs.Var(servicesFlag{&dst.Services}, "services", "comma-separated services to enable (default: all implemented)")
	fs.StringVar(&dst.S3Host, "s3-host", dst.S3Host, "base host for virtual-hosted-style S3 bucket addressing")
	fs.StringVar(&dst.AccountID, "account-id", dst.AccountID, "twelve-digit account id every ARN carries (default 000000000000; set at creation, hard to change later)")
	fs.StringVar(&dst.Region, "region", dst.Region, "region this instance serves; its data lives under <data-dir>/<region> (default us-east-1)")
	fs.BoolVar(&dst.Console, "console", dst.Console, "serve the web management console at /_console")
	fs.DurationVar(&dst.LambdaIdleTimeout, "lambda-idle", dst.LambdaIdleTimeout, "how long a warm Lambda keeps its process before scaling to zero")
	fs.BoolVar(&dst.LambdaQuiet, "lambda-quiet", dst.LambdaQuiet, "do not echo Lambda function output to this log")
	fs.StringVar(&dst.TemplateFile, "template", dst.TemplateFile, "CloudFormation/SAM template to apply at boot (default: ./template.yaml if present)")
	fs.StringVar(&dst.IAMMode, "iam-mode", dst.IAMMode, "IAM enforcement: soft (default: evaluate and record, never block), off (no evaluation), enforce (deny for real)")
	return fs, cp
}

// parseFlags binds the flags onto dst and parses args, returning the --config path.
func parseFlags(args []string, dst *config.Config) (configPath string) {
	fs, cp := newFlagSet(dst)
	fs.Parse(args) //nolint:errcheck // flag.ExitOnError exits on a parse error.
	return *cp
}

// run builds the stack and serves until interrupted.
func run(cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	iamMode, err := iam.ParseMode(cfg.IAMMode)
	if err != nil {
		return err
	}

	// A data directory written before regions keeps its services directly under
	// the root; they belong under <region>/ now. This is a directory rename per
	// service and it happens once, but it happens to somebody's data — so it is
	// announced before it runs rather than discovered afterwards.
	if dozeaws.NeedsMigration(cfg.DataDir) {
		plan := dozeaws.PlanMigration(cfg.DataDir, cfg.Identity().RegionName())
		for _, line := range plan.Describe() {
			logger.Info(line)
		}
		if _, err := dozeaws.Migrate(cfg.DataDir, cfg.Identity().RegionName()); err != nil {
			return err
		}
		logger.Info("data directory migrated", "services", len(plan.Moves))
	}

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:           cfg.DataDir,
		Services:          cfg.Services,
		S3Host:            cfg.S3Host,
		Identity:          cfg.Identity(),
		LambdaIdleTimeout: cfg.LambdaIdleTimeout,
		LambdaQuiet:       cfg.LambdaQuiet,
		LambdaRuntimes:    cfg.LambdaRuntimes,
		IAMMode:           iamMode,
		Endpoint:          reachableEndpoint(cfg.ListenAddr),
		Logf: func(format string, args ...any) {
			logger.Info(fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		return err
	}
	defer stack.Close()

	// Apply a CloudFormation/SAM template at boot, if one is named or a
	// conventionally-named one is sitting in the working directory.
	tmplPath := cfg.TemplateFile
	if tmplPath == "" {
		for _, candidate := range config.DefaultTemplateFiles {
			if _, err := os.Stat(candidate); err == nil {
				tmplPath = candidate
				break
			}
		}
	}
	if tmplPath != "" {
		data, err := os.ReadFile(tmplPath)
		if err != nil {
			return fmt.Errorf("template: %w", err)
		}
		sf, rep, err := loadTemplate(data, tmplPath, nil)
		if err != nil {
			return fmt.Errorf("template %s: %w", tmplPath, err)
		}
		printTranspileReport(rep)
		applyRep, err := provision.Apply(context.Background(), stack.Handler(), sf, cfg.Identity())
		if err != nil {
			return err
		}
		created, updated, skipped := applyRep.Counts()
		logger.Info("template applied", "file", tmplPath, "created", created, "updated", updated, "in_place", skipped)
	}

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	enabled := cfg.Services
	if enabled == nil {
		enabled = dozeaws.Implemented
	}
	// This exact line is what the E2E test (and any wrapping tooling) parses
	// to learn the bound address — keep its shape stable.
	logger.Info("listening", "addr", ln.Addr().String(), "services", strings.Join(enabled, ","))

	// Mount the web console alongside the AWS gateway on the same endpoint. The
	// "/_console" prefix can never collide with a valid S3 bucket name (those
	// forbid underscores), so path-style S3 routing is unaffected.
	handler := http.Handler(stack.Handler())
	if cfg.Console {
		// The recorder wraps the gateway for external SDK/CLI traffic; the
		// console reads it for the Traffic tail but drives its own calls
		// through the RAW gateway so they never appear there.
		rec := console.NewRecorder(stack.Handler(), cfg.Identity())
		// S3, Lambda and API Gateway name their operation by PATH. Without these
		// the wire falls back to mapping the HTTP method, which collapses all 64
		// S3 operations onto five strings — GetBucketVersioning shown as
		// GetObject. These are the same route tables the validators use, so the
		// wire and the validator name an operation identically.
		rec.SetOpResolver(dozeaws.OperationResolvers())
		// Pollers have no request behind them, so they are handed the recorder
		// directly — it is how a queued message's cause reaches the wire.
		stack.SetTraceSink(rec)
		// The console drives its own calls straight to each raw service handler
		// (peers.InProcess over the stack), so they never pass through the
		// recorder and never appear in the Traffic tail.
		con, err := console.New(console.Options{
			Identity: cfg.Identity(),
			Peers:    peers.InProcess(stack.Service),
			Recorder: rec,
		})
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		mux.Handle("/_console/", con)
		mux.Handle("/_console", http.RedirectHandler("/_console/", http.StatusFound))
		mux.Handle("/", rec)
		handler = mux
		logger.Info("console", "url", "http://"+ln.Addr().String()+"/_console/")
	}

	srv := &http.Server{Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Join .doze: claim aws.doze, serve the zone if no peer is, and take an
	// extra listener on the name's own address. All of it is additive — the
	// configured address above is the contract and is never affected.
	z := joinZone(ctx, logger)
	defer z.close()
	_, cfgPort, _ := net.SplitHostPort(ln.Addr().String())
	extra := z.listen(logger, ln.Addr().String(), cfgPort)
	if url := z.url(); url != "" {
		logger.Info("listening", "addr", extra.Addr().String(), "name", url)
	}

	errc := make(chan error, 1)
	bg.Go(slogf(logger), "doze-aws: listener", func() { errc <- srv.Serve(ln) })
	serveExtra(srv, extra, logger)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down: draining connections, closing stores")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// reachableEndpoint turns a listen address into a URL a child Lambda process can
// dial (AWS_ENDPOINT_URL). A wildcard/empty host becomes 127.0.0.1.
func reachableEndpoint(listenAddr string) string {
	if listenAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://" + listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// servicesFlag collects a comma-separated (and/or repeated) flag into a slice.
type servicesFlag struct{ vals *[]string }

func (f servicesFlag) String() string {
	if f.vals == nil {
		return ""
	}
	return strings.Join(*f.vals, ",")
}

func (f servicesFlag) Set(s string) error {
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*f.vals = append(*f.vals, part)
		}
	}
	return nil
}
