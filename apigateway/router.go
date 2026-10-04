package apigateway

// API Gateway's control planes, routed by the tables generated from AWS's
// model: routes for the REST API (v1) and routesV2 for the HTTP API (v2).
//
// Each table becomes a chi router, with what the model leaves out added beside
// it, and a handler per operation. A request is matched once; validation and
// the handler read the operation from its context. The data plane — a
// deployed API's own paths — is not here: it matches a user's resources by
// AWS's precedence rules (execute.go, v2_execute.go), which are not chi's.

import (
	"net/http"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/restroute"
)

// greedyLabels are labels that take the rest of the path, because their value
// may hold a slash a client did not escape: an ARN, and a route key like
// "GET /items/{id}".
var greedyLabels = map[string]bool{"resourceArn": true, "routeKey": true}

// labelValue is a path label's value; a greedy one is read from the wildcard.
func labelValue(r *http.Request, name string) string {
	if greedyLabels[name] {
		return restroute.Wildcard(r)
	}
	return restroute.Param(r, name)
}

func patternOf(rt route) string {
	greedy := len(rt.Labels) > 0 && greedyLabels[rt.Labels[len(rt.Labels)-1]]
	return restroute.Pattern(rt.Segs, rt.Labels, greedy)
}

// refused is a family doze-aws recognises and does not model. Refusing it by
// name beats a bare 404 that looks like a routing bug.
func refused(msg string) restroute.Handler {
	return func(http.ResponseWriter, *http.Request) *awshttp.APIError {
		return awshttp.Errf(501, "NotImplemented", "%s", msg)
	}
}

// family routes a path and everything beneath it, for every method, to a
// refusal.
func family(pattern, msg string) []restroute.Route {
	h := refused(msg)
	return []restroute.Route{
		{Pattern: pattern, Handler: h},
		{Pattern: pattern + "/*", Handler: h},
	}
}

// v1Specs is the REST API's routes, without handlers.
var v1Specs = sync.OnceValue(func() []restroute.Route {
	var out []restroute.Route
	for _, rt := range routes {
		out = append(out, restroute.Route{Op: rt.Op, Method: rt.Method, Pattern: patternOf(rt)})
	}
	// Served, but not in the model's table: listing, tags (addressed by an
	// ARN that may hold unescaped slashes), and the families doze-aws refuses.
	out = append(out,
		restroute.Route{Op: "GetRestApis", Method: "GET", Pattern: "/restapis"},
		restroute.Route{Op: "GetTags", Method: "GET", Pattern: "/tags/*"},
		restroute.Route{Op: "TagResource", Method: "PUT", Pattern: "/tags/*"},
		restroute.Route{Op: "UntagResource", Method: "DELETE", Pattern: "/tags/*"},
	)
	for _, fam := range []string{"clientcertificates", "domainnames", "vpclinks", "sdktypes"} {
		out = append(out, family("/"+fam, "doze-aws does not implement API Gateway "+fam)...)
	}
	for _, fam := range []string{"models", "requestvalidators", "documentation", "gatewayresponses"} {
		out = append(out, family("/restapis/{restApiId}/"+fam, "doze-aws does not implement API Gateway "+fam)...)
	}
	return append(out, family("/usageplans/{usagePlanId}/usage",
		"doze-aws does not meter requests, so there is no usage to report or reset")...)
})

// v2Specs is the HTTP API's routes, without handlers.
var v2Specs = sync.OnceValue(func() []restroute.Route {
	var out []restroute.Route
	for _, rt := range routesV2 {
		out = append(out, restroute.Route{Op: rt.Op, Method: rt.Method, Pattern: patternOf(rt)})
	}
	for _, fam := range []string{"domainnames", "vpclinks"} {
		out = append(out, family("/v2/"+fam,
			"doze-aws does not implement API Gateway v2 "+fam+": there is no DNS, TLS termination or VPC locally")...)
	}
	for _, fam := range []string{"portals", "portalproducts"} {
		out = append(out, family("/v2/"+fam, "doze-aws does not implement API Gateway v2 portals")...)
	}
	for _, fam := range []string{"models", "exports", "routingrules"} {
		out = append(out, family("/v2/apis/{apiId}/"+fam, "doze-aws does not implement API Gateway v2 "+fam)...)
	}
	out = append(out, family("/v2/apis/{apiId}/routes/{routeId}/routeresponses",
		"doze-aws does not implement route responses: an HTTP API route answers with its integration's response")...)
	out = append(out, family("/v2/apis/{apiId}/routes/{routeId}/requestparameters",
		"doze-aws does not implement DeleteRouteRequestParameter; update the route's requestParameters instead")...)
	return append(out, family("/v2/apis/{apiId}/integrations/{integrationId}/integrationresponses",
		"doze-aws does not implement integration responses: an HTTP API proxies the backend's response as is")...)
})

