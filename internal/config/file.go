package config

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// tomlDuration lets a Go duration string ("10m", "30s") sit in a TOML file and
// round-trip through BurntSushi/toml via the text (un)marshaler hooks.
type tomlDuration struct{ time.Duration }

func (d *tomlDuration) UnmarshalText(b []byte) error {
	parsed, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

func (d tomlDuration) MarshalText() ([]byte, error) { return []byte(d.Duration.String()), nil }

// DefaultConfigFile is loaded automatically from the working directory when no
// --config flag is given and the file exists.
const DefaultConfigFile = "doze-aws.toml"

// DefaultTemplateFiles are the CloudFormation/SAM template names applied at
// boot when present and no --template names one. The order follows the
// conventions those tools already use, so an existing project needs no config.
var DefaultTemplateFiles = []string{
	"template.yaml", "template.yml",
	"cloudformation.yaml", "cloudformation.yml",
	"template.json",
}

// fileConfig is the on-disk (TOML) shape of Config. Its keys match the
// command-line flags one-to-one. Every scalar is a pointer (slices nil-able)
// so a key absent from the file leaves the corresponding Config value
// untouched — that is what makes flags > file > defaults precedence work.
type fileConfig struct {
	Listen    *string     `toml:"listen"`
	DataDir   *string     `toml:"data-dir"`
	Services  []string    `toml:"services"`
	Lambda    *lambdaFile `toml:"lambda"`
	Template  *string     `toml:"template"`
	Region    *string     `toml:"region"`
	AccountID *string     `toml:"account-id"`
	Name      *string     `toml:"name"`
}

type lambdaFile struct {
	IdleTimeout *tomlDuration     `toml:"idle-timeout"`
	Quiet       *bool             `toml:"quiet"`
	Runtimes    map[string]string `toml:"runtimes"`
}

// LoadFile reads a TOML config file and overlays it onto cfg. Unknown keys are
// rejected so a typo fails loudly instead of being silently ignored.
func LoadFile(path string, cfg *Config) error {
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return fmt.Errorf("config: %s: unknown key(s): %s%s",
			path, strings.Join(keys, ", "), removedKeyHint(keys))
	}
	fc.applyTo(cfg)
	resolveAgainstFile(path, fc.DataDir, cfg)
	return nil
}

// resolveAgainstFile anchors the data directory to the CONFIG FILE rather than
// to the process's working directory.
//
// A path written in a file has to mean the same thing wherever the file is
// read from — the convention Cargo.toml, package.json and tsconfig.json all
// use. Without this, `doze-aws --config /srv/harbour/doze-aws.toml` run from
// your home directory put the data in ~/data, which is nobody's intent.
//
// The flag is deliberately NOT anchored this way. --data-dir is typed in a
// shell, now, so it resolves against where you are standing; it is applied
// after this, so it overrides whatever was resolved here.
//
// An absent key means <config dir>/data, so the ordinary layout — a TOML and
// its data side by side — needs nothing written down. `doze-aws init` writes
// the key explicitly anyway, because reading the file should tell you where
// the data is without having to know this rule, and because it is the one line
// an operator edits to point at a bigger volume.
func resolveAgainstFile(path string, fileValue *string, cfg *Config) {
	dir := filepath.Dir(path)
	switch {
	case fileValue == nil:
		cfg.DataDir = filepath.Join(dir, "data")
	case filepath.IsAbs(*fileValue):
		// Already absolute: the mounted-volume case, left exactly as written.
	default:
		cfg.DataDir = filepath.Join(dir, *fileValue)
	}
}

// removed maps a key this config file used to accept to what replaced it.
//
// An unknown key is normally a typo, and "unknown key" is the whole answer. A
// key that was REAL last release is a different problem: the person did not
// mistype it, they wrote it when it worked, and telling them only that it is
// unknown leaves them to guess whether the feature went or the spelling did.
var removed = map[string]string{
	"s3":      "virtual-hosted S3 addressing now uses the instance suffix: <bucket>.s3.<region>.<suffix>. Set --suffix, or use UsePathStyle in your SDK",
	"s3.host": "virtual-hosted S3 addressing now uses the instance suffix: <bucket>.s3.<region>.<suffix>. Set --suffix, or use UsePathStyle in your SDK",
}

// removedKeyHint appends an explanation for any key that used to be valid.
func removedKeyHint(keys []string) string {
	var out strings.Builder
	seen := map[string]bool{}
	for _, k := range keys {
		hint, ok := removed[k]
		if !ok || seen[hint] {
			continue
		}
		seen[hint] = true
		out.WriteString("\n  " + k + " was removed — " + hint)
	}
	return out.String()
}

func (fc fileConfig) applyTo(cfg *Config) {
	if fc.Listen != nil {
		cfg.ListenAddr = *fc.Listen
	}
	if fc.DataDir != nil {
		cfg.DataDir = *fc.DataDir
	}
	if fc.Services != nil {
		cfg.Services = fc.Services
	}
	if fc.Lambda != nil && fc.Lambda.IdleTimeout != nil {
		cfg.LambdaIdleTimeout = fc.Lambda.IdleTimeout.Duration
	}
	if fc.Lambda != nil && fc.Lambda.Quiet != nil {
		cfg.LambdaQuiet = *fc.Lambda.Quiet
	}
	if fc.Lambda != nil && fc.Lambda.Runtimes != nil {
		cfg.LambdaRuntimes = fc.Lambda.Runtimes
	}
	if fc.Region != nil {
		cfg.Region = *fc.Region
	}
	if fc.AccountID != nil {
		cfg.AccountID = *fc.AccountID
	}
	if fc.Name != nil {
		cfg.Name = *fc.Name
	}
	if fc.Template != nil {
		cfg.TemplateFile = *fc.Template
	}
}

// WriteTOML renders cfg as a TOML document — the effective configuration in
// the same shape LoadFile reads, so its output is a valid config file. Used by
// `doze-aws config print`.
func WriteTOML(w io.Writer, cfg Config) error {
	fc := fileConfig{
		Listen:    &cfg.ListenAddr,
		DataDir:   &cfg.DataDir,
		Region:    &cfg.Region,
		AccountID: &cfg.AccountID,
		Name:      &cfg.Name,
		Lambda:    &lambdaFile{IdleTimeout: &tomlDuration{cfg.LambdaIdleTimeout}, Quiet: &cfg.LambdaQuiet, Runtimes: cfg.LambdaRuntimes},
	}
	if len(cfg.Services) > 0 {
		fc.Services = cfg.Services
	}
	return toml.NewEncoder(w).Encode(fc)
}
