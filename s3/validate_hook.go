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
)

// codeREST is what S3 calls a refused input.
const codeREST = "InvalidRequest"

// matchRoute finds the operation a request addresses. Routes are ordered
// most-specific first, so the first whose method, path shape and sub-resource
// markers all match wins.
func matchRoute(method, path string, query map[string][]string, headers http.Header) (route, map[string]string, bool) {
	// Two passes. The first requires every path segment to be there; only if
	// nothing matches does the second allow a greedy {Key+} to be empty.
	//
	// The order matters both ways. Without the second pass, GET /bucket?retention
	// matches nothing — there is no bucket-level retention operation — so it is
	// never validated, and doze-aws answers a request that should be refused for
	// a missing key. With the second pass running first, DELETE /bucket?tagging
	// would be claimed by DeleteObjectTagging with an empty key and a legitimate
	// DeleteBucketTagging would be refused.
	if rt, labels, ok := matchExact(method, path, query, headers, false); ok {
		return rt, labels, true
	}
	return matchExact(method, path, query, headers, true)
}

func matchExact(method, path string, query map[string][]string, headers http.Header, emptyKey bool) (route, map[string]string, bool) {
	// Interior empty segments are kept: "//key" is a request whose bucket is
	// empty, and dropping it would make every @required label pass vacuously.
	// A single trailing empty is dropped, because "/bucket/" is how a bucket
	// listing is spelled, not an object whose key is blank.
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segs) > 1 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	if len(segs) == 1 && segs[0] == "" {
		segs = nil
	}
	for _, rt := range routes {
		if rt.Method != method {
			continue
		}
		switch {
		case rt.Greedy && emptyKey:
			// The greedy label swallows nothing: the caller left the key out.
			if len(segs) != len(rt.Segs)-1 {
				continue
			}
		case rt.Greedy:
			if len(segs) < len(rt.Segs) {
				continue
			}
		default:
			if len(segs) != len(rt.Segs) {
				continue
			}
		}
		marked := true
		for k, want := range rt.Marks {
			v, ok := query[k]
			if !ok || (want != "" && (len(v) == 0 || v[0] != want)) {
				marked = false
				break
			}
		}
		if !marked {
			continue
		}
		// A marker the route does not declare belongs to a different operation.
		// Without this, ListObjects — which declares none — claims
		// GET /bucket?legal-hold and answers with a bucket listing.
		for k := range query {
			if markerKeys[k] && rt.Marks[k] == "" {
				if _, declared := rt.Marks[k]; !declared {
					marked = false
					break
				}
			}
		}
		if !marked {
			continue
		}
		for _, h := range rt.NeedHeaders {
			if _, ok := headers[http.CanonicalHeaderKey(h)]; !ok {
				marked = false
				break
			}
		}
		for _, q := range rt.NeedQuery {
			if _, ok := query[q]; !ok {
				marked = false
				break
			}
		}
		if !marked {
			continue
		}
		labels, ok := bindLabels(rt, segs, emptyKey)
		if !ok {
			continue
		}
		return rt, labels, true
	}
	return route{}, nil, false
}

// bindLabels fills the route's labels from the path segments. A greedy label
// takes every remaining segment, slashes included — that is what {Key+} means.
func bindLabels(rt route, segs []string, emptyKey bool) (map[string]string, bool) {
	out := map[string]string{}
	for i, want := range rt.Segs {
		last := i == len(rt.Segs)-1
		if rt.Greedy && last && emptyKey {
			out[rt.Labels[i]] = ""
			continue
		}
		if want != "" {
			if segs[i] != want {
				return nil, false
			}
			continue
		}
		if rt.Greedy && last {
			out[rt.Labels[i]] = strings.Join(segs[i:], "/")
			continue
		}
		out[rt.Labels[i]] = segs[i]
	}
	return out, true
}

// validateControl reassembles the operation's input and walks its constraints.
// It returns the body it read so the handler can still decode it — reading a
// request body consumes it, and a validator that ate the input would break
// every operation it was meant to protect.
func validateControl(r *http.Request) (body []byte, aerr *awshttp.APIError) {
	rt, labels, ok := matchRoute(r.Method, r.URL.Path, r.URL.Query(), r.Header)
	if !ok {
		return nil, nil
	}
	table := constraintTables[rt.Op]
	if len(table) == 0 {
		return nil, nil
	}
	// Only an XML document body is read. An object body is the payload of
	// PutObject and friends, and can be gigabytes — buffering it to check
	// constraints that are not in it would be a bad trade.
	if rt.Payload != "" && r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		r.Body.Close()
		if err != nil {
			return nil, awshttp.Errf(400, "MalformedXML", "read request body: %v", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	input := map[string]any{}
	if len(body) > 0 {
		if doc, err := xmlToMap(body); err == nil {
			input[rt.Payload] = normalizeXML(doc, rt.Payload, rt, table)
		}
	}
	for name, value := range labels {
		// An omitted path label is not an absent field, it is an empty segment.
		if value == "" {
			continue
		}
		input[name] = value
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
	return body, modelcheck.ValidateMapAs(input, table, codeREST)
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

// OperationFor reports the S3 operation a request addresses, or "" when no
// route matches.
//
// Exported for the console's traffic classifier, which otherwise names S3
// operations by mapping the HTTP method — collapsing all 64 of them onto five
// strings, so GetBucketVersioning is displayed as GetObject. The route table is
// already the source of truth for which operation a request IS (the validator
// picks the constraint set with it), and a wire that names it differently from
// the validator is a wire that lies about what happened.
func OperationFor(r *http.Request) string {
	rt, _, ok := matchRoute(r.Method, r.URL.Path, r.URL.Query(), r.Header)
	if !ok {
		return ""
	}
	return rt.Op
}
