package cloudformation

// Emitting a CloudFormation template from a live stack — the inverse of the
// transpiler, and what `doze-aws export` produces.
//
// This exists because the capability is worth keeping and the old format is
// not: "click a stack together in the console, export it, commit it" is a good
// workflow, but the artifact should be something the rest of the world can
// read. A template exported here deploys to real AWS.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/doze-dev/doze-aws/provision"
)

// Emit renders a resource graph as a CloudFormation template in YAML.
func Emit(s *provision.Stack) ([]byte, error) {
	resources := map[string]any{}

	// Logical IDs must be alphanumeric, so a name like "app/config" or
	// "orders.fifo" is sanitised — and the real name is kept as an explicit
	// property so the round trip is lossless.
	add := func(prefix, name string, typ string, props map[string]any) {
		resources[logicalID(prefix, name)] = map[string]any{
			"Type":       typ,
			"Properties": props,
		}
	}

	// Order does not matter. Every emitter only INSERTS into resources, never
	// reads it back; the cross-references between them (a function naming a
	// layer, a filter naming its log group) go through logicalID, which is a
	// pure string function and does not need the referenced resource to exist
	// yet. marshalYAML sorts keys, so the bytes are identical whatever order
	// these run in. They are listed roughly as a stack is built rather than
	// alphabetically, because that reads better than either alternative.
	emitStateMachines(s, add)
	emitQueues(s, add)
	emitTopics(s, add)
	emitBuckets(s, add)
	emitTables(s, add)
	emitLayers(s, add)
	emitFunctions(s, add)
	emitRules(s, add)
	emitEventsHTTP(s, add)
	emitLogs(s, add)
	emitCloudWatch(s, add)
	emitKeys(s, add)
	emitSecrets(s, add)
	emitParameters(s, add)

	// This stays last: it is the only thing here that reads resources.
	if len(resources) == 0 {
		return nil, fmt.Errorf("nothing to export: no resources found")
	}
	doc := map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              "Exported from doze-aws",
		"Resources":                resources,
	}
	return marshalYAML(doc)
}

// ---- helpers ----

// logicalID builds a valid CloudFormation logical ID from a resource name.
func logicalID(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	upper := true
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if upper && r >= 'a' && r <= 'z' {
				b.WriteRune(r - 32)
			} else {
				b.WriteRune(r)
			}
			upper = false
		default:
			upper = true // the next letter starts a new word
		}
	}
	return b.String()
}

// arnSub emits an Fn::Sub ARN so an exported template is portable to real AWS
// rather than hard-coding the local account and region.
func arnSub(service, name string) map[string]any {
	return map[string]any{"Fn::Sub": fmt.Sprintf("arn:${AWS::Partition}:%s:${AWS::Region}:${AWS::AccountId}:%s", service, name)}
}

func lambdaArnSub(name string) map[string]any {
	return map[string]any{"Fn::Sub": "arn:${AWS::Partition}:lambda:${AWS::Region}:${AWS::AccountId}:function:" + name}
}

func destARN(d *provision.Dest) any {
	switch {
	case d.Topic != "":
		return arnSub("sns", d.Topic)
	case d.Lambda != "":
		return lambdaArnSub(d.Lambda)
	default:
		return arnSub("sqs", d.Queue)
	}
}

// rawDoc turns a stored JSON document back into a structure, so it renders as
// YAML rather than an embedded string.
func rawDoc(d provision.Doc) any {
	var v any
	if json.Unmarshal([]byte(d.JSON), &v) == nil {
		return v
	}
	return d.JSON
}

// keyBlocks turns the "pk:S sk:N" shorthand into CloudFormation's separate
// AttributeDefinitions and KeySchema lists.
func keyBlocks(key string) (attrs []any, keySchema []any) {
	parts := strings.Fields(key)
	roles := []string{"HASH", "RANGE"}
	for i, part := range parts {
		if i >= len(roles) {
			break
		}
		name, typ, _ := strings.Cut(part, ":")
		if typ == "" {
			typ = "S"
		}
		attrs = append(attrs, map[string]any{"AttributeName": name, "AttributeType": strings.ToUpper(typ)})
		keySchema = append(keySchema, map[string]any{"AttributeName": name, "KeyType": roles[i]})
	}
	return attrs, keySchema
}

// mergeAttrs unions attribute definitions, since a GSI key may reuse or add to
// the table's attributes and CloudFormation rejects duplicates.
func mergeAttrs(a, b []any) []any {
	seen := map[string]bool{}
	var out []any
	for _, list := range [][]any{a, b} {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := fmt.Sprint(m["AttributeName"])
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, item)
		}
	}
	return out
}

func putIf(m map[string]any, key string, v bool) {
	if v {
		m[key] = true
	}
}

func putIfNum(m map[string]any, key string, v int) {
	if v > 0 {
		m[key] = v
	}
}

func putIfStr(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func putIfList(m map[string]any, key string, v []string) {
	if len(v) > 0 {
		m[key] = v
	}
}

func putTags(m map[string]any, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	var out []any
	for _, k := range sortedNames(tags) {
		out = append(out, map[string]any{"Key": k, "Value": tags[k]})
	}
	m["Tags"] = out
}

func appendAny(existing any, item any) []any {
	list, _ := existing.([]any)
	return append(list, item)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func sortedNames[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// marshalYAML renders the template with a stable two-space indent.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := newYAMLEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// newYAMLEncoder builds the encoder Emit renders through.
func newYAMLEncoder(w *bytes.Buffer) *yaml.Encoder {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc
}
