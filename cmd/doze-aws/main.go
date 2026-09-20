// Command doze-aws serves local, from-scratch emulations of the AWS services a
// development stack leans on — real wire protocols, both AWS SDK generations,
// no Docker, no JVM.
//
// An instance answers on its own name. `doze-aws` in a directory called
// harbour is aws.harbour.doze, and the AWS-shaped hostnames sit beneath it, so
// a URL it hands back differs from the real one by the suffix alone:
//
//	https://sqs.ap-south-1.amazonaws.com/811690671382/orders     AWS
//	http://sqs.ap-south-1.aws.harbour.doze/811690671382/orders   doze-aws
//
// That needs `.doze` to resolve, which the first run offers to arrange: one
// prompt, one sudo, once per machine. `doze-aws dns-setup` is the same thing
// run deliberately, for CI or a scripted install. Where a name cannot serve
// — a sibling container over a compose network, CI, anywhere without DNS —
// `--listen host:port` is the opt-in, and it REPLACES the name rather than
// adding to it: one instance, one way to reach it.
//
// Data lives under ./data, or wherever doze-aws.toml says.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/iam"
	"github.com/doze-dev/doze-aws/internal/config"
	"github.com/doze-dev/doze-aws/internal/console"
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
		{"env", envUsage, runEnv},
		{"config print", "print the effective configuration as TOML, ready to edit", runConfigPrint},
		{"dns-setup", "prepare this machine for .doze names (idempotent)", runDNSSetup},
		{"doctor", doctorUsage, runDoctor},
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
	// Default(), not a zero Config: PrintDefaults reads the values bound to the
	// flags, so a zero one showed no defaults at all — and the canonical help
	// implied --console was opt-in when it is on.
	probe := config.Default()
	fs, _ := newFlagSet(&probe)
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprint(w, "\nExamples:\n"+
		"  doze-aws                                  everything, on aws.<dir>.doze\n"+
		"  doze-aws dns-setup                        set .doze up deliberately (CI, scripted installs)\n"+
		"  doze-aws --name harbour                   name the instance explicitly\n"+
		"  doze-aws --services s3,sqs,lambda         just those three\n"+
		"  doze-aws --listen 127.0.0.1:4566          an address instead of a name\n"+
		"  doze-aws apply template.yaml              deploy a template into a running stack\n"+
		"  eval \"$(doze-aws env)\"                    point your shell's AWS SDK at it\n"+
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
	case "-v", "--version", "-version":
		// `doze-aws version` is the documented spelling, but --version is
		// muscle memory from cargo, docker, gh and everything else. Without
		// this it fell through to the flag package, which answered a version
		// query with "flag provided but not defined" and thirty lines of flags.
		return runVersion(nil), true
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
		// Asked BEFORE anything is opened or claimed, so answering no leaves
		// the machine exactly as it was.
		if err := confirmOverrides(liveOverrideEnv(st.cfg.AssumeYes), st.configFile, st.fileKeys, st.given); err != nil {
			os.Exit(1)
		}
	}

	if err := run(st.cfg, logger); err != nil {
		fatal(logger, err)
	}
}

// fatal reports a startup failure and exits.
//
// Multi-line errors go to stderr PLAINLY rather than through the log handler.
// The errors worth reading at startup are the ones carrying remedies —
// "nothing to listen on", a name held by another process, a data directory
// belonging to a different account — and slog's TextHandler renders those as
// one quoted line with the newlines escaped to \n, which turns a three-line
// answer into something nobody reads.
//
// A wrapping prefix is stripped for the same reason. dozeaws wraps per-region
// errors as "region ap-south-1: …", which is right for a region-level failure
// and noise on one about the whole data directory — the account check is not
// about a region and says so on its own.
func fatal(logger *slog.Logger, err error) {
	var changed *dozeaws.ErrAccountChanged
	if errors.As(err, &changed) {
		err = changed
	}
	if strings.Contains(err.Error(), "\n") {
		fmt.Fprintln(os.Stderr, err)
	} else {
		logger.Error("fatal", "err", err)
	}
	os.Exit(1)
}

// startup holds everything resolved from the command line before serving.
type startup struct {
	cfg        config.Config
	configFile string // the config file actually loaded, or "" if none.
	// fileKeys names what the config file set, and given what was passed on the
	// command line. Kept so startup can report a flag overruling the file — see
	// confirmOverrides.
	fileKeys map[string]bool
	given    map[string]bool
}