// P reads a path label.
func p(r *http.Request, name string) string { return restroute.Param(r, name) }

// verb is the method label of a REST API method, upper-cased: GET and get
// are one method.
func verb(r *http.Request) string { return strings.ToUpper(p(r, "httpMethod")) }

// v1Handlers says which handler serves each REST API operation.
func (s *Server) v1Handlers() map[string]restroute.Handler {
	api := func(r *http.Request) string { return p(r, "restApiId") }
	// meth is the (api, resource, verb) a method-level operation addresses.
	meth := func(h func(w http.ResponseWriter, apiID, res, verb string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, api(r), p(r, "resourceId"), verb(r))
		}
	}
	methReq := func(h func(w http.ResponseWriter, r *http.Request, apiID, res, verb string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, r, api(r), p(r, "resourceId"), verb(r))
		}
	}
	status := func(h func(w http.ResponseWriter, apiID, res, verb, status string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, api(r), p(r, "resourceId"), verb(r), p(r, "statusCode"))
		}
	}
	statusReq := func(h func(w http.ResponseWriter, r *http.Request, apiID, res, verb, status string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return h(w, r, api(r), p(r, "resourceId"), verb(r), p(r, "statusCode"))
		}
	}
	onAPI := func(h func(w http.ResponseWriter, apiID string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, api(r)) }
	}
	onAPIReq := func(h func(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, api(r)) }
	}
	// on names the label that follows the API id.
	on := func(label string, h func(w http.ResponseWriter, apiID, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, api(r), p(r, label)) }
	}
	onReq := func(label string, h func(w http.ResponseWriter, r *http.Request, apiID, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, api(r), p(r, label)) }
	}
	plan := func(h func(w http.ResponseWriter, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, p(r, "usagePlanId")) }
	}
	planReq := func(h func(w http.ResponseWriter, r *http.Request, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, p(r, "usagePlanId")) }
	}
	return map[string]restroute.Handler{
		// account, API keys, usage plans
		"GetAccount":    func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.getAccount(w) },
		"UpdateAccount": s.updateAccount,

		"CreateApiKey": s.createAPIKeyOrImport,
		"GetApiKeys":   func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.listAPIKeys(w, r) },
		"GetApiKey": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.getAPIKey(w, r, p(r, "apiKey"))
		},
		"UpdateApiKey": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.patchAPIKey(w, r, p(r, "apiKey"))
		},
		"DeleteApiKey": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.deleteAPIKey(w, p(r, "apiKey"))
		},

		"CreateUsagePlan":    s.createUsagePlan,
		"GetUsagePlans":      func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.listUsagePlans(w, r) },
		"GetUsagePlan":       plan(s.getUsagePlan),
		"UpdateUsagePlan":    planReq(s.patchUsagePlan),
		"DeleteUsagePlan":    plan(s.deleteUsagePlan),
		"CreateUsagePlanKey": planReq(s.createUsagePlanKey),
		"GetUsagePlanKeys":   planReq(s.getUsagePlanKeys),
		"GetUsagePlanKey": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.getUsagePlanKey(w, p(r, "usagePlanId"), p(r, "keyId"))
		},
		"DeleteUsagePlanKey": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.deleteUsagePlanKey(w, p(r, "usagePlanId"), p(r, "keyId"))
		},

		// APIs
		"CreateRestApi": s.createRestAPI,
		"GetRestApis":   func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return s.listRestAPIs(w) },
		"GetRestApi":    onAPI(s.getRestAPI),
		"UpdateRestApi": onAPIReq(s.patchRestAPI),
		"DeleteRestApi": onAPI(s.deleteRestAPI),

		// resources
		"GetResources": onAPI(s.getResources),
		"GetResource":  on("resourceId", s.getResource),
		"CreateResource": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.createResource(w, r, api(r), p(r, "parentId"))
		},
		"UpdateResource": onReq("resourceId", s.patchResource),
		"DeleteResource": on("resourceId", s.deleteResource),

		// methods, integrations and their responses
		"PutMethod":    methReq(s.putMethod),
		"GetMethod":    meth(s.getMethod),
		"DeleteMethod": meth(s.deleteMethod),

		"PutIntegration":    methReq(s.putIntegration),
		"GetIntegration":    meth(s.getIntegration),
		"DeleteIntegration": meth(s.deleteIntegration),

		"PutMethodResponse":    statusReq(s.putMethodResponse),
		"GetMethodResponse":    status(s.getMethodResponse),
		"DeleteMethodResponse": status(s.deleteMethodResponse),

		"PutIntegrationResponse":    statusReq(s.putIntegrationResponse),
		"GetIntegrationResponse":    status(s.getIntegrationResponse),
		"DeleteIntegrationResponse": status(s.deleteIntegrationResponse),

		// deployments, stages, authorizers
		"CreateDeployment": onAPIReq(s.createDeployment),
		"GetDeployments":   onAPI(s.getDeployments),
		"GetDeployment":    on("deploymentId", s.getDeployment),
		"DeleteDeployment": on("deploymentId", s.deleteDeployment),

		"CreateStage": onAPIReq(s.createStage),
		"GetStages":   onAPIReq(s.getStages),
		"GetStage":    onReq("stageName", s.getStage),
		"UpdateStage": onReq("stageName", s.patchStage),
		"DeleteStage": on("stageName", s.deleteStage),

		"CreateAuthorizer": onAPIReq(s.createAuthorizer),
		"GetAuthorizers":   onAPI(s.getAuthorizers),
		"GetAuthorizer":    on("authorizerId", s.getAuthorizer),
		"UpdateAuthorizer": onReq("authorizerId", s.patchAuthorizer),
		"DeleteAuthorizer": on("authorizerId", s.deleteAuthorizer),

		// tags, shared with the HTTP API
		"GetTags":       s.getTags,
		"TagResource":   s.tagResource,
		"UntagResource": s.untagResource,
	}
}

