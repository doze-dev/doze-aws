package main

// The subset of a Smithy JSON AST doze-aws needs: shapes, the members of a
// structure, and the constraint traits AWS annotates them with.
//
// Constraints live on the shape a member TARGETS, not on the member — an input
// member named TableName targets a TableArn shape, and the length and pattern
// are there. @required is the exception: it is a property of the member, since
// the same shape is optional elsewhere. Both have to be read.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type model struct {
	Shapes map[string]shape `json:"shapes"`
}

type shape struct {
	Type    string                     `json:"type"`
	Members map[string]member          `json:"members"`
	Member  *member                    `json:"member"` // list/set element
	Value   *member                    `json:"value"`  // map value
	Input   *ref                       `json:"input"`
	Traits  map[string]json.RawMessage `json:"traits"`

	// A service reaches its operations two ways: directly, and through
	// resources that bind them to lifecycle slots. Lambda hangs 72 of its 85
	// operations off resources, so reading Operations alone finds a seventh of
	// the surface and looks plausible while doing it.
	Operations           []ref `json:"operations"`
	Resources            []ref `json:"resources"`
	CollectionOperations []ref `json:"collectionOperations"`
	Create               *ref  `json:"create"`
	Put                  *ref  `json:"put"`
	Read                 *ref  `json:"read"`
	Update               *ref  `json:"update"`
	Delete               *ref  `json:"delete"`
	List                 *ref  `json:"list"`
}

type member struct {
	Target string                     `json:"target"`
	Traits map[string]json.RawMessage `json:"traits"`
}

type ref struct {
	Target string `json:"target"`
}

// modelURL is where AWS publishes the models its own SDKs are generated from.
const modelURL = "https://raw.githubusercontent.com/aws/aws-sdk-go-v2/main/codegen/sdk-codegen/aws-models/"

// load reads a model from a path, or fetches it by service id and caches it.
// Fetching is explicit rather than automatic on every run: these are the spec
// the audit is measured against, so it should be obvious when they move.
func load(src, cacheDir string) (*model, error) {
	path := src
	if !strings.HasSuffix(src, ".json") {
		var err error
		if path, err = fetch(src, cacheDir); err != nil {
			return nil, err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m model
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

func fetch(service, cacheDir string) (string, error) {
	path := cacheDir + "/" + service + ".json"
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(modelURL + service + ".json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: %s (is that the aws-models file name?)", service, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// service returns the model's service shape: its id, protocol, and operations.
func (m *model) service() (id string, protocol string, ops []string) {
	for sid, s := range m.Shapes {
		if s.Type != "service" {
			continue
		}
		id = sid
		for t := range s.Traits {
			if strings.HasPrefix(t, "aws.protocols#") {
				protocol = strings.TrimPrefix(t, "aws.protocols#")
			}
		}
		seen := map[string]bool{}
		m.collectOps(s, seen, &ops)
		return id, protocol, ops
	}
	return "", "", nil
}

// collectOps gathers a service's operations, following the resource tree.
func (m *model) collectOps(s shape, seen map[string]bool, out *[]string) {
	add := func(r *ref) {
		if r == nil || seen[r.Target] {
			return
		}
		seen[r.Target] = true
		*out = append(*out, r.Target)
	}
	for i := range s.Operations {
		add(&s.Operations[i])
	}
	for i := range s.CollectionOperations {
		add(&s.CollectionOperations[i])
	}
	for _, r := range []*ref{s.Create, s.Put, s.Read, s.Update, s.Delete, s.List} {
		add(r)
	}
	for _, r := range s.Resources {
		if seen["res:"+r.Target] {
			continue
		}
		seen["res:"+r.Target] = true
		if child, ok := m.Shapes[r.Target]; ok {
			m.collectOps(child, seen, out)
		}
	}
}

// constraint is one rule AWS states about an input, in the form the case
// generator needs: what it is, and what a violating value looks like.
type constraint struct {
	Kind    string   // range | length | pattern | enum | required
	Min     *float64 // range, length
	Max     *float64 // range, length
	Pattern string
	Values  []string // enum members
}

func (c constraint) String() string {
	switch c.Kind {
	case "range", "length":
		lo, hi := "-", "-"
		if c.Min != nil {
			lo = trimNum(*c.Min)
		}
		if c.Max != nil {
			hi = trimNum(*c.Max)
		}
		return c.Kind + " " + lo + ".." + hi
	case "pattern":
		return "pattern " + c.Pattern
	case "enum":
		if len(c.Values) > 6 {
			return fmt.Sprintf("enum (%d values)", len(c.Values))
		}
		return "enum " + strings.Join(c.Values, "|")
	}
	return c.Kind
}

func trimNum(f float64) string { return strings.TrimSuffix(fmt.Sprintf("%.0f", f), ".0") }

// constraintsOf reads the constraint traits off a trait map.
func constraintsOf(traits map[string]json.RawMessage) []constraint {
	var out []constraint
	for name, raw := range traits {
		switch name {
		case "smithy.api#range", "smithy.api#length":
			var b struct{ Min, Max *float64 }
			if json.Unmarshal(raw, &b) == nil {
				out = append(out, constraint{
					Kind: strings.TrimPrefix(name, "smithy.api#"), Min: b.Min, Max: b.Max,
				})
			}
		case "smithy.api#pattern":
			var p string
			if json.Unmarshal(raw, &p) == nil {
				out = append(out, constraint{Kind: "pattern", Pattern: p})
			}
		case "smithy.api#required":
			out = append(out, constraint{Kind: "required"})
		}
	}
	return out
}

// enumValues returns the members of an enum shape. Modern models express an
// enum as its own shape whose members carry enumValue, rather than as a trait.
func (m *model) enumValues(s shape) []string {
	var vals []string
	for _, mem := range s.Members {
		raw, ok := mem.Traits["smithy.api#enumValue"]
		if !ok {
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			vals = append(vals, v)
		}
	}
	return vals
}
