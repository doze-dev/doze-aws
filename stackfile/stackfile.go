// Package stackfile implements doze-aws's declarative resource file: a
// stack.yaml a team commits to their repo so `doze-aws apply stack.yaml`
// (or `doze-aws --stack stack.yaml`) stands the whole local stack up.
//
// Design choices, deliberately:
//   - Resources are named by map key and wired by NAME, not ARN — inside one
//     local account/region names are unambiguous, and the file reads like the
//     console.
//   - Apply is CONVERGENT: create what's missing, update what's cheap to
//     update, and never touch values a human may have changed (secrets and
//     parameters keep their live value unless `force: true`).
//   - Dependency order is fixed by phase (queues before the topics that
//     subscribe to them, functions before the rules that target them, bucket
//     notifications last) so references always resolve.
package stackfile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stack is the parsed stack.yaml.
type Stack struct {
	// Vars feed ${var:name} references; `doze-aws apply --var name=value`
	// overrides them. Values may themselves use ${env:...}.
	Vars       map[string]string    `yaml:"vars,omitempty"`
	Queues     map[string]Queue     `yaml:"queues,omitempty"`
	Topics     map[string]Topic     `yaml:"topics,omitempty"`
	Buckets    map[string]Bucket    `yaml:"buckets,omitempty"`
	Tables     map[string]Table     `yaml:"tables,omitempty"`
	Functions  map[string]Function  `yaml:"functions,omitempty"`
	Rules      map[string]Rule      `yaml:"rules,omitempty"`
	Keys       map[string]Key       `yaml:"keys,omitempty"`
	Secrets    map[string]Secret    `yaml:"secrets,omitempty"`
	Parameters map[string]Parameter `yaml:"parameters,omitempty"`
}

type Queue struct {
	FIFO         bool   `yaml:"fifo,omitempty"`
	ContentDedup bool   `yaml:"content_dedup,omitempty"`
	DLQ          string `yaml:"dlq,omitempty"` // "auto" or a queue name
	MaxReceives  int    `yaml:"max_receives,omitempty"`
	Visibility   int    `yaml:"visibility,omitempty"`
	Delay        int    `yaml:"delay,omitempty"`
	Retention    int    `yaml:"retention,omitempty"`
}

type Topic struct {
	Subscriptions []Subscription `yaml:"subscriptions,omitempty"`
}

// Subscription names exactly one endpoint kind.
type Subscription struct {
	Queue  string `yaml:"queue,omitempty"`
	Lambda string `yaml:"lambda,omitempty"`
	HTTP   string `yaml:"http,omitempty"`
	Filter Doc    `yaml:"filter,omitempty"` // SNS filter policy (inline YAML or JSON string)
	Raw    bool   `yaml:"raw,omitempty"`    // raw message delivery
}

type Bucket struct {
	Versioning bool     `yaml:"versioning,omitempty"`
	ObjectLock bool     `yaml:"object_lock,omitempty"`
	Notify     []Notify `yaml:"notify,omitempty"`
}

// Notify wires bucket events to exactly one destination kind.
type Notify struct {
	Events []string `yaml:"events,omitempty"` // default ["s3:ObjectCreated:*"]
	Prefix string   `yaml:"prefix,omitempty"`
	Suffix string   `yaml:"suffix,omitempty"`
	Queue  string   `yaml:"queue,omitempty"`
	Topic  string   `yaml:"topic,omitempty"`
	Lambda string   `yaml:"lambda,omitempty"`
}

type Table struct {
	Key  string         `yaml:"key"` // "pk:S" or "pk:S sk:N"
	TTL  string         `yaml:"ttl,omitempty"`
	GSIs map[string]GSI `yaml:"gsis,omitempty"`
}

type GSI struct {
	Key string `yaml:"key"` // same shorthand as Table.Key
}

