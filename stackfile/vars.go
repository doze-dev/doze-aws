package stackfile

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Variable references, serverless-style, resolved BEFORE the YAML is parsed:
//
//	${env:BUILD_DIR}            — environment variable; unset is an error
//	${env:STAGE, dev}           — with a default when unset
//	${var:name}                 — from the file's top-level `vars:` block,
//	                              overridable with `doze-aws apply --var name=value`
//	$${literal}                 — escapes to ${literal}
//
// The headline use-case: secrets. `doze-aws export` deliberately redacts
// secret values, so a committed file says
//
//	secrets:
//	  app/config:
//	    value: ${env:APP_CONFIG_JSON}
//
// and the value never touches the repo.
var refRe = regexp.MustCompile(`\$\$\{|\$\{(env|var):([A-Za-z_][A-Za-z0-9_./-]*)(?:\s*,\s*([^}]*))?\}`)

// varsHeader is the shape of just the vars: block, extracted before full
// parsing so ${var:...} references elsewhere in the document can resolve.
type varsHeader struct {
	Vars map[string]string `yaml:"vars"`
}

// expand resolves every variable reference in the raw document. overrides
// (from --var flags) win over the file's vars: block; env references read the
// process environment. Unknown references are collected and reported together.
func expand(data []byte, overrides map[string]string) ([]byte, error) {
	// The vars block itself may use ${env:...}, so expand it first with an
	// empty var set (vars referencing other vars would be order-dependent
	// magic — env-only keeps the block predictable).
	var hdr varsHeader
	if err := yaml.Unmarshal(data, &hdr); err == nil && len(hdr.Vars) > 0 {
		for k, v := range hdr.Vars {
			ex, err := expandRefs([]byte(v), nil)
			if err != nil {
				return nil, fmt.Errorf("vars.%s: %w", k, err)
			}
			hdr.Vars[k] = string(ex)
		}
	}
	if hdr.Vars == nil {
		hdr.Vars = map[string]string{}
	}
	for k, v := range overrides {
		hdr.Vars[k] = v
	}
	return expandRefs(data, hdr.Vars)
}

func expandRefs(data []byte, vars map[string]string) ([]byte, error) {
	var missing []string
	out := refRe.ReplaceAllFunc(data, func(m []byte) []byte {
		if string(m) == "$${" {
			return []byte("${")
		}
		groups := refRe.FindSubmatch(m)
		kind, name := string(groups[1]), string(groups[2])
		hasDefault := strings.Contains(string(m), ",")
		def := strings.TrimSpace(string(groups[3]))
		switch kind {
		case "env":
			if v, ok := os.LookupEnv(name); ok {
				return []byte(v)
			}
			if hasDefault {
				return []byte(def)
			}
			missing = append(missing, "${env:"+name+"}")
		case "var":
			if v, ok := vars[name]; ok {
				return []byte(v)
			}
			if hasDefault {
				return []byte(def)
			}
			missing = append(missing, "${var:"+name+"}")
		}
		return m
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("unresolved references: %s (set the variable, pass --var, or add a default: ${env:NAME, fallback})", strings.Join(dedupe(missing), ", "))
	}
	return out, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
