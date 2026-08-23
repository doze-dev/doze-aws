package apigateway

// Rejection parity for API Gateway, driven by the cases dzaudit derives from
// AWS's own service model (`dzaudit cases api-gateway`).
//
// Same rule as everywhere: a request refused for the WRONG reason looks exactly
// like a pass, so every case is a mutation of a baseline this test first proves
// the service accepts.
//
// The wire is what is new. The other audits here send one encoded body to one
// endpoint; restJson1 spreads a single operation's input across the method, the
// URI, the query string and the body, and there is no header naming the
// operation at all. So each case carries the binding dzaudit read out of the
// model, and the harness assembles the request from it — a member bound with
// @httpLabel is substituted into the URI template, @httpQuery goes on the query
// string, and everything else goes in the JSON body.
//
// That distinction is load-bearing and silent when wrong: a label left in the
// body produces a 404 from the router, which is a 4xx and reads exactly like a
// validation refusal.

import (
	"bytes"
	"encoding/json"
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

type httpBinding struct {
	Method string            `json:"method"`
	URI    string            `json:"uri"`
	Bind   map[string]string `json:"bind"`
}

type auditCase struct {
	Operation  string       `json:"operation"`
	Path       string       `json:"path"`
	Why        string       `json:"why"`
	Value      any          `json:"value"`
	Constraint string       `json:"constraint"`
	HTTP       *httpBinding `json:"http"`
}

func apigwServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

// call assembles a restJson1 request from the operation's binding: labels into
// the URI, @httpQuery members onto the query string, the rest into the body.
func call(t *testing.T, ts *httptest.Server, b *httpBinding, body map[string]any) (int, string) {
	t.Helper()
	uri := b.URI
	query := url.Values{}
	headers := map[string]string{}
	payload := map[string]any{}

	for name, v := range body {
		switch bind := b.Bind[name]; {
		case bind == "label":
			// A label the case omitted becomes an empty segment, which is what
			// AWS's own router sees when a caller leaves it out.
			uri = strings.Replace(uri, "{"+name+"}", url.PathEscape(fmt.Sprint(deref(v))), 1)
			uri = strings.Replace(uri, "{"+name+"+}", fmt.Sprint(deref(v)), 1)
		case strings.HasPrefix(bind, "query:"):
			param := strings.TrimPrefix(bind, "query:")
			// A list-valued query member is repeated, not joined: ?tagKeys=a&tagKeys=b.
			if list, isList := v.([]any); isList {
				for _, el := range list {
					query.Add(param, fmt.Sprint(el))
				}
				continue
			}
			query.Set(param, fmt.Sprint(deref(v)))
		case strings.HasPrefix(bind, "header:"):
			headers[strings.TrimPrefix(bind, "header:")] = fmt.Sprint(deref(v))
		default:
			payload[name] = v
		}
	}
	// Any label the request did not supply at all still has to leave the
	// template, or the path would contain a literal "{restApiId}".
	for name, bind := range b.Bind {
		if bind == "label" {
			uri = strings.Replace(uri, "{"+name+"}", "", 1)
			uri = strings.Replace(uri, "{"+name+"+}", "", 1)
		}
	}
	if len(query) > 0 {
		uri += "?" + query.Encode()
	}

	var rdr io.Reader
	if len(payload) > 0 {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(b.Method, ts.URL+uri, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/apigateway/aws4_request, SignedHeaders=host, Signature=x")
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
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_apigateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []auditCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("no cases: the audit would pass vacuously")
	}
	for _, c := range cs {
		if c.HTTP == nil {
			t.Fatalf("%s/%s has no HTTP binding: the harness cannot build the request",
				c.Operation, c.Path)
		}
	}
	return cs
}

// loadRoutes reads every implemented operation's binding, including those with
// no constraints. The cases cannot supply this: an operation with nothing to
// violate produces none, and the shortening check below needs the whole set.
func loadRoutes(t *testing.T) map[string]*httpBinding {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "routes_apigateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]*httpBinding
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

// unexpressibleOn reports whether a case cannot be put on this wire at all.
//
// Emptying the LAST label of a URI does not produce an invalid request: it
// produces a shorter path, and if another operation's template is exactly that
// path, the request simply becomes that operation. GET /2015-03-31/functions
// is ListFunctions, not a GetFunction missing its name. There is nothing for
// the service to refuse and AWS does not refuse it either, so these are counted
// separately rather than as gaps.
//
// Derived from the bindings rather than listed by hand, so an operation added
// to the audit later cannot quietly acquire a case that tests nothing.
func unexpressibleOn(c auditCase, bind map[string]*httpBinding) (string, bool) {
	// A header cannot carry a control character: HTTP forbids it, and Go's
	// transport refuses to send the request at all, so the service never sees
	// the value. AWS's own SDK is bound by the same rule.
	if s, isStr := c.Value.(string); isStr && strings.HasPrefix(c.HTTP.Bind[c.Path], "header:") {
		for i := 0; i < len(s); i++ {
			if b := s[i]; b < 0x20 && b != '\t' || b == 0x7f {
				return "a header cannot carry a control character", true
			}
		}
	}
	if c.HTTP.Bind[c.Path] != "label" {
		return "", false
	}
	// Only an empty value shortens the path: any other violation still fills
	// the segment, and the request stays the operation it was.
	if c.Value != nil && c.Value != "" {
		return "", false
	}
	segs := strings.Split(strings.Trim(c.HTTP.URI, "/"), "/")
	if segs[len(segs)-1] != "{"+c.Path+"}" {
		return "", false
	}
	shorter := "/" + strings.Join(segs[:len(segs)-1], "/")
	for op, b := range bind {
		if op != c.Operation && b.Method == c.HTTP.Method && b.URI == shorter {
			return fmt.Sprintf("%s %s is %s", b.Method, shorter, op), true
		}
	}
	return "", false
}

func TestAPIGatewayRejectsWhatTheModelForbids(t *testing.T) {
	ts := apigwServer(t)
	f := setUpFixture(t, ts)
	base := baselines(f)
	n := 0
	seq := func() int { n++; return n }

	byOp := map[string][]auditCase{}
	bind := map[string]*httpBinding{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
		bind[c.Operation] = c.HTTP
	}
	// The shortening check needs every implemented operation, not only the ones
	// with cases.
	all := loadRoutes(t)
	for op, b := range all {
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
