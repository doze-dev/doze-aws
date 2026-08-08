package apigateway

// The execute-api data plane: serving a deployed API.
//
// This is the half that makes API Gateway worth having. Everything in
// control.go is bookkeeping; this is where a request actually reaches a Lambda.
//
// Routing follows API Gateway's own precedence, which is not simply "first
// match wins":
//
//  1. an exact literal segment beats a path parameter;
//  2. a path parameter `{id}` beats a greedy proxy;
//  3. a greedy `{proxy+}` matches the whole remaining path, and the LONGEST
//     matching prefix wins so a nested proxy beats a root one.
//
// Getting that order wrong means `/users/me` silently dispatches to the
// `/users/{id}` handler, which is the sort of bug you only find in production.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// serveExecute handles /_aws/execute-api/{apiId}/{stage}/{path...}.
func (s *Server) serveExecute(w http.ResponseWriter, r *http.Request, rest string) {
	apiID, remainder, _ := strings.Cut(rest, "/")
	stage, path, _ := strings.Cut(remainder, "/")
	if apiID == "" || stage == "" {
		writeExecuteError(w, 404, "Not Found", "the execute-api path needs an api id and a stage")
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	api, err := s.store.Get(apiID)
	if err != nil {
		writeExecuteError(w, 403, "Forbidden", "")
		return
	}
	if _, ok := api.Stages[stage]; !ok {
		writeExecuteError(w, 404, "Not Found",
			"Invalid stage identifier specified: "+stage)
		return
	}

	res, params, ok := matchResource(api, path)
	if !ok {
		writeExecuteError(w, 403, "Missing Authentication Token", "")
		return
	}
	method := s.methodFor(res, r.Method)
	if method == nil {
		writeExecuteError(w, 403, "Missing Authentication Token", "")
		return
	}
	if method.Integration == nil {
		writeExecuteError(w, 500, "Internal server error",
			"No integration defined for method")
		return
	}

	s.logf("apigateway: %s %s -> %s %s", r.Method, path, method.Integration.Type, method.Integration.URI)
	s.invokeIntegration(w, r, api, stage, res, method, params, path)
}

// methodFor picks the method that serves a verb, honouring ANY.
func (s *Server) methodFor(res *Resource, verb string) *Method {
	if m, ok := res.Methods[strings.ToUpper(verb)]; ok {
		return m
	}
	return res.Methods["ANY"]
}

// ---- path matching ----

// matchResource resolves a request path against the API's resource tree,
// returning the matched resource and any captured path parameters.
func matchResource(api *RestAPI, path string) (*Resource, map[string]string, bool) {
	want := splitPath(path)

	var bestExact *Resource
	var bestExactParams map[string]string
	var bestProxy *Resource
	var bestProxyParams map[string]string
	bestProxyDepth := -1

	for _, res := range api.Resources {
		have := splitPath(res.Path)
		params, proxy, ok := matchSegments(have, want)
		if !ok {
			continue
		}
		if !proxy {
			// An exact-length match; prefer the one with fewer parameters, so
			// a literal segment beats a path parameter.
			if bestExact == nil || paramCount(have) < paramCount(splitPath(bestExact.Path)) {
				bestExact, bestExactParams = res, params
			}
			continue
		}
		// A greedy match: the deepest (longest) prefix wins.
		if len(have) > bestProxyDepth {
			bestProxy, bestProxyParams, bestProxyDepth = res, params, len(have)
		}
	}
	if bestExact != nil {
		return bestExact, bestExactParams, true
	}
	if bestProxy != nil {
		return bestProxy, bestProxyParams, true
	}
	return nil, nil, false
}

// matchSegments compares a resource's path segments against a request's.
// proxy reports whether the match consumed the remainder via a {proxy+}.
func matchSegments(have, want []string) (params map[string]string, proxy bool, ok bool) {
	params = map[string]string{}
	for i, seg := range have {
		// A greedy parameter swallows everything left, including nothing.
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "+}") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "+}")
			if i > len(want) {
				return nil, false, false
			}
			params[name] = strings.Join(want[min(i, len(want)):], "/")
			return params, true, true
		}
		if i >= len(want) {
			return nil, false, false
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params[strings.Trim(seg, "{}")] = want[i]
			continue
		}
		if seg != want[i] {
			return nil, false, false
		}
	}
	if len(want) != len(have) {
		return nil, false, false
	}
	return params, false, true
}

func paramCount(segs []string) int {
	n := 0
	for _, s := range segs {
		if strings.HasPrefix(s, "{") {
			n++
		}
	}
	return n
}