type Function struct {
	Runtime   string            `yaml:"runtime,omitempty"`
	Handler   string            `yaml:"handler,omitempty"`
	Code      string            `yaml:"code,omitempty"` // local path (the _local_ extension)
	Command   []string          `yaml:"command,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Timeout   int               `yaml:"timeout,omitempty"`
	Memory    int               `yaml:"memory,omitempty"`
	OnSuccess *Dest             `yaml:"on_success,omitempty"`
	OnFailure *Dest             `yaml:"on_failure,omitempty"`
	Triggers  []Trigger         `yaml:"triggers,omitempty"`
}

// Dest names exactly one destination kind.
type Dest struct {
	Queue  string `yaml:"queue,omitempty"`
	Topic  string `yaml:"topic,omitempty"`
	Lambda string `yaml:"lambda,omitempty"`
}

type Trigger struct {
	Queue string `yaml:"queue"`
	Batch int    `yaml:"batch,omitempty"`
}

type Rule struct {
	Bus      string   `yaml:"bus,omitempty"` // default "default"
	Pattern  Doc      `yaml:"pattern,omitempty"`
	Schedule string   `yaml:"schedule,omitempty"`
	Targets  []string `yaml:"targets,omitempty"` // "queue:orders", "lambda:fn"
}

type Key struct {
	Spec     string `yaml:"spec,omitempty"` // default SYMMETRIC_DEFAULT
	Rotation bool   `yaml:"rotation,omitempty"`
}

type Secret struct {
	Value string `yaml:"value,omitempty"`
	Force bool   `yaml:"force,omitempty"` // overwrite a live value on apply
}

// Parameter accepts a scalar shorthand:
//
//	parameters:
//	  /app/db/host: localhost
type Parameter struct {
	Value string `yaml:"value,omitempty"`
	Type  string `yaml:"type,omitempty"` // String | SecureString | StringList
	Force bool   `yaml:"force,omitempty"`
}

func (p *Parameter) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		p.Value = n.Value
		return nil
	}
	type plain Parameter
	return n.Decode((*plain)(p))
}

// Doc is a JSON document that may be written as inline YAML or a JSON string.
type Doc struct {
	JSON string
}

func (d *Doc) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		d.JSON = strings.TrimSpace(n.Value)
		return nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d.JSON = string(b)
	return nil
}

func (d Doc) MarshalYAML() (any, error) {
	if d.JSON == "" {
		return nil, nil
	}
	// Round-trip through a generic value so exports read as inline YAML.
	var v any
	if err := json.Unmarshal([]byte(d.JSON), &v); err != nil {
		return d.JSON, nil //nolint:nilerr // not JSON — emit the raw string
	}
	return v, nil
}

func (d Doc) IsZero() bool { return d.JSON == "" }

// keyAttr is one parsed "name:TYPE" key element.
type keyAttr struct{ Name, Type string }

// parseKey parses the "pk:S" / "pk:S sk:N" shorthand.
func parseKey(s string) (hash keyAttr, rng *keyAttr, err error) {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 2 {
		return hash, nil, fmt.Errorf("key %q: want \"attr:TYPE\" or \"pk:TYPE sk:TYPE\"", s)
	}
	parse := func(f string) (keyAttr, error) {
		name, typ, ok := strings.Cut(f, ":")
		if !ok || name == "" {
			return keyAttr{}, fmt.Errorf("key part %q: want \"attr:TYPE\"", f)
		}
		typ = strings.ToUpper(typ)
		if typ != "S" && typ != "N" && typ != "B" {
			return keyAttr{}, fmt.Errorf("key part %q: type must be S, N or B", f)
		}
		return keyAttr{name, typ}, nil
	}
	if hash, err = parse(fields[0]); err != nil {
		return hash, nil, err
	}
	if len(fields) == 2 {
		r, err := parse(fields[1])
		if err != nil {
			return hash, nil, err
		}
		rng = &r
	}
	return hash, rng, nil
}

// Parse decodes and validates a stack.yaml, resolving ${env:...} and
// ${var:...} references first.
func Parse(data []byte) (*Stack, error) { return ParseWithVars(data, nil) }

// ParseWithVars is Parse with --var overrides for ${var:...} references.
func ParseWithVars(data []byte, overrides map[string]string) (*Stack, error) {
	expanded, err := expand(data, overrides)
	if err != nil {
		return nil, fmt.Errorf("stackfile: %w", err)
	}
	var s Stack
	dec := yaml.NewDecoder(strings.NewReader(string(expanded)))
	dec.KnownFields(true) // typos fail loudly, like the config loader
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("stackfile: %w", err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("stackfile: %w", err)
	}
	return &s, nil
}

func (s *Stack) validate() error {
	oneOf := func(what, name string, set ...string) error {
		n := 0
		for _, v := range set {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("%s %q: name exactly one destination (queue / topic / lambda / http)", what, name)
		}
		return nil
	}

	for name, q := range s.Queues {
		if q.FIFO && !strings.HasSuffix(name, ".fifo") {
			return fmt.Errorf("queue %q: fifo queues must be named with the .fifo suffix — references elsewhere in this file use the final name", name)
		}
		if !q.FIFO && strings.HasSuffix(name, ".fifo") {
			return fmt.Errorf("queue %q: the .fifo suffix requires fifo: true", name)
		}
		if q.DLQ != "" && q.DLQ != "auto" {
			if _, ok := s.Queues[q.DLQ]; !ok {
				return fmt.Errorf("queue %q: dlq %q is not declared in this file", name, q.DLQ)
			}
		}
	}
	for name, t := range s.Tables {
		if _, _, err := parseKey(t.Key); err != nil {
			return fmt.Errorf("table %q: %w", name, err)
		}
		for gname, g := range t.GSIs {
			if _, _, err := parseKey(g.Key); err != nil {
				return fmt.Errorf("table %q gsi %q: %w", name, gname, err)
			}
		}
	}
	for name, tp := range s.Topics {
		for i, sub := range tp.Subscriptions {
			if err := oneOf("topic", fmt.Sprintf("%s subscription %d", name, i+1), sub.Queue, sub.Lambda, sub.HTTP); err != nil {
				return err
			}
			if sub.Queue != "" {
				if _, ok := s.Queues[sub.Queue]; !ok {
					return fmt.Errorf("topic %q: subscribed queue %q is not declared in this file", name, sub.Queue)
				}
			}
			if sub.Lambda != "" {
				if _, ok := s.Functions[sub.Lambda]; !ok {
					return fmt.Errorf("topic %q: subscribed lambda %q is not declared in this file", name, sub.Lambda)
				}
			}
		}
	}
	for name, b := range s.Buckets {
		for i, nf := range b.Notify {
			if err := oneOf("bucket", fmt.Sprintf("%s notify %d", name, i+1), nf.Queue, nf.Topic, nf.Lambda); err != nil {
				return err
			}
			refErr := func(kind, ref string, ok bool) error {
				if !ok {
					return fmt.Errorf("bucket %q: notify %s %q is not declared in this file", name, kind, ref)
				}
				return nil
			}
			if nf.Queue != "" {
				if _, ok := s.Queues[nf.Queue]; refErr("queue", nf.Queue, ok) != nil {
					return refErr("queue", nf.Queue, ok)
				}
			}
			if nf.Topic != "" {
				if _, ok := s.Topics[nf.Topic]; refErr("topic", nf.Topic, ok) != nil {
					return refErr("topic", nf.Topic, ok)
				}
			}
			if nf.Lambda != "" {
				if _, ok := s.Functions[nf.Lambda]; refErr("lambda", nf.Lambda, ok) != nil {
					return refErr("lambda", nf.Lambda, ok)
				}
			}
		}
	}
	for name, f := range s.Functions {
		if f.Code == "" {
			return fmt.Errorf("function %q: code (a local path) is required", name)
		}
		for _, d := range []*Dest{f.OnSuccess, f.OnFailure} {
			if d == nil {
				continue
			}
			if err := oneOf("function", name+" destination", d.Queue, d.Topic, d.Lambda); err != nil {
				return err
			}
		}
		for i, tr := range f.Triggers {
			if tr.Queue == "" {
				return fmt.Errorf("function %q trigger %d: queue is required", name, i+1)
			}
			if _, ok := s.Queues[tr.Queue]; !ok {
				return fmt.Errorf("function %q: trigger queue %q is not declared in this file", name, tr.Queue)
			}
		}
	}
	for name, r := range s.Rules {
		if r.Pattern.IsZero() && r.Schedule == "" {
			return fmt.Errorf("rule %q: needs a pattern, a schedule, or both", name)
		}
		if !r.Pattern.IsZero() && !json.Valid([]byte(r.Pattern.JSON)) {
			return fmt.Errorf("rule %q: pattern is not valid JSON", name)
		}
		for _, t := range r.Targets {
			kind, ref, ok := strings.Cut(t, ":")
			if !ok {
				return fmt.Errorf("rule %q: target %q — want \"queue:name\" or \"lambda:name\"", name, t)
			}
			switch kind {
			case "queue":
				if _, ok := s.Queues[ref]; !ok {
					return fmt.Errorf("rule %q: target queue %q is not declared in this file", name, ref)
				}
			case "lambda":
				if _, ok := s.Functions[ref]; !ok {
					return fmt.Errorf("rule %q: target lambda %q is not declared in this file", name, ref)
				}
			default:
				return fmt.Errorf("rule %q: target kind %q — want queue or lambda", name, kind)
			}
		}
	}
	for name, p := range s.Parameters {
		switch p.Type {
		case "", "String", "SecureString", "StringList":
		default:
			return fmt.Errorf("parameter %q: type %q — want String, SecureString or StringList", name, p.Type)
		}
	}
	return nil
}

// Marshal renders a Stack back to YAML (used by export).
func Marshal(s *Stack) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("# stack.yaml — generated by `doze-aws export`.\n")
	buf.WriteString("# Secret and SecureString values are not exported; fill them in (or let\n")
	buf.WriteString("# apply create them empty) — apply never overwrites live values without force.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	enc.Close()
	return []byte(buf.String()), nil
}

// sortedNames returns map keys in stable order (apply and export both walk
// resources deterministically).
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