// loadConfig resolves the effective configuration with flags > file > defaults
// precedence. It parses once to discover --config (falling back to
// ./doze-aws.toml if present), overlays that file onto the defaults, then
// parses the flags again on top so any flag still wins.
func loadConfig(args []string) (startup, error) {
	probe := config.Default()
	configPath, _ := parseFlags(args, &probe)
	if configPath == "" {
		if _, err := os.Stat(config.DefaultConfigFile); err == nil {
			configPath = config.DefaultConfigFile
		}
	}

	c := config.Default()
	var fileKeys map[string]bool
	if configPath != "" {
		var err error
		if fileKeys, err = config.LoadFile(configPath, &c); err != nil {
			return startup{}, err
		}
	}
	_, given := parseFlags(args, &c)
	// The instance name is resolved HERE rather than in Default, so it is the
	// same answer for the server and for the `apply`/`export` clients that have
	// to find it — all three run in the project directory, and all three go
	// through loadConfig.
	if c.Name == "" {
		wd, err := os.Getwd()
		if err != nil {
			return startup{}, fmt.Errorf("config: cannot read the working directory to name this instance: %w", err)
		}
		c.Name = config.DeriveName(wd)
	}
	// The data directory is made ABSOLUTE once, here, so that every later
	// reader means the same thing by it.
	//
	// It has two possible anchors — the config file for a value written in one,
	// the working directory for the --data-dir flag — and which applied is not
	// visible downstream. A log line, `doctor`, and `config print` should not
	// each have to ask "relative to what?", and `config print` in particular
	// claims to emit the EFFECTIVE configuration: "./data" is not effective,
	// it is a question.
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	return startup{cfg: c, configFile: configPath, fileKeys: fileKeys, given: given}, nil
}

// newFlagSet binds the flags onto dst and returns the set plus the --config
// value. Because the flags' defaults are dst's current field values, keys
// already set from a config file survive unless the flag is explicitly passed.
func newFlagSet(dst *config.Config) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("doze-aws", flag.ExitOnError)
	cp := fs.String("config", "", "path to a TOML config file (default: ./doze-aws.toml if present)")
	fs.StringVar(&dst.ListenAddr, "listen", dst.ListenAddr, "serve on this host:port INSTEAD of a .doze name (for containers, CI, anywhere without DNS)")
	fs.StringVar(&dst.DataDir, "data-dir", dst.DataDir, "root directory for service data")
	fs.Var(servicesFlag{&dst.Services}, "services", "comma-separated services to enable (default: all implemented)")
	fs.StringVar(&dst.AccountID, "account-id", dst.AccountID, "twelve-digit account id every ARN carries (default 000000000000; set at creation, hard to change later)")
	fs.StringVar(&dst.Region, "region", dst.Region, "default region; its data lives under <data-dir>/<region> (default us-east-1)")
	fs.BoolVar(&dst.AssumeYes, "yes", dst.AssumeYes, "answer yes to the confirmation a flag overruling doze-aws.toml asks for")
	fs.StringVar(&dst.Name, "name", dst.Name, "this instance's name in .doze; it answers on aws.<name>.doze (default: the directory name)")
	fs.StringVar(&dst.Suffix, "suffix", dst.Suffix, "DNS suffix standing in for amazonaws.com (default: this instance's own name)")
	fs.Var(servicesFlag{&dst.Regions}, "regions", "comma-separated extra regions to serve (any region a signed request names is created on first use regardless)")
	fs.BoolVar(&dst.Console, "console", dst.Console, "serve the web management console at /_console")
	fs.DurationVar(&dst.LambdaIdleTimeout, "lambda-idle", dst.LambdaIdleTimeout, "how long a warm Lambda keeps its process before scaling to zero")
	fs.BoolVar(&dst.LambdaQuiet, "lambda-quiet", dst.LambdaQuiet, "do not echo Lambda function output to this log")
	fs.StringVar(&dst.TemplateFile, "template", dst.TemplateFile, "CloudFormation/SAM template to apply at boot (default: ./template.yaml if present)")
	fs.StringVar(&dst.IAMMode, "iam-mode", dst.IAMMode, "IAM enforcement: soft (default: evaluate and record, never block), off (no evaluation), enforce (deny for real)")
	return fs, cp
}

