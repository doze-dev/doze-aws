package apigateway

// The AWS_PROXY integration: the v1 proxy event a Lambda receives and the
// response shape it must return.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/peercall"
	"github.com/doze-dev/doze-aws/peers"
)

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

// buildProxyEvent shapes the request as API Gateway hands it to a function.
// cc, when an authorizer ran, lands under requestContext.authorizer with the
// principal, as on AWS.
func (s *Server) buildProxyEvent(r *http.Request, api *RestAPI, stage string, res *Resource,
	params map[string]string, path string, body []byte, cc *callCtx, rid string) proxyEvent {

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
			"requestId":    rid,
			"protocol":     "HTTP/1.1",
			"identity": map[string]any{
				"sourceIp":  sourceIP(r),
				"userAgent": r.Header.Get("User-Agent"),
			},
		},
	}
	if cc != nil {
		if cc.Principal != "" {
			auth := map[string]any{"principalId": cc.Principal}
			for k, v := range cc.Context {
				auth[k] = v
			}
			ev.RequestContext["authorizer"] = auth
		}
		if cc.APIKeyID != "" {
			ident := ev.RequestContext["identity"].(map[string]any)
			ident["apiKey"], ident["apiKeyId"] = cc.APIKey, cc.APIKeyID
		}
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
	return ev
}

func (s *Server) invokeLambdaProxy(w http.ResponseWriter, r *http.Request, api *RestAPI,
	stage string, res *Resource, integ *Integration, params map[string]string, path string, body []byte, cc *callCtx, rl *requestLog) {

	fn := lambdaFromURI(integ.URI)
	if fn == "" {
		rl.errMessage = "cannot tell which Lambda function the integration URI names: " + integ.URI
		writeExecuteError(w, 500, "Internal server error",
			"cannot tell which Lambda function the integration URI names: "+integ.URI)
		return
	}
	ev := s.buildProxyEvent(r, api, stage, res, params, path, body, cc, rl.id)
	payload, err := json.Marshal(ev)
	if err != nil {
		rl.errMessage = err.Error()
		writeExecuteError(w, 500, "Internal server error", err.Error())
		return
	}
	rl.integBody, rl.integStart = payload, s.now()
	// The invoke is API Gateway's own call, on behalf of the API.
	out, err := peercall.LambdaInvoke(peers.WithPrincipal(r.Context(), "apigateway", APIARN(api.ID)), s.peers, fn, payload)
	rl.integEnd, rl.integResp = s.now(), out
	if err != nil {
		rl.errMessage = "invoking " + fn + ": " + err.Error()
		writeExecuteError(w, 502, "Internal server error",
			"invoking "+fn+": "+err.Error())
		return
	}
	if msg := writeProxyResponse(w, out, fn); msg != "" {
		rl.errMessage = msg
	}
}

// writeProxyResponse turns a Lambda's return value into an HTTP response.
//
// A handler that returns a malformed shape produces a 502 in AWS, and the same
// here — a proxy integration's contract is the response object, so a handler
// that ignores it has genuinely failed.
//
// It reports the malformed-response message when there was one, for the
// execution log; "" when the response went out as the function shaped it.
func writeProxyResponse(w http.ResponseWriter, out []byte, fn string) string {
	var resp struct {
		StatusCode        int                 `json:"statusCode"`
		Headers           map[string]string   `json:"headers"`
		MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
		Body              string              `json:"body"`
		IsBase64Encoded   bool                `json:"isBase64Encoded"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.StatusCode == 0 {
		msg := "function " + fn + " did not return a proxy-integration response " +
			`({"statusCode":…,"body":…}); got: ` + truncate(string(out), 200)
		writeExecuteError(w, 502, "Internal server error", msg)
		return "Malformed Lambda proxy response: " + msg
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
	return ""
}
