package apigateway

// The HTTP API data plane. An HTTP API answers at the same execute-api
// addresses a REST API does, with one difference AWS has too: the $default
// stage is served at the API's root, with no stage segment, and a named
// stage under its name.
//
// Route selection is by key — "GET /items/{id}", "ANY /{proxy+}",
// "$default" — with HTTP API's precedence: a literal segment beats a path
// parameter, a parameter beats a greedy proxy, an exact method beats ANY,
// and $default catches whatever nothing else matched. The integration is a
// Lambda proxy (payload 1.0 or 2.0) or an HTTP proxy; CORS is answered from
// the API's configuration.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/httpevent"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// v2Call is one data-plane request once its route is known.
type v2Call struct {
	api    *RestAPI
	stage  *Stage
	route  *V2Route
	params map[string]string
	path   string // the path within the stage
	rl     *requestLog
}

// serveExecuteV2 serves an HTTP API. remainder is what followed the api id
// in the URL: "<stage>/<path>", or "<path>" for the $default stage.
func (s *Server) serveExecuteV2(w http.ResponseWriter, r *http.Request, api *RestAPI, remainder string) {
	stageName, path := v2ResolveStage(api, remainder)
	st, ok := api.Stages[stageName]
	if !ok || st.DeploymentID == "" {
		// A stage nothing has been deployed to answers 404, as on AWS: the
		// console's "deploy by hand" option is meaningful only if it does.
		writeExecuteError(w, 404, "Not Found", "")
		return
	}
	rl := &requestLog{
		id: requestID(), started: s.now(), apiID: api.ID, stage: stageName,
		method: strings.ToUpper(r.Method), path: path, protocol: r.Proto,
		sourceIP: sourceIP(r), userAgent: r.UserAgent(), query: r.URL.RawQuery, headers: r.Header,
	}
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		rl.status, rl.respLength = sw.status, sw.length
		s.logs.record(st, rl)
	}()
	w = sw

	// CORS: a preflight the configuration admits is answered here; any
	// other request is routed, with the headers on whatever the route
	// answers when its origin is allowed.
	if api.CORS != nil {
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" && v2Preflight(w, r, api.CORS) {
			return
		}
		v2CORSHeaders(w.Header(), r, api.CORS)
	}

	route, params, ok := matchV2Route(api, r.Method, path)
	if !ok {
		rl.errMessage = "Not Found"
		writeExecuteError(w, 404, "Not Found", "")
		return
	}
	rl.resource = route.RouteKey
	call := &v2Call{api: api, stage: st, route: route, params: params, path: path, rl: rl}

	var authorizer map[string]any
	if route.AuthorizationType == "CUSTOM" {
		a := api.V2Authorizers[route.AuthorizerID]
		if a == nil {
			rl.errMessage = "the route's authorizer no longer exists"
			writeExecuteError(w, 500, "Internal Server Error", "")
			return
		}
		ctx, denial := s.v2Authorize(r, call, a)
		if denial != nil {
			rl.errMessage = denial.message
			writeExecuteError(w, denial.status, denial.message, "")
			return
		}
		authorizer = ctx
	}

	integID := strings.TrimPrefix(route.Target, "integrations/")
	integ := api.V2Integrations[integID]
	if integ == nil {
		rl.errMessage = "No integration defined for route"
		writeExecuteError(w, 500, "Internal Server Error", "")
		return
	}
	rl.integType, rl.integURI = integ.Type, integ.URI
	body, _ := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	rl.body = body
	s.logf("apigateway: %s %s -> %s %s (HTTP API %s)", r.Method, path, integ.Type, integ.URI, api.ID)
	switch integ.Type {
	case "AWS_PROXY":
		s.v2InvokeLambda(w, r, call, integ, body, authorizer)
	case "HTTP_PROXY":
		v1 := &Integration{Type: "HTTP_PROXY", URI: integ.URI}
		if integ.Method != "" && integ.Method != "ANY" {
			v1.HTTPMethod = integ.Method
		}
		rl.integBody, rl.integStart = body, s.now()
		s.invokeHTTP(w, r, v1, params, body)
		rl.integEnd = s.now()
	default:
		rl.errMessage = "unsupported integration type " + integ.Type
		writeExecuteError(w, 500, "Internal Server Error", "")
	}
}

