package s3

// Resolving a request to an operation, and validating it.
//
// S3 names its operation nowhere on the wire. A request is identified by the
// method, the path shape, and a query-string sub-resource marker — which is
// exactly the hazard subresource.go exists to guard against. This resolves the
// operation the same way, from AWS's own model, and then reassembles the input
// from the four places restXml scatters it: the path labels, the query string,
// the headers, and an XML document body.

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// codeREST is what S3 calls a refused input.
const codeREST = "InvalidRequest"

// routeFor is the model's route for an operation.
func routeFor(op string) (route, bool) {
	for _, rt := range routes {
		if rt.Op == op {
			return rt, true
		}
	}
	return route{}, false
}

// validateRequest reassembles the operation's input and walks its constraints.
// The operation is the one the router matched, so a request that matched no
// route is not validated — the router refuses it. It leaves a document body
// readable for the handler: reading a request body consumes it, and a
// validator that ate the input would break every operation it was meant to
// protect.
func validateRequest(r *http.Request) (aerr *awshttp.APIError) {
	var body []byte
	rt, ok := routeFor(restroute.Op(r))
	if !ok {
		return nil
	}
	table := constraintTables[rt.Op]
	if len(table) == 0 {
		return nil
	}
	// Only an XML document body is read. An object body is the payload of
	// PutObject and friends, and can be gigabytes — buffering it to check
	// constraints that are not in it would be a bad trade.
	if rt.Payload != "" && r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		r.Body.Close()
		if err != nil {
			return awshttp.Errf(400, "MalformedXML", "read request body: %v", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	input := map[string]any{}
	if len(body) > 0 {
		if doc, err := xmlToMap(body); err == nil {
			input[rt.Payload] = normalizeXML(doc, rt.Payload, rt, table)
		}
	}
	for i, name := range rt.Labels {
		// An omitted path label is not an absent field, it is an empty segment.
		if name == "" {
			continue
		}
		value := restroute.Param(r, name)
		if rt.Greedy && i == len(rt.Labels)-1 {
			value = restroute.Wildcard(r)
		}
		if value != "" {
			input[name] = value
		}
	}
	query := r.URL.Query()
	for param, member := range rt.Query {
		v, ok := query[param]
		if !ok || len(v) == 0 {
			continue
		}
		if rt.QueryList[member] {
			els := make([]any, len(v))
			for i, s := range v {
				els[i] = s
			}
			input[member] = els
			continue
		}
		input[member] = v[0]
	}
	for name, member := range rt.Header {
		v, ok := r.Header[http.CanonicalHeaderKey(name)]
		if !ok || len(v) == 0 {
			continue
		}
		if rt.HeaderList[member] {
			// A list in a header is comma-separated, not a repeated header:
			// x-amz-optional-object-attributes: RestoreStatus,Other.
			parts := strings.Split(v[0], ",")
			els := make([]any, len(parts))
			for i, p := range parts {
				els[i] = strings.TrimSpace(p)
			}
			input[member] = els
			continue
		}
		input[member] = v[0]
	}
	return modelcheck.ValidateMapAs(input, table, codeREST)
}

// xmlToMap turns an XML document into the map shape modelcheck walks. Repeated
// elements become a list, which is how restXml spells one — there is no bracket
// syntax to distinguish a one-element list from a scalar, so a single
// occurrence stays a scalar and modelcheck's list handling accepts both.
func xmlToMap(raw []byte) (map[string]any, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var root *node
	stack := []*node{}
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
			// Attributes are members too: S3 spells a grantee's type as
			// <Grantee xsi:type="CanonicalUser">, and a validator that read
			// only elements would report a valid SDK request as missing it.
			for _, a := range t.Attr {
				if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
					continue
				}
				n.kids = append(n.kids, &node{name: a.Name.Local, text: a.Value})
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.kids = append(parent.kids, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return map[string]any{}, nil
	}
	m, _ := root.value().(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

type node struct {
	name string
	text string
	kids []*node
}

func (n *node) value() any {
	if len(n.kids) == 0 {
		return strings.TrimSpace(n.text)
	}
	out := map[string]any{}
	for _, k := range n.kids {
		v := k.value()
		switch prev, seen := out[k.name]; {
		case !seen:
			out[k.name] = v
		default:
			if list, isList := prev.([]any); isList {
				out[k.name] = append(list, v)
				continue
			}
			out[k.name] = []any{prev, v}
		}
	}
	return out
}

// normalizeXML rewrites a parsed document from its wire spelling into the
// member names the constraints are written against, using the shape the model
// supplied.
//
// Two things make this necessary, and both are silent when skipped. A wrapped
// list arrives as <TagSet><Tag/><Tag/></TagSet> — a map, not a list, so
// "Tagging.TagSet[].Key" would never match. And a renamed member arrives under
// its wire tag: S3 sends <Rule> for LifecycleConfiguration.Rules.
func normalizeXML(v any, path string, rt route, table []modelcheck.Constraint) any {
	if s, isText := v.(string); isText && s == "" && hasMembers(path, table) {
		// An element written empty is an empty STRUCTURE, not an empty string.
		// Removing Suffix leaves <IndexDocument></IndexDocument>, and reading
		// that back as text means the required member inside it is never looked
		// for — the omission the case is about would pass.
		return map[string]any{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for tag, raw := range m {
		member := memberFor(tag, path, rt)
		child := path + "." + member
		list, isList := rt.XMLLists[child]
		if !isList {
			out[member] = normalizeXML(raw, child, rt, table)
			continue
		}
		els := listElements(raw, list)
		norm := make([]any, len(els))
		for i, el := range els {
			norm[i] = normalizeXML(el, child+"[]", rt, table)
		}
		out[member] = norm
	}
	return out
}

// hasMembers reports whether the model puts anything inside this path — which
// is what makes it a structure rather than a value.
func hasMembers(path string, table []modelcheck.Constraint) bool {
	prefix := path + "."
	for _, c := range table {
		if strings.HasPrefix(c.Path, prefix) {
			return true
		}
	}
	return false
}

// listElements pulls a list's members out of the document. A flattened list is
// already the repeated value; a wrapped one is inside its wrapper.
func listElements(raw any, list xmlList) []any {
	if !list.Flattened {
		wrapper, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		raw = wrapper[list.Element]
	}
	switch v := raw.(type) {
	case nil:
		return nil
	case []any:
		return v
	default:
		// One occurrence is indistinguishable from a scalar in the document
		// itself; the model is what says it is a list.
		return []any{v}
	}
}

// memberFor turns a wire tag back into the member name it stands for.
func memberFor(tag, path string, rt route) string {
	prefix := path + "."
	for member, wire := range rt.XMLNames {
		// The wire name may carry a namespace prefix the decoder strips:
		// "xsi:type" arrives as "type".
		if i := strings.LastIndex(wire, ":"); i >= 0 {
			wire = wire[i+1:]
		}
		if wire != tag || !strings.HasPrefix(member, prefix) {
			continue
		}
		if rest := member[len(prefix):]; !strings.ContainsAny(rest, ".[{") {
			return rest
		}
	}
	return tag
}
