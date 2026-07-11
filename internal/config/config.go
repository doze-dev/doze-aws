// Package config defines the doze-aws binary's runtime configuration and its
// defaults. Construct with Default, overlay a TOML file, then flags, then call
// Validate — the same flags > file > defaults model as doze-kafka.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/gateway"
)

// Config holds every tunable for the doze-aws binary.
type Config struct {
	// ListenAddr is the shared endpoint every enabled service answers on.
	ListenAddr string
	// DataDir is the root data directory; each service owns a subdirectory.
	DataDir string
	// Services to enable; empty means every implemented service.
	Services []string
	// S3Host is the base host for virtual-hosted-style S3 bucket detection
	// (reserved until the s3 service lands).
	S3Host string
	// Console mounts the web management UI at /_console on the shared endpoint.
	Console bool
	// LambdaIdleTimeout is how long a warm Lambda function keeps its process(es)
	// before scaling to zero.
	LambdaIdleTimeout time.Duration
	// StackFile is a declarative stack.yaml applied at boot (and the default
	// target of `doze-aws apply`). Empty auto-detects ./stack.yaml.
	StackFile string
}

// Default returns a Config suitable for zero-config local development. The
// listen port matches LocalStack's, so existing AWS_ENDPOINT_URL setups work
// unchanged.
func Default() Config {
	return Config{
		ListenAddr:        "127.0.0.1:4566",
		DataDir:           "./data",
		S3Host:            "localhost",
		Console:           true,
		LambdaIdleTimeout: 10 * time.Minute,
	}
}

// Validate reports the first problem that would prevent the binary from
// starting. Service-name existence is checked against the full roadmap set
// here; whether a service is implemented yet is the stack's concern.
func (c Config) Validate() error {
	switch {
	case c.ListenAddr == "":
		return fmt.Errorf("config: listen address is empty")
	case c.DataDir == "":
		return fmt.Errorf("config: data dir is empty")
	}
	for _, s := range c.Services {
		if !gateway.KnownService(s) {
			return fmt.Errorf("config: unknown service %q (known: %s)", s, strings.Join(gateway.Services, ", "))
		}
	}
	return nil
}
