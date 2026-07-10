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

	"github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/config"
)

// version is the build version, injected by the release tooling
// (-ldflags "-X main.version=..."). It defaults to "dev" for local builds.
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("doze-aws %s — local AWS services: %s\n", version, strings.Join(dozeaws.Implemented, ", "))
		return
	}

	// `doze-aws config print` writes the effective configuration (defaults,
	// then the config file, then flags) as TOML — a ready-to-edit starting point.
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if len(os.Args) < 3 || os.Args[2] != "print" {
			fmt.Fprintln(os.Stderr, "usage: doze-aws config print [flags]")
			os.Exit(2)
		}
		st, err := loadConfig(os.Args[3:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := config.WriteTOML(os.Stdout, st.cfg); err != nil {
			fmt.Fprintln(os.Stderr, "config print:", err)
			os.Exit(1)
		}
		return
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

// parseFlags binds the flags onto dst and parses args, returning the --config
// path. Because the flags' defaults are dst's current field values, keys
// already set from a config file survive unless the flag is explicitly passed.
func parseFlags(args []string, dst *config.Config) (configPath string) {
	fs := flag.NewFlagSet("doze-aws", flag.ExitOnError)
	cp := fs.String("config", "", "path to a TOML config file (default: ./doze-aws.toml if present)")
	fs.StringVar(&dst.ListenAddr, "listen", dst.ListenAddr, "host:port for the shared endpoint")
	fs.StringVar(&dst.DataDir, "data-dir", dst.DataDir, "root directory for service data")
	fs.Var(servicesFlag{&dst.Services}, "services", "comma-separated services to enable (default: all implemented)")
	fs.StringVar(&dst.S3Host, "s3-host", dst.S3Host, "base host for virtual-hosted-style S3 bucket addressing")
	fs.Parse(args) //nolint:errcheck // flag.ExitOnError exits on a parse error.
	return *cp
}

// run builds the stack and serves until interrupted.
func run(cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	stack, err := dozeaws.NewStack(dozeaws.StackConfig{
		DataDir:  cfg.DataDir,
		Services: cfg.Services,
		S3Host:   cfg.S3Host,
		Logf: func(format string, args ...any) {
			logger.Info(fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		return err
	}
	defer stack.Close()

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

	srv := &http.Server{Handler: stack.Handler()}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

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