// v2Handlers says which handler serves each HTTP API operation.
func (s *Server) v2Handlers() map[string]restroute.Handler {
	api := func(r *http.Request) string { return p(r, "apiId") }
	onAPI := func(h func(w http.ResponseWriter, apiID string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, api(r)) }
	}
	onAPIReq := func(h func(w http.ResponseWriter, r *http.Request, apiID string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, api(r)) }
	}
	on := func(label string, h func(w http.ResponseWriter, apiID, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, api(r), p(r, label)) }
	}
	onReq := func(label string, h func(w http.ResponseWriter, r *http.Request, apiID, id string) *awshttp.APIError) restroute.Handler {
		return func(w http.ResponseWriter, r *http.Request) *awshttp.APIError { return h(w, r, api(r), p(r, label)) }
	}
	return map[string]restroute.Handler{
		"CreateApi": s.v2CreateAPI,
		"GetApis":   s.v2ListAPIs,
		"GetApi": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.v2GetAPI(w, r, api(r))
		},
		"UpdateApi":               onAPIReq(s.v2UpdateAPI),
		"DeleteApi":               onAPI(s.v2DeleteAPI),
		"DeleteCorsConfiguration": onAPI(s.v2DeleteCORS),

		"CreateAuthorizer": onAPIReq(s.v2CreateAuthorizer),
		"GetAuthorizers":   onAPI(s.v2GetAuthorizers),
		"GetAuthorizer":    on("authorizerId", s.v2GetAuthorizer),
		"UpdateAuthorizer": onReq("authorizerId", s.v2UpdateAuthorizer),
		"DeleteAuthorizer": on("authorizerId", s.v2DeleteAuthorizer),

		"CreateDeployment": onAPIReq(s.v2CreateDeployment),
		"GetDeployments":   onAPI(s.v2GetDeployments),
		"GetDeployment":    on("deploymentId", s.v2GetDeployment),
		"UpdateDeployment": onReq("deploymentId", s.v2UpdateDeployment),
		"DeleteDeployment": on("deploymentId", s.v2DeleteDeployment),

		"CreateIntegration": onAPIReq(s.v2CreateIntegration),
		"GetIntegrations":   onAPI(s.v2GetIntegrations),
		"GetIntegration":    on("integrationId", s.v2GetIntegration),
		"UpdateIntegration": onReq("integrationId", s.v2UpdateIntegration),
		"DeleteIntegration": on("integrationId", s.v2DeleteIntegration),

		"CreateRoute": onAPIReq(s.v2CreateRoute),
		"GetRoutes":   onAPI(s.v2GetRoutes),
		"GetRoute":    on("routeId", s.v2GetRoute),
		"UpdateRoute": onReq("routeId", s.v2UpdateRoute),
		"DeleteRoute": on("routeId", s.v2DeleteRoute),

		"CreateStage": onAPIReq(s.v2CreateStage),
		"GetStages": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.v2GetStages(w, r, api(r))
		},
		"GetStage": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.v2GetStage(w, r, api(r), p(r, "stageName"))
		},
		"UpdateStage": onReq("stageName", s.v2UpdateStage),
		"DeleteStage": on("stageName", s.v2DeleteStage),

		"DeleteAccessLogSettings": on("stageName", s.v2DeleteAccessLogSettings),
		"ResetAuthorizersCache":   on("stageName", s.v2ResetAuthorizersCache),
		"DeleteRouteSettings": func(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
			return s.v2DeleteRouteSettings(w, api(r), p(r, "stageName"), labelValue(r, "routeKey"))
		},

		"GetTags":       s.getTags,
		"TagResource":   s.tagResource,
		"UntagResource": s.untagResource,
	}
}

