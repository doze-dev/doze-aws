package config

import (
	"fmt"
	"io"
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

// fileConfig is the on-disk (TOML) shape of Config. Its keys match the
// command-line flags one-to-one. Every scalar is a pointer (slices nil-able)
// so a key absent from the file leaves the corresponding Config value
// untouched — that is what makes flags > file > defaults precedence work.
type fileConfig struct {
	Listen   *string     `toml:"listen"`
	DataDir  *string     `toml:"data-dir"`
	Services []string    `toml:"services"`
	S3       *s3File     `toml:"s3"`
	Lambda   *lambdaFile `toml:"lambda"`
}

type s3File struct {
	Host *string `toml:"host"`
}

type lambdaFile struct {
	IdleTimeout *tomlDuration `toml:"idle-timeout"`
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
		return fmt.Errorf("config: %s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	fc.applyTo(cfg)
	return nil
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
	if fc.S3 != nil && fc.S3.Host != nil {
		cfg.S3Host = *fc.S3.Host
	}
	if fc.Lambda != nil && fc.Lambda.IdleTimeout != nil {
		cfg.LambdaIdleTimeout = fc.Lambda.IdleTimeout.Duration
	}
}

// WriteTOML renders cfg as a TOML document — the effective configuration in
// the same shape LoadFile reads, so its output is a valid config file. Used by
// `doze-aws config print`.
func WriteTOML(w io.Writer, cfg Config) error {
	fc := fileConfig{
		Listen:  &cfg.ListenAddr,
		DataDir: &cfg.DataDir,
		S3:      &s3File{Host: &cfg.S3Host},
		Lambda:  &lambdaFile{IdleTimeout: &tomlDuration{cfg.LambdaIdleTimeout}},
	}
	if len(cfg.Services) > 0 {
		fc.Services = cfg.Services
	}
	return toml.NewEncoder(w).Encode(fc)
}
