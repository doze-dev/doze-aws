package apigateway

// Resolving a request to an operation, and validating it.
//
// Every other service here names its operation on the wire — a target header or
// an Action parameter — so validation is a map lookup. API Gateway names it
// nowhere: the operation IS the method and the path. So before the constraint
// table can be consulted, the request has to be matched against the route table
// generated from AWS's model, and the input reassembled from the three places
// restJson1 scatters it: the path labels, the query string, and the body.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// codeREST is what API Gateway calls a refused input. Not ValidationException,
// which is the awsJson spelling, and not ValidationError, which is Query's.
const codeREST = "BadRequestException"

// matchRoute finds the operation a request addresses and pulls out its path
// labels. Routes are ordered most-specific first, so the first match wins.
func matchRoute(method, path string) (op string, labels map[string]string, ok bool) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) == 1 && segs[0] == "" {
		return "", nil, false
	}
	// The HTTP API (v2) surface has its own table; the operation names
	// overlap (both have a CreateAuthorizer), so the path prefix decides.
	table := routes
	if segs[0] == "v2" {
		table = routesV2
	}
	for _, rt := range table {
		if rt.Method != method || len(rt.Segs) != len(segs) {
			continue
		}
		got := map[string]string{}
		matched := true
		for i, want := range rt.Segs {
			if want == "" {
				got[rt.Labels[i]] = segs[i]
				continue
			}
			if want != segs[i] {
				matched = false
				break
			}
		}
		if matched {
			return rt.Op, got, true
		}
	}
	return "", nil, false
}

// validateControl reassembles the operation's input and walks its constraints.
// It returns the body it read so the handler can still decode it — reading a
// request body consumes it, and a validator that ate the input would break
// every operation it was meant to protect.
func validateControl(r *http.Request) (body []byte, aerr *awshttp.APIError) {
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 16<<20))
		r.Body.Close()
		if err != nil {
			return nil, errBadRequest("read request body: %v", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	op, labels, ok := matchRoute(r.Method, r.URL.Path)
	if !ok {
		return body, nil
	}
	table := constraintTables[op]
	if isV2Path(r.URL.Path) {
		table = constraintTablesV2[op]
	}
	if len(table) == 0 {
		return body, nil
	}

	input := map[string]any{}
	if len(body) > 0 {
		// A body that is not an object is the handler's problem to report, not
		// the validator's — it has no members to check.
		if json.Unmarshal(body, &input) != nil {
			input = map[string]any{}
		}
	}
	for name, value := range labels {
		// An omitted path label is not an absent field, it is an empty segment:
		// GET /restapis//resources. Recording it as present would let every
		// @required label pass, since a label is never missing from the map.
		if value == "" {
			continue
		}
		input[name] = value
	}
	rt := routeFor(op, isV2Path(r.URL.Path))
	query := r.URL.Query()
	for param, member := range rt.Query {
		// Presence, not non-emptiness: "?Qualifier=" supplies the member as an
		// empty string, which a minimum length must refuse. Testing the value
		// instead would make every such case pass vacuously.
		v, ok := query[param]
		if !ok || len(v) == 0 {
			continue
		}
		if rt.QueryList[member] {
			// A repeated parameter is a list, and its constraints are on the
			// elements: TagKeys[], not TagKeys.
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
		if v, ok := r.Header[http.CanonicalHeaderKey(name)]; ok && len(v) > 0 {
			input[member] = v[0]
		}
	}
	return body, modelcheck.ValidateMapAs(input, table, codeREST)
}

// isV2Path reports whether a control-plane path is the HTTP API surface.
func isV2Path(path string) bool { return strings.HasPrefix(path, "/v2/") }

func routeFor(op string, v2 bool) route {
	table := routes
	if v2 {
		table = routesV2
	}
	for _, rt := range table {
		if rt.Op == op {
			return rt
		}
	}
	return route{}
}

// OperationFor reports the API Gateway operation a request addresses, or ""
// when no route matches. Exported for the console's traffic classifier — see
// the note on s3.OperationFor.
func OperationFor(r *http.Request) string {
	op, _, ok := matchRoute(r.Method, r.URL.Path)
	if !ok {
		return ""
	}
	return op
}