// ---- integrations ----

func (s *Server) invokeIntegration(w http.ResponseWriter, r *http.Request, api *RestAPI,
	stage string, res *Resource, method *Method, params map[string]string, path string) {

	integ := method.Integration
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))

	switch integ.Type {
	case "AWS_PROXY":
		s.invokeLambdaProxy(w, r, api, stage, res, integ, params, path, body)
	case "MOCK":
		s.invokeMock(w, method, integ)
	case "HTTP", "HTTP_PROXY":
		s.invokeHTTP(w, r, integ, params, body)
	case "AWS":
		// A non-proxy AWS integration is a Velocity mapping template over an
		// arbitrary AWS action. Emulating VTL badly would silently produce the
		// wrong request, so it is refused instead.
		writeExecuteError(w, 500, "Internal server error",
			"doze-aws implements AWS_PROXY, MOCK, HTTP and HTTP_PROXY integrations; "+
				"non-proxy AWS integrations need Velocity mapping templates, which are not emulated")
	default:
		writeExecuteError(w, 500, "Internal server error",
			"unsupported integration type "+integ.Type)
	}
}

// proxyEvent is the API Gateway v1 proxy event shape.
type proxyEvent struct {
	Resource                        string              `json:"resource"`
	Path                            string              `json:"path"`
	HTTPMethod                      string              `json:"httpMethod"`
	Headers                         map[string]string   `json:"headers"`
	MultiValueHeaders               map[string][]string `json:"multiValueHeaders"`
	QueryStringParameters           map[string]string   `json:"queryStringParameters"`
	MultiValueQueryStringParameters map[string][]string `json:"multiValueQueryStringParameters"`
	PathParameters                  map[string]string   `json:"pathParameters"`
	StageVariables                  map[string]string   `json:"stageVariables"`
	RequestContext                  map[string]any      `json:"requestContext"`
	Body                            string              `json:"body"`
	IsBase64Encoded                 bool                `json:"isBase64Encoded"`
}

func (s *Server) invokeLambdaProxy(w http.ResponseWriter, r *http.Request, api *RestAPI,
	stage string, res *Resource, integ *Integration, params map[string]string, path string, body []byte) {

	fn := lambdaFromURI(integ.URI)
	if fn == "" {
		writeExecuteError(w, 500, "Internal server error",
			"cannot tell which Lambda function the integration URI names: "+integ.URI)
		return
	}

	ev := proxyEvent{
		Resource: res.Path, Path: path, HTTPMethod: strings.ToUpper(r.Method),
		Headers: map[string]string{}, MultiValueHeaders: map[string][]string{},
		PathParameters: nilIfEmpty(params),
		RequestContext: map[string]any{
			"resourceId":   res.ID,
			"resourcePath": res.Path,
			"httpMethod":   strings.ToUpper(r.Method),
			"path":         "/" + stage + path,
			"stage":        stage,
			"apiId":        api.ID,
			"accountId":    awsident.AccountID,
			"requestId":    requestID(),
			"protocol":     "HTTP/1.1",
			"identity": map[string]any{
				"sourceIp":  sourceIP(r),
				"userAgent": r.Header.Get("User-Agent"),
			},
		},
	}
	if st, ok := api.Stages[stage]; ok && len(st.Variables) > 0 {
		ev.StageVariables = st.Variables
	}
	for k, v := range r.Header {
		ev.Headers[k] = v[len(v)-1]
		ev.MultiValueHeaders[k] = v
	}
	if q := r.URL.Query(); len(q) > 0 {
		ev.QueryStringParameters = map[string]string{}
		ev.MultiValueQueryStringParameters = map[string][]string{}
		for k, v := range q {
			ev.QueryStringParameters[k] = v[len(v)-1]
			ev.MultiValueQueryStringParameters[k] = v
		}
	}
	// A body that is not valid UTF-8 travels base64, exactly as in AWS.
	if len(body) > 0 {
		if utf8.Valid(body) {
			ev.Body = string(body)
		} else {
			ev.Body = base64.StdEncoding.EncodeToString(body)
			ev.IsBase64Encoded = true
		}
	}

	payload, err := json.Marshal(ev)
	if err != nil {
		writeExecuteError(w, 500, "Internal server error", err.Error())
		return
	}
	out, err := peercall.LambdaInvoke(s.peers, fn, payload)
	if err != nil {
		writeExecuteError(w, 502, "Internal server error",
			"invoking "+fn+": "+err.Error())
		return
	}
	writeProxyResponse(w, out, fn)
}

