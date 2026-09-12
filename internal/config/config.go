// Package config defines the doze-aws binary's runtime configuration and its
// defaults. Construct with Default, overlay a TOML file, then flags, then call
// Validate — the same flags > file > defaults model as doze-kafka.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
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
	// Console mounts the web management UI at /_console on the shared endpoint.
	Console bool
	// LambdaIdleTimeout is how long a warm Lambda function keeps its process(es)
	// before scaling to zero.
	LambdaIdleTimeout time.Duration
	// LambdaQuiet stops function output (stdout and stderr of the function
	// processes) from being echoed to the doze-aws log. The lines still reach
	// the logs service.
	LambdaQuiet bool
	// LambdaRuntimes overrides the interpreter per runtime family: python,
	// nodejs, ruby, java, dotnet. The PATH is searched otherwise.
	LambdaRuntimes map[string]string
	// TemplateFile is a CloudFormation/SAM template applied at boot (and the
	// default target of `doze-aws apply`). Empty auto-detects the conventional
	// template names.
	TemplateFile string
	// IAMMode selects how far IAM goes on the request path: "soft" (the
	// default) evaluates and records without ever blocking, "off" never
	// evaluates, "enforce" returns real AccessDenied errors.
	IAMMode string
	// AccountID is the twelve-digit account every ARN this instance mints
	// carries. Empty means the conventional local account.
	//
	// It is settable at creation and effectively frozen afterwards: ARNs stored
	// INSIDE other resources — an EventBridge target, a Lambda event source
	// mapping, an IAM policy resource — are strings that were written with the
	// old account, so changing it orphans every cross-resource reference
	// silently, at fire time rather than when the change is made.
	AccountID string
	// Region is the region every ARN this instance mints carries. Empty means
	// the conventional local region.
	Region string
	// Suffix is the DNS suffix that stands in for amazonaws.com in the
	// AWS-shaped hostnames this instance recognises and mints. Empty means only
	// the conventional infixes are recognised and no AWS-shaped URL is minted.
	Suffix string
	// Regions are additional regions served alongside Region. A region a signed
	// request names is created on first use whether or not it is listed; listing
	// one only creates it eagerly, so it appears before anything touches it.
	Regions []string
	// AssumeYes answers the confirmation a flag overruling the config file
	// asks for. It is not persisted to the file -- it is a property of one
	// invocation, not of the instance.
	AssumeYes bool
	// Name is this instance's name in the .doze zone. doze-aws answers on
	// aws.<Name>.doze, and the AWS-shaped hostnames it mints sit beneath that,
	// so two projects on one machine never contend for an address.
	//
	// Empty means derive one from the working directory — see DeriveName.
	Name string
}

// DefaultName is the instance name used when nothing better can be derived:
// a directory whose name has no letters or digits in it at all.
const DefaultName = "local"

// InstanceName is the name to claim and to report, and is never empty.
//
// Name is normally filled in when the configuration is loaded, from the
// project directory. This is the floor for a Config built directly in code —
// without it an empty name renders as aws..doze, which resolves to nothing and
// reads like a bug in the zone rather than a missing setting.
func (c Config) InstanceName() string {
	if c.Name == "" {
		return DefaultName
	}
	return c.Name
}

// DeriveName picks an instance name from a project directory.
//
// The directory's own name is the right default because it is what a person
// already calls the project, and because two checkouts then get two instances
// with nobody configuring anything — which is the whole point of naming.
//
// The result is not sanitized here. doze-names reduces a name to a DNS label
// on claim, and duplicating that rule is exactly the kind of mirror this tree
// keeps getting bitten by; what this checks is only whether anything would
// SURVIVE that reduction, since a directory called "~" would otherwise claim
// the empty label.
func DeriveName(dir string) string {
	base := filepath.Base(dir)
	if !hasLabelChar(base) {
		return DefaultName
	}
	return base
}

// hasLabelChar reports whether s contains anything that survives reduction to
// a DNS label — at least one letter or digit.
func hasLabelChar(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// Identity is the region and account this configuration mints ARNs for.
func (c Config) Identity() awsident.Identity {
	return awsident.Identity{Region: c.Region, AccountID: c.AccountID}
}

// Default returns a Config suitable for zero-config local development. The
// listen port matches LocalStack's, so existing AWS_ENDPOINT_URL setups work
// unchanged.
func Default() Config {
	return Config{
		// No default address: doze-aws answers on its .doze NAME, and --listen
		// is the opt-in for the cases a name cannot serve. See cmd/doze-aws/listen.go.
		ListenAddr:        "",
		DataDir:           "./data",
		Console:           true,
		LambdaIdleTimeout: 10 * time.Minute,
		IAMMode:           "soft",
	}
}

// Validate reports the first problem that would prevent the binary from
// starting. Service-name existence is checked against the full roadmap set
// here; whether a service is implemented yet is the stack's concern.
func (c Config) Validate() error {
	// An empty ListenAddr is no longer an error: it is the default. doze-aws
	// answers on its .doze name, and --listen adds an address alongside it.
	if c.DataDir == "" {
		return fmt.Errorf("config: data dir is empty")
	}
	for _, s := range c.Services {
		if !gateway.KnownService(s) {
			return fmt.Errorf("config: unknown service %q (known: %s)", s, strings.Join(gateway.Services, ", "))
		}
	}
	// A name with nothing label-worthy in it would claim aws..doze, which
	// resolves to nothing and is confusing rather than broken-looking.
	if c.Name != "" && !hasLabelChar(c.Name) {
		return fmt.Errorf("config: instance name %q has no letters or digits — it cannot become a DNS label", c.Name)
	}
	// AWS account ids are exactly twelve digits, and a wrong one is not a
	// cosmetic problem: it goes into every ARN the instance mints, and an SDK
	// that parses an ARN for its account segment gets a malformed answer.
	if c.AccountID != "" {
		if len(c.AccountID) != 12 {
			return fmt.Errorf("config: account id %q is %d characters, want 12 digits", c.AccountID, len(c.AccountID))
		}
		for _, r := range c.AccountID {
			if r < '0' || r > '9' {
				return fmt.Errorf("config: account id %q must be digits only", c.AccountID)
			}
		}
	}
	return nil
}