// v2ResolveStage splits the remainder into the stage and the path: a leading
// segment naming a stage wins, "$default" may be spelled out, and otherwise
// the whole remainder is the path on the $default stage.
func v2ResolveStage(api *RestAPI, remainder string) (stage, path string) {
	first, rest, _ := strings.Cut(remainder, "/")
	if first != "" {
		if _, ok := api.Stages[first]; ok {
			return first, "/" + rest
		}
	}
	return "$default", "/" + remainder
}

// matchV2Route picks the route for a method and path.
func matchV2Route(api *RestAPI, method, path string) (*V2Route, map[string]string, bool) {
	method = strings.ToUpper(method)
	want := splitPath(path)
	type candidate struct {
		route  *V2Route
		params map[string]string
		proxy  bool
		depth  int
		nparam int
		anyM   bool
	}
	var best *candidate
	var fallback *V2Route
	better := func(c, b *candidate) bool {
		if b == nil {
			return true
		}
		if c.proxy != b.proxy {
			return !c.proxy
		}
		if c.proxy && c.depth != b.depth {
			return c.depth > b.depth
		}
		// At equal depth a literal segment beats a parameter, for proxy
		// routes as for exact ones.
		if c.nparam != b.nparam {
			return c.nparam < b.nparam
		}
		if c.anyM != b.anyM {
			return !c.anyM
		}
		// Two keys that match the same request equally are the same route
		// spelled differently (creation refuses that); keep the pick stable.
		return c.route.RouteKey < b.route.RouteKey
	}
	for _, rt := range api.V2Routes {
		if rt.RouteKey == "$default" {
			fallback = rt
			continue
		}
		m, p := routeKeyParts(rt.RouteKey)
		if m != method && m != "ANY" {
			continue
		}
		have := splitPath(p)
		params, proxy, ok := matchSegments(have, want)
		if !ok {
			continue
		}
		c := &candidate{route: rt, params: params, proxy: proxy, depth: len(have), nparam: paramCount(have), anyM: m == "ANY"}
		if better(c, best) {
			best = c
		}
	}
	if best != nil {
		return best.route, best.params, true
	}
	if fallback != nil {
		return fallback, map[string]string{}, true
	}
	return nil, nil, false
}

// ---- CORS ----

func v2OriginAllowed(c *CORSConfig, origin string) bool {
	if origin == "" {
		return false
	}
	for _, o := range c.AllowOrigins {
		if o == "*" || strings.EqualFold(o, origin) || globMatch(o, origin) {
			return true
		}
	}
	return false
}

// v2AllowOrigin is the Access-Control-Allow-Origin value: the literal "*"
// when the configuration is "*", the request's origin when a pattern or an
// exact entry admitted it — as AWS answers.
func v2AllowOrigin(c *CORSConfig, origin string) string {
	for _, o := range c.AllowOrigins {
		if o == "*" {
			return "*"
		}
	}
	return origin
}

func v2CORSHeaders(h http.Header, r *http.Request, c *CORSConfig) {
	origin := r.Header.Get("Origin")
	if !v2OriginAllowed(c, origin) {
		return
	}
	h.Set("Access-Control-Allow-Origin", v2AllowOrigin(c, origin))
	if c.AllowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(c.ExposeHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(c.ExposeHeaders, ","))
	}
}

// v2Preflight answers an OPTIONS preflight from the configuration when the
// origin and method are allowed: 204 with the allow headers. It reports
// false when the configuration does not match, and the request is then
// routed like any other — an OPTIONS route may answer it, or nothing does.
func v2Preflight(w http.ResponseWriter, r *http.Request, c *CORSConfig) bool {
	origin := r.Header.Get("Origin")
	method := strings.ToUpper(r.Header.Get("Access-Control-Request-Method"))
	methodOK := len(c.AllowMethods) == 0
	for _, m := range c.AllowMethods {
		if m == "*" || strings.EqualFold(m, method) {
			methodOK = true
		}
	}
	if !v2OriginAllowed(c, origin) || !methodOK {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", v2AllowOrigin(c, origin))
	if len(c.AllowMethods) > 0 {
		h.Set("Access-Control-Allow-Methods", strings.Join(c.AllowMethods, ","))
	}
	if len(c.AllowHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", strings.Join(c.AllowHeaders, ","))
	} else if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
		h.Set("Access-Control-Allow-Headers", req)
	}
	if c.AllowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(c.ExposeHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(c.ExposeHeaders, ","))
	}
	if c.MaxAge != nil {
		h.Set("Access-Control-Max-Age", strconv.Itoa(*c.MaxAge))
	}
	w.WriteHeader(204)
	return true
}

