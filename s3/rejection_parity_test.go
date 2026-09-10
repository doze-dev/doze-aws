package s3

// Rejection parity for S3, driven by the cases dzaudit derives from AWS's own
// service model (`dzaudit cases s3`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// S3 is the third protocol this audit has had to learn, and the least
// forgiving. A single operation's input is spread across the path, the query
// string, the headers AND an XML document, and the operation itself is named
// nowhere — it is the method, the path shape, and a query marker together. So
// each case carries the binding dzaudit read out of the model, and the request
// is assembled from it: labels into the path, @httpQuery onto the query string,
// @httpHeader into headers, and the @httpPayload member serialised to XML using
// the element names the model supplies.
//
// Those element names are not optional. "Tagging.TagSet[].Key" is
// <Tagging><TagSet><Tag><Key/></Tag></TagSet></Tagging> on the wire, and
// nothing in the document says whether one <Tag> is a scalar or a one-element
// list. A harness that guessed would send a document S3 cannot read, and the
// refusal would look like validation working.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

type binding struct {
	Method   string             `json:"method"`
	URI      string             `json:"uri"`
	Bind     map[string]string  `json:"bind"`
	XMLLists map[string]xmlList `json:"xml_lists"`
	XMLNames map[string]string  `json:"xml_names"`
}

type auditCase struct {
	Operation   string           `json:"operation"`
	Path        string           `json:"path"`
	Why         string           `json:"why"`
	Value       any              `json:"value"`
	ValueRepeat *auditkit.Repeat `json:"value_repeat,omitempty"`
	Constraint  string           `json:"constraint"`
	HTTP        *binding         `json:"http"`
}

func (b *binding) lists() map[string]xmlList {
	out := map[string]xmlList{}
	for k, v := range b.XMLLists {
		out[k] = v
	}
	return out
}

func s3Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// toXML serialises the payload member using the element names the model gave,
// which is the only way a wrapped list comes out right.
func toXML(root string, v any, path string, b *binding, out *bytes.Buffer) {
	name := root
	if wire, ok := b.XMLNames[path]; ok && path != "" {
		// Strip any namespace prefix: the harness writes the local name, which
		// the validator reads back the same way an attribute would arrive.
		if i := strings.LastIndex(wire, ":"); i >= 0 {
			wire = wire[i+1:]
		}
		name = wire
	}
	switch t := v.(type) {
	case map[string]any:
		fmt.Fprintf(out, "<%s>", name)
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := k
			if path != "" {
				child = path + "." + k
			}
			if list, isList := b.XMLLists[child]; isList {
				els, _ := t[k].([]any)
				if els == nil {
					els = []any{t[k]}
				}
				wrapper := k
				if wire, ok := b.XMLNames[child]; ok {
					wrapper = wire
				}
				if !list.Flattened {
					fmt.Fprintf(out, "<%s>", wrapper)
				}
				for _, el := range els {
					tag := list.Element
					if list.Flattened {
						tag = wrapper
					}
					toXML(tag, el, child+"[]", b, out)
				}
				if !list.Flattened {
					fmt.Fprintf(out, "</%s>", wrapper)
				}
				continue
			}
			toXML(k, t[k], child, b, out)
		}
		fmt.Fprintf(out, "</%s>", name)
	case nil:
		fmt.Fprintf(out, "<%s></%s>", name, name)
	default:
		var esc bytes.Buffer
		xml.EscapeText(&esc, []byte(fmt.Sprint(t)))
		fmt.Fprintf(out, "<%s>%s</%s>", name, esc.String(), name)
	}
}

// call assembles a restXml request from the operation's binding.
func call(t *testing.T, ts *httptest.Server, b *binding, body map[string]any) (int, string) {
	t.Helper()
	path, rawQuery, _ := strings.Cut(b.URI, "?")
	query := url.Values{}
	for _, kv := range strings.Split(rawQuery, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		query.Set(k, v)
	}
	headers := map[string]string{}
	var payload io.Reader

	for name, v := range body {
		switch bind := b.Bind[name]; {
		case bind == "label":
			path = strings.Replace(path, "{"+name+"}", url.PathEscape(fmt.Sprint(deref(v))), 1)
			path = strings.Replace(path, "{"+name+"+}", fmt.Sprint(deref(v)), 1)
		case strings.HasPrefix(bind, "query:"):
			param := strings.TrimPrefix(bind, "query:")
			if list, isList := v.([]any); isList {
				for _, el := range list {
					query.Add(param, fmt.Sprint(el))
				}
				continue
			}
			query.Set(param, fmt.Sprint(deref(v)))
		case strings.HasPrefix(bind, "header:"):
			if list, isList := v.([]any); isList {
				parts := make([]string, len(list))
				for i, el := range list {
					parts[i] = fmt.Sprint(el)
				}
				headers[strings.TrimPrefix(bind, "header:")] = strings.Join(parts, ",")
				continue
			}
			headers[strings.TrimPrefix(bind, "header:")] = fmt.Sprint(deref(v))
		case bind == "payload":
			var buf bytes.Buffer
			toXML(name, v, name, b, &buf)
			payload = bytes.NewReader(buf.Bytes())
		}
	}
	for name, bind := range b.Bind {
		if bind == "label" {
			path = strings.Replace(path, "{"+name+"}", "", 1)
			path = strings.Replace(path, "{"+name+"+}", "", 1)
		}
	}
	target := ts.URL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, _ := http.NewRequest(b.Method, target, payload)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=x")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func deref(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_s3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []auditCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	// A max-length case stores the shape of its padding rather than the run
	// itself: written out, those runs were 35 MB of the 37.5 MB of fixtures.
	for i := range cs {
		cs[i].Value = auditkit.Materialize(cs[i].Value, cs[i].ValueRepeat)
	}
	if len(cs) == 0 {
		t.Fatal("no cases: the audit would pass vacuously")
	}
	return cs
}

