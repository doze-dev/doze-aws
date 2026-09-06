package awsquery

// The client half of the Query protocol: flattening a JSON-shaped request
// into form parameters, and lifting an XML response back into JSON. The
// server half (Params, Members, PairMap, MessageAttrs, and modelcheck's
// FromQuery) fixes the spellings; this file is their mirror, and the encode
// tests prove the round trip through those very decoders.
//
// Spellings, as the decoders read them:
//
//	scalar          Name=value
//	structure       Parent.Child=value
//	list            Prefix.member.N=value      (Prefix.N when Memberless)
//	list of structs Prefix.member.N.Field=value
//	map             Prefix.entry.N.<key>=k, Prefix.entry.N.<val>=v
//
// A JSON object is a structure unless the member is one the protocol models
// as a map — the Query protocol only knows from the model, so the caller
// names them (DefaultMaps covers the ones SNS speaks: MessageAttributes as
// entry.N.Name/Value, Attributes as entry.N.key/value).

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// MapShape says how one map-valued member flattens: the key and value
// element names under Prefix.entry.N.
type MapShape struct{ Key, Val string }

// EncodeOptions steer Encode. Maps is keyed by member name (the last path
// segment); Memberless numbers lists as Prefix.N (SQS's legacy spelling)
// rather than Prefix.member.N.
type EncodeOptions struct {
	Maps       map[string]MapShape
	Memberless bool
}

// DefaultMaps are the map-valued members the Query services in this repo
// decode: SNS MessageAttributes (entry.N.Name / entry.N.Value.*) and SNS
// topic Attributes (entry.N.key / entry.N.value).
var DefaultMaps = map[string]MapShape{
	"MessageAttributes": {Key: "Name", Val: "Value"},
	"Attributes":        {Key: "key", Val: "value"},
}

// Encode flattens params into the form for one Action. params is the JSON
// document decoded to any: strings, numbers, bools, lists and objects.
func Encode(action string, params map[string]any, opts EncodeOptions) url.Values {
	if opts.Maps == nil {
		opts.Maps = DefaultMaps
	}
	form := url.Values{"Action": {action}}
	encodeValue(form, "", params, opts)
	return form
}

func encodeValue(form url.Values, prefix string, v any, opts EncodeOptions) {
	switch t := v.(type) {
	case nil:
		// An absent member is absent, not "null".
	case string:
		form.Set(prefix, t)
	case bool:
		form.Set(prefix, strconv.FormatBool(t))
	case float64:
		form.Set(prefix, strconv.FormatFloat(t, 'f', -1, 64))
	case int:
		form.Set(prefix, strconv.Itoa(t))
	case int64:
		form.Set(prefix, strconv.FormatInt(t, 10))
	case []any:
		for i, el := range t {
			key := prefix + ".member." + strconv.Itoa(i+1)
			if opts.Memberless {
				key = prefix + "." + strconv.Itoa(i+1)
			}
			encodeValue(form, key, el, opts)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if shape, isMap := opts.Maps[lastSegment(prefix)]; isMap && prefix != "" {
			for i, k := range keys {
				base := prefix + ".entry." + strconv.Itoa(i+1) + "."
				form.Set(base+shape.Key, k)
				encodeValue(form, base+shape.Val, t[k], opts)
			}
			return
		}
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			encodeValue(form, key, t[k], opts)
		}
	default:
		form.Set(prefix, fmt.Sprint(t))
	}
}

func lastSegment(prefix string) string {
	if i := strings.LastIndex(prefix, "."); i >= 0 {
		return prefix[i+1:]
	}
	return prefix
}

// ---- responses ----

// Result lifts a Query success envelope into JSON: the children of the
// {Action}Result element become members, `member`-numbered lists become
// arrays, `entry` lists with key/value (or Name/Value) children become
// objects, and leaves are strings — the protocol carries no types, so the
// caller's schema (or the reader's eye) supplies them. A result-less action
// yields an empty object.
func Result(action string, body []byte) (map[string]any, error) {
	root, err := parseXML(body)
	if err != nil {
		return nil, err
	}
	for _, child := range root.children {
		if child.name == action+"Result" {
			out, _ := child.toJSON().(map[string]any)
			if out == nil {
				out = map[string]any{}
			}
			return out, nil
		}
	}
	return map[string]any{}, nil
}

// Error decodes the Query error envelope. ok is false when body is not one.
func Error(body []byte) (code, message string, ok bool) {
	root, err := parseXML(body)
	if err != nil || root.name != "ErrorResponse" {
		return "", "", false
	}
	for _, e := range root.children {
		if e.name != "Error" {
			continue
		}
		for _, f := range e.children {
			switch f.name {
			case "Code":
				code = f.text
			case "Message":
				message = f.text
			}
		}
		return code, message, code != ""
	}
	return "", "", false
}

// node is a namespace-blind XML element tree — enough for response
// envelopes, whose xmlns is decoration.
type node struct {
	name     string
	text     string
	children []*node
}

func parseXML(body []byte) (*node, error) {
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil {
		return nil, fmt.Errorf("empty XML document")
	}
	return root, nil
}

func (n *node) toJSON() any {
	if len(n.children) == 0 {
		return strings.TrimSpace(n.text)
	}
	// Every child a `member`: a list. Every child an `entry`: a map.
	allNamed := func(name string) bool {
		for _, c := range n.children {
			if c.name != name {
				return false
			}
		}
		return true
	}
	if allNamed("member") {
		list := make([]any, 0, len(n.children))
		for _, c := range n.children {
			list = append(list, c.toJSON())
		}
		return list
	}
	if allNamed("entry") {
		if m, ok := entriesAsMap(n.children); ok {
			return m
		}
	}
	out := map[string]any{}
	for _, c := range n.children {
		v := c.toJSON()
		// A repeated sibling (S3-style lists without a member wrapper)
		// accumulates into an array.
		if prev, dup := out[c.name]; dup {
			if list, isList := prev.([]any); isList {
				out[c.name] = append(list, v)
			} else {
				out[c.name] = []any{prev, v}
			}
			continue
		}
		out[c.name] = v
	}
	return out
}

func entriesAsMap(entries []*node) (map[string]any, bool) {
	out := map[string]any{}
	for _, e := range entries {
		var key string
		var val any
		found := false
		for _, pair := range [][2]string{{"key", "value"}, {"Name", "Value"}} {
			var k, v *node
			for _, c := range e.children {
				switch c.name {
				case pair[0]:
					k = c
				case pair[1]:
					v = c
				}
			}
			if k != nil && v != nil {
				key, val, found = k.text, v.toJSON(), true
				break
			}
		}
		if !found {
			return nil, false
		}
		out[key] = val
	}
	return out, true
}