// ---- Lambda proxy ----

// v2LambdaName reads the function from an integrationUri, which for an
// HTTP API may be the bare function ARN or the invoke URI.
func v2LambdaName(uri string) string {
	if fn := lambdaFromURI(uri); fn != "" {
		return fn
	}
	if j := strings.Index(uri, ":function:"); j >= 0 {
		name := uri[j+len(":function:"):]
		name, _, _ = strings.Cut(name, ":")
		return name
	}
	return ""
}

// v2Event builds the event for the integration's payload format version.
func (s *Server) v2Event(r *http.Request, call *v2Call, version string, body []byte, authorizer map[string]any) []byte {
	if version == "1.0" {
		res := &Resource{ID: call.route.ID, Path: v2RoutePath(call.route)}
		ev := s.buildProxyEvent(r, call.api, call.stage.Name, res, call.params, call.path, body, nil, call.rl.id)
		ev.RequestContext["routeKey"] = call.route.RouteKey
		// requestContext.path carries the stage prefix for a named stage and
		// nothing for $default, as AWS's 1.0 example shows.
		ev.RequestContext["path"] = v2StagePath(call.stage.Name, call.path)
		ev.RequestContext["domainName"] = call.api.ID + ".execute-api." + awsident.Region + ".amazonaws.com"
		if len(authorizer) > 0 {
			ev.RequestContext["authorizer"] = authorizer
		}
		var m map[string]any
		raw, _ := json.Marshal(ev)
		json.Unmarshal(raw, &m)
		m["version"] = "1.0"
		out, _ := json.Marshal(m)
		return out
	}
	var auth map[string]any
	if len(authorizer) > 0 {
		auth = map[string]any{"lambda": authorizer}
	}
	out, _ := json.Marshal(httpevent.Event(httpevent.Request{
		// rawPath includes the stage for a named stage (what the serverless
		// adapters strip as the base path) and is the bare path on $default.
		R: r, Path: v2StagePath(call.stage.Name, call.path), Body: body,
		RouteKey: call.route.RouteKey, Stage: call.stage.Name, APIID: call.api.ID,
		DomainName: call.api.ID + ".execute-api." + awsident.Region + ".amazonaws.com",
		AccountID:  awsident.AccountID, RequestID: call.rl.id, Now: s.now(),
		PathParameters: call.params, StageVariables: call.stage.Variables, Authorizer: auth,
	}))
	return out
}

// v2StagePath is the path as the client sent it: under the stage name for a
// named stage, bare for $default.
func v2StagePath(stage, path string) string {
	if stage == "$default" {
		return path
	}
	return "/" + stage + path
}

// v2RoutePath is the path template of a route key, "/" for $default.
func v2RoutePath(rt *V2Route) string {
	_, p := routeKeyParts(rt.RouteKey)
	if p == "" {
		return "/"
	}
	return p
}

func (s *Server) v2InvokeLambda(w http.ResponseWriter, r *http.Request, call *v2Call, integ *V2Integration, body []byte, authorizer map[string]any) {
	rl := call.rl
	fn := v2LambdaName(integ.URI)
	if fn == "" {
		rl.errMessage = "cannot tell which Lambda function the integration URI names: " + integ.URI
		writeExecuteError(w, 500, "Internal Server Error", "")
		return
	}
	payload := s.v2Event(r, call, integ.PayloadFormatVersion, body, authorizer)
	rl.integBody, rl.integStart = payload, s.now()
	out, err := peercall.LambdaInvoke(peers.WithPrincipal(r.Context(), "apigateway", V2APIARN(call.api.ID)), s.peers, fn, payload)
	rl.integEnd, rl.integResp = s.now(), out
	if err != nil {
		rl.errMessage = "invoking " + fn + ": " + err.Error()
		writeExecuteError(w, 500, "Internal Server Error", "")
		return
	}
	if integ.PayloadFormatVersion == "1.0" {
		if msg := writeProxyResponse(w, out, fn); msg != "" {
			rl.errMessage = msg
		}
		return
	}
	httpevent.WriteResponse(w, out)
}