func loadRoutes(t *testing.T) map[string]*binding {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "routes_s3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]*binding
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// unexpressibleOn reports whether a case cannot be put on this wire at all —
// the same two rules the restJson1 audits derive, for the same reasons.
func unexpressibleOn(c auditCase, bind map[string]*binding) (string, bool) {
	if s, isStr := c.Value.(string); isStr && strings.HasPrefix(c.HTTP.Bind[c.Path], "header:") {
		for i := 0; i < len(s); i++ {
			if b := s[i]; b < 0x20 && b != '\t' || b == 0x7f {
				return "a header cannot carry a control character", true
			}
		}
	}
	// Omitting a member that IDENTIFIES the operation makes the request a
	// different, valid one — PUT /b/k without ?uploadId is a PutObject, and
	// without x-amz-copy-source a CopyObject is one too. Same phenomenon as the
	// shortened path below, but carried by a header or query parameter rather
	// than a path segment, so it is derived from the route table instead.
	if (c.Value == nil || c.Value == "") && identifies(c.Operation, c.Path) {
		if other, ok := fallbackRoute(c.Operation, c.Path); ok {
			return fmt.Sprintf("without %s the request is %s", wireName(c), other), true
		}
	}
	if c.HTTP.Bind[c.Path] != "label" {
		return "", false
	}
	if c.Value != nil && c.Value != "" {
		return "", false
	}
	path, marks, _ := strings.Cut(c.HTTP.URI, "?")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	last := segs[len(segs)-1]
	if last != "{"+c.Path+"}" && last != "{"+c.Path+"+}" {
		return "", false
	}
	shorter := "/" + strings.Join(segs[:len(segs)-1], "/")
	// Emptying the bucket leaves the service root, and S3 routes "/" by method
	// alone — a sub-resource marker there selects nothing, so GET /?tagging is
	// ListBuckets. The marker comparison below would miss that, because no
	// service-root operation carries one.
	if shorter == "/" {
		for op, b := range bind {
			other, _, _ := strings.Cut(b.URI, "?")
			if op != c.Operation && b.Method == c.HTTP.Method && other == "/" {
				return fmt.Sprintf("%s / is %s; the service root ignores sub-resource markers",
					b.Method, op), true
			}
		}
	}
	for op, b := range bind {
		other, otherMarks, _ := strings.Cut(b.URI, "?")
		// The markers have to match as well as the path: GET /bucket?tagging
		// really is GetBucketTagging, but GET /?tagging is not ListBuckets.
		if op != c.Operation && b.Method == c.HTTP.Method && other == shorter &&
			sameMarks(marks, otherMarks) {
			return fmt.Sprintf("%s %s%s is %s", b.Method, shorter, markSuffix(otherMarks), op), true
		}
	}
	return "", false
}

// wireName is how a member is spelled on the wire.
func wireName(c auditCase) string {
	bind := c.HTTP.Bind[c.Path]
	if i := strings.Index(bind, ":"); i >= 0 {
		return bind[i+1:]
	}
	return c.Path
}

// identifies reports whether a member is part of the operation's identity in
// the route table, rather than merely part of its input.
func identifies(op, member string) bool {
	rt := routeOf(op)
	for _, h := range rt.NeedHeaders {
		if h == memberWire(rt.Header, member) {
			return true
		}
	}
	for _, q := range rt.NeedQuery {
		if q == memberWire(rt.Query, member) {
			return true
		}
	}
	return false
}

func memberWire(m map[string]string, member string) string {
	for wire, name := range m {
		if name == member {
			return wire
		}
	}
	return ""
}

func routeOf(op string) route {
	for _, rt := range routes {
		if rt.Op == op {
			return rt
		}
	}
	return route{}
}

// fallbackRoute names the operation a request actually becomes once the
// identifying member is gone. "Actually" is the whole point: a candidate that
// requires the omitted member, or requires something this request never
// carried, would not match either — naming it would put a confident, wrong
// reason next to a case nobody tested.
func fallbackRoute(op, member string) (string, bool) {
	rt := routeOf(op)
	gone := memberWire(rt.Header, member) + memberWire(rt.Query, member)
	have := map[string]bool{}
	for _, h := range rt.NeedHeaders {
		have[h] = true
	}
	for _, q := range rt.NeedQuery {
		have[q] = true
	}
	delete(have, gone)

	best := ""
	for _, other := range routes {
		if other.Op == op || other.Method != rt.Method ||
			len(other.Segs) != len(rt.Segs) || len(other.Marks) != len(rt.Marks) {
			continue
		}
		same := true
		for k, v := range rt.Marks {
			if other.Marks[k] != v {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		// Everything it asks for must still be on the request.
		satisfied := true
		for _, need := range append(append([]string{}, other.NeedHeaders...), other.NeedQuery...) {
			if !have[need] {
				satisfied = false
				break
			}
		}
		if !satisfied {
			continue
		}
		// The most specific one wins: with uploadId still present, PUT /b/k is
		// UploadPart rather than PutObject.
		if best == "" || len(other.NeedHeaders)+len(other.NeedQuery) >
			len(routeOf(best).NeedHeaders)+len(routeOf(best).NeedQuery) {
			best = other.Op
		}
	}
	return best, best != ""
}

// sameMarks compares two URI query strings ignoring x-id, which AWS's SDK sends
// to make a request cacheable and which identifies nothing.
func sameMarks(a, b string) bool {
	return strings.Join(realMarks(a), "&") == strings.Join(realMarks(b), "&")
}

func realMarks(qs string) []string {
	var out []string
	for _, kv := range strings.Split(qs, "&") {
		if kv == "" || strings.HasPrefix(kv, "x-id=") {
			continue
		}
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}

func markSuffix(qs string) string {
	if m := realMarks(qs); len(m) > 0 {
		return "?" + strings.Join(m, "&")
	}
	return ""
}

func TestS3RejectsWhatTheModelForbids(t *testing.T) {
	ts := s3Server(t)
	f := setUpFixture(t, ts)
	base := baselines(f)
	n := 0
	seq := func() int { n++; return n }

	byOp := map[string][]auditCase{}
	bind := map[string]*binding{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
		bind[c.Operation] = c.HTTP
	}
	for op, b := range loadRoutes(t) {
		if _, ok := bind[op]; !ok {
			bind[op] = b
		}
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	var total, gaps, unbuildable, unwireable int
	for _, op := range ops {
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline", op, len(byOp[op]))
			continue
		}
		t.Run(op, func(t *testing.T) {
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, f, op, "", bl, seq())
			if code, body := call(t, ts, bind[op], bl); code < 200 || code > 299 {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless",
					code, body, op)
			}

			paths := make([]string, 0, len(byOp[op]))
			for _, c := range byOp[op] {
				paths = append(paths, c.Path)
			}
			for _, prefix := range auditkit.Containers(paths) {
				probe := auditkit.DeepCopy(b).(map[string]any)
				if err := auditkit.Apply(probe, exemplars(), prefix+".probe", nil, false); err != nil {
					t.Errorf("container %s: %v", prefix, err)
					continue
				}
				prepare(t, ts, f, op, "", probe, seq())
				if code, resp := call(t, ts, bind[op], probe); code < 200 || code > 299 {
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s\n"+
						"  Every case under it would be refused for the exemplar, not the mutation.",
						prefix, code, resp)
				}
			}

			for _, c := range byOp[op] {
				total++
				if why, ok := unexpressibleOn(c, bind); ok {
					unwireable++
					t.Logf("cannot express %s/%s on the wire: %s", op, c.Path, why)
					continue
				}
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := auditkit.DeepCopy(b).(map[string]any)
					if err := auditkit.Apply(body, exemplars(), c.Path, c.Value, true); err != nil {
						unbuildable++
						t.Fatalf("could not build the case: %v\n"+
							"This is a hole in the harness, not a finding about the service.", err)
					}
					prepare(t, ts, f, op, c.Path, body, seq())

					key := op + "/" + c.Path + "/" + c.Why
					code, resp := call(t, ts, bind[op], body)
					if code >= 200 && code <= 299 {
						gaps++
						if !knownGaps[key] {
							t.Errorf("accepted %s = %v\n  AWS refuses it: %s\n  constraint: %s\n"+
								"  This is a NEW gap. Fix it, or add %q to knownGaps with a reason.",
								c.Path, c.Value, c.Why, c.Constraint, key)
						}
						return
					}
					if code >= 500 {
						t.Fatalf("%s = %d (a refusal should be a 4xx): %s", c.Path, code, resp)
					}
					if knownGaps[key] {
						t.Errorf("%s is enforced now — delete it from knownGaps", key)
					}
				})
			}
		})
	}

	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations "+
		"(%d unbuildable, %d not expressible on this wire)",
		total-gaps-unbuildable-unwireable, total, len(ops), unbuildable, unwireable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