// parseFlags binds the flags onto dst and parses args, returning the --config path.
// given names the flags EXPLICITLY passed, via fs.Visit — which walks only
// those that were set, unlike VisitAll. That distinction is the whole point:
// a flag left at its default must not look like an instruction.
func parseFlags(args []string, dst *config.Config) (configPath string, given map[string]bool) {
	fs, cp := newFlagSet(dst)
	fs.Parse(args) //nolint:errcheck // flag.ExitOnError exits on a parse error.
	given = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	return *cp, given
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Addresses are decided BEFORE anything is built, because what doze-aws
	// tells a Lambda process to dial (AWS_ENDPOINT_URL) and what it reports in
	// a queue URL both depend on where it ended up listening.
	//
	// The two modes are EXCLUSIVE, not additive. Without --listen this instance
	// is its .doze name and nothing else. With --listen it is that address and
	// nothing else: no name is claimed, no DNS check runs, and the registry
	// records nothing. One instance, one way to reach it.
	//
	// Additive was the old shape, and it produced an instance that answered on
	// two addresses with two different URL shapes depending on which one you
	// asked through — and a binds.endpoint that preferred the name, so a child
	// Lambda under --listen was handed a .doze URL it could not resolve on a
	// machine where dns-setup had never run.
	var z *zone
	if cfg.ListenAddr == "" {
		if _, nerr := ensureNames(liveNameEnv()); nerr != nil {
			return nerr
		}
		z = joinZone(ctx, logger, cfg.InstanceName())
		defer z.close()
	}

	binds, err := openListeners(cfg, z, logger)
	if err != nil {
		return err
	}
	defer binds.close()

	// The suffix that stands in for amazonaws.com is this instance's own name,
	// unless --suffix says otherwise. Deriving it rather than requiring it is
	// what makes sqs.ap-south-1.aws.harbour.doze come out of a plain
	// `doze-aws` with nothing configured.
	//
	// Under --listen there is no name, so there is no derived suffix and the
	// URL shapes fall back to paths — correct, because there would be no
	// resolver to serve an AWS-shaped hostname. --suffix still works there, and
	// that is the containerised-behind-a-proxy case: the proxy owns the name.
	if cfg.Suffix == "" && z != nil && z.own != nil {
		cfg.Suffix = z.own.Name.Host
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

	regions, err := dozeaws.NewRegions(dozeaws.StackConfig{
		DataDir:           cfg.DataDir,
		Services:          cfg.Services,
		Identity:          cfg.Identity(),
		LambdaIdleTimeout: cfg.LambdaIdleTimeout,
		LambdaQuiet:       cfg.LambdaQuiet,
		LambdaRuntimes:    cfg.LambdaRuntimes,
		IAMMode:           iamMode,
		Endpoint:          binds.endpoint,
		Suffix:            cfg.Suffix,
		Logf: func(format string, args ...any) {
			logger.Info(fmt.Sprintf(format, args...))
		},
	}, cfg.Regions)
	if err != nil {
		return err
	}
	defer regions.Close()
	// The default region's stack is what the console and the boot-time template
	// apply act on. Everything reached over the wire goes through regions,
	// which picks per request from the credential scope.
	stack := regions.Stack(regions.Default())

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

	enabled := cfg.Services
	if enabled == nil {
		enabled = dozeaws.Implemented
	}
	// This exact line is what the E2E test (and any wrapping tooling) parses
	// to learn the bound address — keep its shape stable.
	logger.Info("listening", "addr", binds.primary().ln.Addr().String(), "services", strings.Join(enabled, ","),
		"regions", strings.Join(regions.Serving(), ","), "account", cfg.Identity().Account(),
		"instance", cfg.InstanceName())
	for _, b := range binds.all {
		logger.Info("reachable at", "url", b.url, "as", b.what)
	}

	// Mount the web console alongside the AWS gateway on the same endpoint. The
	// "/_console" prefix can never collide with a valid S3 bucket name (those
	// forbid underscores), so path-style S3 routing is unaffected.
	// What to put in front of a person, which is not always binds.primary().url:
	// a name that does not resolve on this machine is worse than an address,
	// and the console link has to agree with the endpoint or one of them is
	// wrong. Resolved once, here, after the name is registered and the listener
	// is up. See binding.advertise.
	advertised := binds.primary().advertise(logger)

	handler := http.Handler(regions)
	consoleURL := ""
	if cfg.Console {
		// The recorder wraps the gateway for external SDK/CLI traffic; the
		// console reads it for the Traffic tail but drives its own calls
		// through the RAW gateway so they never appear there.
		rec := console.NewRecorder(regions, cfg.Identity())
		// S3, Lambda and API Gateway name their operation by PATH. Without these
		// the wire falls back to mapping the HTTP method, which collapses all 64
		// S3 operations onto five strings — GetBucketVersioning shown as
		// GetObject. These are the same route tables the validators use, so the
		// wire and the validator name an operation identically.
		rec.SetOpResolver(dozeaws.OperationResolvers())
		// Pollers have no request behind them, so they are handed the recorder
		// directly — it is how a queued message's cause reaches the wire.
		regions.SetTraceSink(rec)
		// The console drives its own calls straight to each raw service handler
		// (peers.InProcess over the stack), so they never pass through the
		// recorder and never appear in the Traffic tail.
		con, err := console.New(console.Options{
			Identity: cfg.Identity(),
			// Without this the console routed with an empty suffix, so an
			// AWS-shaped host reached it and was not recognised as one.
			Suffix:   cfg.Suffix,
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
		logger.Info("console", "url", advertised+"/_console/")
		consoleURL = advertised + "/_console/"
	}

	// The same facts as the log lines above, for the person rather than the
	// parser. See ready.go for why this is stdout and they are stderr.
	ready{
		endpoint: advertised,
		console:  consoleURL,
		services: enabled,
		region:   cfg.Identity().RegionName(),
		account:  cfg.Identity().Account(),
	}.write(os.Stdout)

	srv := &http.Server{Handler: handler}

	errc := make(chan error, 1)
	binds.serve(srv, logger, errc)

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