// refuse logs and writes an error the way every API Gateway refusal is.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, aerr *awshttp.APIError) {
	s.logf("apigateway: %s %s -> %s", r.Method, r.URL.Path, aerr.Code)
	writeError(w, aerr)
}

func notFound(r *http.Request) *awshttp.APIError {
	return errNotFound("unknown resource %s", r.URL.Path)
}

func notAllowed(r *http.Request) *awshttp.APIError {
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method %s on %s", r.Method, r.URL.Path)
}

func (s *Server) build(specs []restroute.Route, handlers map[string]restroute.Handler) *restroute.Router {
	out := append([]restroute.Route(nil), specs...)
	for i := range out {
		if out[i].Handler == nil {
			out[i].Handler = handlers[out[i].Op]
		}
	}
	return restroute.Build(out, restroute.Options{
		OnError:          s.refuse,
		Tolerant:         true,
		NotFound:         notFound,
		MethodNotAllowed: notAllowed,
		// After matching, so the operation is known: validation first, then
		// the REST API scope check.
		Use: []func(http.Handler) http.Handler{s.validateMiddleware, s.restScope},
	})
}

func (s *Server) buildV1() *restroute.Router { return s.build(v1Specs(), s.v1Handlers()) }
func (s *Server) buildV2() *restroute.Router { return s.build(v2Specs(), s.v2Handlers()) }

func (s *Server) validateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if aerr := validateRequest(r); aerr != nil {
			s.refuse(w, r, aerr)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// restScope keeps the two control planes' id spaces apart: an HTTP API is not
// a REST API, so the v1 surface does not see it, as on AWS.
func (s *Server) restScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := p(r, "restApiId"); id != "" {
			if api, err := s.store.Get(id); err == nil && api.Protocol == "HTTP" {
				s.refuse(w, r, errNotFound("Invalid REST API identifier specified %s", id))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// serveControl routes a control-plane request to the plane its path names.
func (s *Server) serveControl(w http.ResponseWriter, r *http.Request) {
	if isV2Path(r.URL.Path) {
		s.v2().ServeHTTP(w, r)
		return
	}
	s.v1().ServeHTTP(w, r)
}

// opsOnly names operations for callers with no Server — the console's wire
// page: the same routes with no handlers behind them.
var opsOnly = sync.OnceValues(func() (v1, v2 *restroute.Router) {
	o := restroute.Options{
		OnError:          func(http.ResponseWriter, *http.Request, *awshttp.APIError) {},
		Tolerant:         true,
		NotFound:         notFound,
		MethodNotAllowed: notAllowed,
	}
	return restroute.Build(v1Specs(), o), restroute.Build(v2Specs(), o)
})

// OperationFor reports the API Gateway operation a request addresses, or ""
// when no route matches. Exported for the console's traffic classifier.
func OperationFor(r *http.Request) string {
	v1, v2 := opsOnly()
	if isV2Path(r.URL.Path) {
		return v2.Op(r)
	}
	return v1.Op(r)
}