// writeProxyResponse turns a Lambda's return value into an HTTP response.
//
// A handler that returns a malformed shape produces a 502 in AWS, and the same
// here — a proxy integration's contract is the response object, so a handler
// that ignores it has genuinely failed.
func writeProxyResponse(w http.ResponseWriter, out []byte, fn string) {
	var resp struct {
		StatusCode        int                 `json:"statusCode"`
		Headers           map[string]string   `json:"headers"`
		MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
		Body              string              `json:"body"`
		IsBase64Encoded   bool                `json:"isBase64Encoded"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.StatusCode == 0 {
		writeExecuteError(w, 502, "Internal server error",
			"function "+fn+" did not return a proxy-integration response "+
				`({"statusCode":…,"body":…}); got: `+truncate(string(out), 200))
		return
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	for k, vals := range resp.MultiValueHeaders {
		w.Header().Del(k)
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	body := []byte(resp.Body)
	if resp.IsBase64Encoded {
		if decoded, err := base64.StdEncoding.DecodeString(resp.Body); err == nil {
			body = decoded
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// invokeMock answers from the integration's own response templates, which is
// what MOCK integrations are for — CORS preflights, mostly.
func (s *Server) invokeMock(w http.ResponseWriter, method *Method, integ *Integration) {
	status := "200"
	var chosen *IntegrationResponse
	for code, ir := range integ.Responses {
		if chosen == nil || code < status {
			chosen, status = ir, code
		}
	}
	code := 200
	if chosen != nil {
		code = atoiOr(chosen.StatusCode, 200)
	}
	// Static response parameters become headers; the mapping syntax is
	// 'integration.response.header.X' -> "'literal'".
	if chosen != nil {
		for k, v := range chosen.ResponseParameters {
			if name, ok := strings.CutPrefix(k, "method.response.header."); ok {
				w.Header().Set(name, strings.Trim(v, "'"))
			}
		}
	}
	body := ""
	if chosen != nil {
		if t, ok := chosen.ResponseTemplates["application/json"]; ok {
			body = t
		}
	}
	if body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(code)
	w.Write([]byte(body))
}

// invokeHTTP forwards to a real HTTP endpoint.
func (s *Server) invokeHTTP(w http.ResponseWriter, r *http.Request, integ *Integration,
	params map[string]string, body []byte) {

	target := expandURI(integ.URI, params)
	method := integ.HTTPMethod
	if method == "" {
		method = r.Method
	}
	if q := r.URL.RawQuery; q != "" {
		if strings.Contains(target, "?") {
			target += "&" + q
		} else {
			target += "?" + q
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target, strings.NewReader(string(body)))
	if err != nil {
		writeExecuteError(w, 500, "Internal server error", err.Error())
		return
	}
	for k, v := range r.Header {
		if strings.EqualFold(k, "Host") {
			continue
		}
		for _, item := range v {
			req.Header.Add(k, item)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeExecuteError(w, 502, "Internal server error", err.Error())
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		for _, item := range v {
			w.Header().Add(k, item)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ---- helpers ----

// lambdaFromURI extracts the function name from the integration URI shape
// AWS uses:
//
//	arn:aws:apigateway:<region>:lambda:path/2015-03-31/functions/<fnArn>/invocations
func lambdaFromURI(uri string) string {
	i := strings.Index(uri, "/functions/")
	if i < 0 {
		return ""
	}
	rest := uri[i+len("/functions/"):]
	arn, _, _ := strings.Cut(rest, "/invocations")
	// The embedded value is a function ARN, possibly with a qualifier.
	if j := strings.Index(arn, ":function:"); j >= 0 {
		name := arn[j+len(":function:"):]
		name, _, _ = strings.Cut(name, ":")
		return name
	}
	return arn
}

// expandURI substitutes {param} placeholders in an HTTP integration URI.
func expandURI(uri string, params map[string]string) string {
	for k, v := range params {
		uri = strings.ReplaceAll(uri, "{"+k+"}", v)
	}
	return uri
}

// writeExecuteError renders the JSON error shape API Gateway returns on the
// data plane, which is a bare {"message": ...}.
func writeExecuteError(w http.ResponseWriter, status int, message, detail string) {
	if detail != "" {
		message = detail
	}
	body, _ := json.Marshal(map[string]string{"message": message})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-ErrorType", http.StatusText(status))
	w.WriteHeader(status)
	w.Write(body)
}

func sourceIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
