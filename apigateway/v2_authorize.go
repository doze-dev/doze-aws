package apigateway

// HTTP API REQUEST authorizers. The identity sources are selection
// expressions ($request.header.Authorization, $request.querystring.token,
// $context.identity.sourceIp, $stageVariables.x); every one must be present
// or the request is 401 before the function is called. The function gets
// the request in the authorizer's payload format version and answers either
// the simple response ({isAuthorized, context}) or an IAM policy, which is
// evaluated for the route's ARN. The answer is cached per authorizer and
// identity values for authorizerResultTtlInSeconds, as for a REST API.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/httpevent"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

// v2IdentityValues reads the authorizer's identity sources from the request.
func v2IdentityValues(a *V2Authorizer, r *http.Request, call *v2Call) ([]string, bool) {
	sources := a.IdentitySource
	if len(sources) == 0 {
		sources = []string{"$request.header.Authorization"}
	}
	var out []string
	for _, src := range sources {
		src = strings.TrimSpace(src)
		var v string
		switch {
		case strings.HasPrefix(src, "$request.header."):
			v = r.Header.Get(strings.TrimPrefix(src, "$request.header."))
		case strings.HasPrefix(src, "$request.querystring."):
			v = r.URL.Query().Get(strings.TrimPrefix(src, "$request.querystring."))
		case strings.HasPrefix(src, "$request.path."):
			v = call.params[strings.TrimPrefix(src, "$request.path.")]
		case strings.HasPrefix(src, "$stageVariables."):
			v = call.stage.Variables[strings.TrimPrefix(src, "$stageVariables.")]
		case src == "$context.identity.sourceIp":
			v = sourceIP(r)
		case src == "$context.identity.userAgent":
			v = r.UserAgent()
		}
		if v == "" {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// v2RouteARN is the execute-api ARN a policy is evaluated against.
func v2RouteARN(apiID, stage, method, path string) string {
	return awsident.ARN("execute-api", apiID+"/"+stage+"/"+strings.ToUpper(method)+strings.TrimSuffix("/"+strings.TrimPrefix(path, "/"), "/"))
}

// v2AuthResponse is either shape a v2 authorizer may answer with.
type v2AuthResponse struct {
	IsAuthorized *bool          `json:"isAuthorized"`
	Context      map[string]any `json:"context"`
	policy       *authorizerResponse
}

// v2Authorize runs the route's authorizer. It returns the context the
// integration event carries under requestContext.authorizer, or a denial.
func (s *Server) v2Authorize(r *http.Request, call *v2Call, a *V2Authorizer) (map[string]any, *authDenial) {
	rl := call.rl
	values, ok := v2IdentityValues(a, r, call)
	if !ok {
		return nil, &authDenial{401, "Unauthorized"}
	}
	arn := v2RouteARN(call.api.ID, call.stage.Name, r.Method, call.path)
	key := a.ID + "\x00" + strings.Join(values, "\x00")
	if a.ResultTTL > 0 {
		if cached, ok := s.authCache.get(key, s.now()); ok {
			rl.authCached = true
			return s.v2Decide(cached, a, arn, rl)
		}
	}

	fn := lambdaFromURI(a.URI)
	var payload []byte
	if a.PayloadFormatVersion == "1.0" {
		res := &Resource{ID: call.route.ID, Path: v2RoutePath(call.route)}
		ev := s.buildProxyEvent(r, call.api, call.stage.Name, res, call.params, call.path, nil, nil, rl.id)
		m := map[string]any{
			"version": "1.0", "type": "REQUEST", "methodArn": arn, "identitySource": strings.Join(values, ","),
			"authorizationToken": values[0], "resource": ev.Resource, "path": ev.Path, "httpMethod": ev.HTTPMethod,
			"headers": ev.Headers, "multiValueHeaders": ev.MultiValueHeaders, "queryStringParameters": ev.QueryStringParameters,
			"multiValueQueryStringParameters": ev.MultiValueQueryStringParameters, "pathParameters": ev.PathParameters,
			"stageVariables": ev.StageVariables, "requestContext": ev.RequestContext,
		}
		payload, _ = json.Marshal(m)
	} else {
		ev := httpevent.Event(httpevent.Request{
			R: r, Path: call.path, RouteKey: call.route.RouteKey, Stage: call.stage.Name, APIID: call.api.ID,
			DomainName: call.api.ID + ".execute-api." + awsident.Region + ".amazonaws.com",
			AccountID:  awsident.AccountID, RequestID: rl.id, Now: s.now(),
			PathParameters: call.params, StageVariables: call.stage.Variables,
		})
		ev["type"] = "REQUEST"
		ev["routeArn"] = arn
		ev["identitySource"] = values
		delete(ev, "body")
		delete(ev, "isBase64Encoded")
		payload, _ = json.Marshal(ev)
	}
	rl.authorizer, rl.authStart = a.Name, s.now()
	out, err := peercall.LambdaInvoke(peers.WithPrincipal(r.Context(), "apigateway", V2APIARN(call.api.ID)), s.peers, fn, payload)
	rl.authEnd = s.now()
	if err != nil {
		s.logf("apigateway: authorizer %s: invoking %s: %v", a.Name, fn, err)
		return nil, &authDenial{500, "Internal Server Error"}
	}
	resp, perr := s.v2ParseAuth(out, a)
	if perr != nil {
		s.logf("apigateway: authorizer %s: %v", a.Name, perr)
		return nil, &authDenial{500, "Internal Server Error"}
	}
	if a.ResultTTL > 0 {
		s.authCache.put(key, resp, s.now().Add(time.Duration(a.ResultTTL)*time.Second))
	}
	return s.v2Decide(resp, a, arn, rl)
}

// v2ParseAuth reads the function's answer in the shape the authorizer is
// configured for, into the cache's record: a simple response is kept as a
// synthetic policy so one cache serves both.
func (s *Server) v2ParseAuth(out []byte, a *V2Authorizer) (*authorizerResponse, error) {
	if a.EnableSimpleResponses {
		var simple v2AuthResponse
		if err := json.Unmarshal(out, &simple); err != nil || simple.IsAuthorized == nil {
			return nil, errBadRequest("the authorizer did not return a simple response ({isAuthorized, context}): %s", truncate(string(out), 200))
		}
		effect := "Deny"
		if *simple.IsAuthorized {
			effect = "Allow"
		}
		return &authorizerResponse{
			PrincipalID: "simple",
			Policy:      &policyDoc{Version: "2012-10-17", Statement: []statement{{Effect: effect, Action: "execute-api:Invoke", Resource: "*"}}},
			Context:     simple.Context,
		}, nil
	}
	return parseAuthorizerResponse(out)
}

// v2Decide turns the answer into a verdict; the context lands on the
// integration event, with the principal when a policy named one.
func (s *Server) v2Decide(resp *authorizerResponse, a *V2Authorizer, arn string, rl *requestLog) (map[string]any, *authDenial) {
	if !a.EnableSimpleResponses {
		rl.principal = resp.PrincipalID
	}
	if !policyAllows(resp.Policy, arn) {
		return nil, &authDenial{403, "Forbidden"}
	}
	ctx := map[string]any{}
	for k, v := range resp.Context {
		ctx[k] = v
	}
	if !a.EnableSimpleResponses {
		ctx["principalId"] = resp.PrincipalID
	}
	return ctx, nil
}
