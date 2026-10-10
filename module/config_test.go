package aws

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// testBody extracts the aws block's driver body from a full
// `aws "local" { … }` source, the way core hands it to DecodeConfig with the
// common fields (port) already stripped.
func testBody(t *testing.T, src string) hcl.Body {
	t.Helper()
	f, diags := hclparse.NewParser().ParseHCL([]byte(src), "doze.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse: %s", diags)
	}
	content, _, d := f.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "aws", LabelNames: []string{"name"}}},
	})
	if d.HasErrors() || len(content.Blocks) == 0 {
		t.Fatalf("no aws block: %s", d)
	}
	_, remain, d := content.Blocks[0].Body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "port"}},
	})
	if d.HasErrors() {
		t.Fatalf("strip common: %s", d)
	}
	return remain
}

func decodeAWS(t *testing.T, src string) (*Config, error) {
	t.Helper()
	spec, err := Driver{}.DecodeConfig(testBody(t, src), &hcl.EvalContext{}, ".", "")
	if err != nil {
		return nil, err
	}
	return spec.(*Config), nil
}

// The state_machine block lands in provision.Stack with the definition text
// exactly as written (minus surrounding whitespace), the role defaulted, and
// the type upper-cased — the three things a deploy would otherwise fail on.
func TestStateMachineBlockDecode(t *testing.T) {
	cfg, err := decodeAWS(t, `
aws "local" {
  port = 4566
  queue "emails" {}

  state_machine "order_flow" {
    type = "standard"
    tags = { team = "shop" }
    definition = <<-ASL
      {"StartAt": "Done", "States": {"Done": {"Type": "Succeed"}}}
    ASL
  }
}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	m, ok := cfg.Stack.StateMachines["order_flow"]
	if !ok {
		t.Fatalf("state machine missing from the stack: %+v", cfg.Stack.StateMachines)
	}
	if m.Definition != `{"StartAt": "Done", "States": {"Done": {"Type": "Succeed"}}}` {
		t.Errorf("definition not carried verbatim: %q", m.Definition)
	}
	if m.RoleARN != defaultStateMachineRole {
		t.Errorf("role should default when unset, got %q", m.RoleARN)
	}
	if m.Type != "STANDARD" {
		t.Errorf("type should be upper-cased, got %q", m.Type)
	}
	if m.Tags["team"] != "shop" {
		t.Errorf("tags lost: %v", m.Tags)
	}
	if _, ok := cfg.Stack.Queues["emails"]; !ok {
		t.Errorf("the queue beside it should still decode")
	}
}

func TestStateMachineBlockRefusals(t *testing.T) {
	for name, src := range map[string]string{
		"empty definition": "aws \"local\" {\n  state_machine \"m\" { definition = \"  \" }\n}",
		"bad type":         "aws \"local\" {\n  state_machine \"m\" {\n    definition = \"{}\"\n    type = \"FAST\"\n  }\n}",
		"no definition":    "aws \"local\" {\n  state_machine \"m\" {}\n}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeAWS(t, src)
			if err == nil {
				t.Fatal("decoded, want a refusal")
			}
			if !strings.Contains(err.Error(), "m") && !strings.Contains(err.Error(), "definition") {
				t.Errorf("error should name the block or the field: %v", err)
			}
		})
	}
}
