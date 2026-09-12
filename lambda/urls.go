package lambda

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshost"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/httpevent"
	"github.com/doze-dev/doze-aws/internal/iamguard"
	"github.com/doze-dev/doze-aws/internal/iampolicy"
	"github.com/doze-dev/doze-aws/internal/lambdaruntime"
	"github.com/doze-dev/doze-aws/internal/trace"
)

// Function URLs, served.
//
// A URL config mints an id, and the function answers at two addresses the
// gateway routes here: {id}.lambda-url.<region>.on.aws, the shape AWS hands
// out, for a client that can set a Host; and /_aws/lambda-url/{id}/…, for
// one that cannot. The request becomes the payload-format-2.0 event a URL
// invocation carries, and the function's answer is decoded the way AWS
// decodes it — an object with statusCode as a full response, anything else
// as a 200 with the value as the JSON body.

// urlID is the 32-character lowercase id a function's URL carries, derived
// from the name so a redeploy addresses the same URL (awsident.FunctionURLID,
// shared with CloudFormation so a template's GetAtt FunctionUrl is right).
func urlID(name string) string { return awsident.FunctionURLID(name) }

// functionURL is the URL a config reports: the on.aws host when the
// endpoint is unknown, the gateway's path form when it is.
func (s *Server) functionURL(urlID string) string { return s.id.FunctionURL(urlID, s.endpoint) }

// urlConfigView is the Create/Get/UpdateFunctionUrlConfig response.
func (s *Server) urlConfigView(f *Function, status int) map[string]any {
	now := awshttp.ISO8601(s.now())
	v := map[string]any{
		"FunctionUrl": f.FunctionURL, "FunctionArn": f.ARN(), "AuthType": orStr(f.URLAuthType, "NONE"),
		"InvokeMode": "BUFFERED", "CreationTime": now, "LastModifiedTime": now,
	}
	if len(f.URLCors) > 0 {
		v["Cors"] = json.RawMessage(f.URLCors)
	}
	return v
}

func (s *Server) routeFunctionURL(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		var req struct {
			AuthType string          `json:"AuthType"`
			Cors     json.RawMessage `json:"Cors"`
		}
		decode(r, &req)
		f, err := s.store.Update(name, func(f *Function) error {
			if f.URLId == "" {
				f.URLId = urlID(name)
			}
			f.FunctionURL = s.functionURL(f.URLId)
			if req.AuthType != "" {
				f.URLAuthType = req.AuthType
			}
			if len(req.Cors) > 0 {
				f.URLCors = req.Cors
			}
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		status := 201
		if r.Method == http.MethodPut {
			status = 200
		}
		writeJSON(w, status, s.urlConfigView(f, status))
		return nil
	case http.MethodGet:
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if f.FunctionURL == "" {
			return awshttp.Errf(404, "ResourceNotFoundException", "The resource you requested does not exist.")
		}
		writeJSON(w, 200, s.urlConfigView(f, 200))
		return nil
	case http.MethodDelete:
		s.store.Update(name, func(f *Function) error {
			f.FunctionURL, f.URLId, f.URLAuthType, f.URLCors = "", "", "", nil
			return nil
		})
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported function-url request")
}

// functionByURL finds the function whose URL id a request addresses.
func (s *Server) functionByURL(r *http.Request) (*Function, string) {
	id, rest := "", r.URL.Path
	if strings.HasPrefix(r.URL.Path, "/_aws/lambda-url/") {
		rest = strings.TrimPrefix(r.URL.Path, "/_aws/lambda-url/")
		id, rest, _ = strings.Cut(rest, "/")
		rest = "/" + rest
	} else {
		// <id>.lambda-url.<region>.<suffix>, parsed by the shared host reader
		// rather than by taking the first label — which is what this did, and
		// which claimed the first label of ANY host it was handed.
		id = awshost.Parse(r.Host, s.suffix).FunctionURLID
	}
	fns, err := s.store.ListFunctions()
	if err != nil {
		return nil, rest
	}
	for i := range fns {
		if fns[i].URLId == id && fns[i].FunctionURL != "" {
			return &fns[i], rest
		}
	}
	return nil, rest
}

// serveFunctionURL is the data plane: build the v2 event, invoke, decode.
// Under IAM soft or enforce the URL needs what it needs on AWS even with
// AuthType NONE: a resource-policy statement granting
// lambda:InvokeFunctionUrl to everyone, which the URL's anonymous caller is
// admitted by and nothing else.
func (s *Server) serveFunctionURL(w http.ResponseWriter, r *http.Request) {
	f, path := s.functionByURL(r)
	if f != nil {
		mode := r.Header.Get(iamguard.HeaderMode)
		if mode == "" {
			mode = s.guard.Mode
		}
		if mode == "soft" || mode == "enforce" {
			dec, _ := iampolicy.Evaluate(functionPolicyDocs(f), iampolicy.Request{
				Action: "lambda:InvokeFunctionUrl", Resource: s.id.ARN("lambda", "function:"+f.Name), Principal: "",
			})
			if dec != iampolicy.Allowed {
				if mode == "enforce" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(403)
					w.Write([]byte(`{"Message":"Forbidden"}`))
					return
				}
				s.logf("iam[soft]: would deny lambda:InvokeFunctionUrl on %s: no statement grants it to everyone", f.Name)
			}
		}
	}
	if f == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"Message":"Not Found"}`))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 6<<20))
	event := urlEvent(s.id, r, path, body, f)
	payload, _ := json.Marshal(event)
	var res lambdaruntime.Result
	err := trace.StepDetail(r.Context(), trace.Event{Service: "lambda", Action: "Invoke", Resource: f.Name, Via: "function URL"},
		func(ctx context.Context) (string, string, error) {
			var e error
			res, e = s.runInvokeInput(ctx, f, lambdaruntime.Input{Payload: payload, TraceID: r.Header.Get("X-Amzn-Trace-Id")})
			if e == nil && res.FunctionErr != "" {
				e = errFunction(res)
			}
			return invocationTail(res), logsURL(f.Name, res.RequestID), e
		})
	w.Header().Set("X-Amzn-RequestId", res.RequestID)
	if err != nil || res.FunctionErr != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		w.Write([]byte(`{"Message":"Internal Server Error"}`))
		return
	}
	httpevent.WriteResponse(w, res.Payload)
}

type functionError struct{ res lambdaruntime.Result }

func (e functionError) Error() string            { return e.res.FunctionErr + ": " + string(e.res.Payload) }
func errFunction(res lambdaruntime.Result) error { return functionError{res} }

// urlEvent is the payload format 2.0 event a function URL delivers — the
// same document an HTTP API route delivers, built by internal/httpevent.
func urlEvent(id awsident.Identity, r *http.Request, path string, body []byte, f *Function) map[string]any {
	return httpevent.Event(httpevent.Request{
		R: r, Path: path, Body: body,
		APIID:      f.URLId,
		DomainName: f.URLId + ".lambda-url." + id.RegionName() + ".on.aws",
		RequestID:  lambdaruntime.NewRequestID(),
	})
}
