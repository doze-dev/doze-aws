package lambda

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
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

// urlID is the 32-character lowercase id a function's URL carries. It is
// derived from the name so a redeploy addresses the same URL.
func urlID(name string) string {
	sum := sha256.Sum256([]byte("function-url:" + name))
	id := hex.EncodeToString(sum[:])[:32]
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0') // AWS ids are letters and digits; all letters is still valid
		}
		return r
	}, id)
}

// functionURL is the URL a config reports: the on.aws host when the
// endpoint is unknown, the gateway's path form when it is.
func (s *Server) functionURL(id string) string {
	if s.endpoint != "" {
		return strings.TrimRight(s.endpoint, "/") + "/_aws/lambda-url/" + id + "/"
	}
	return "https://" + id + ".lambda-url." + awsident.Region + ".on.aws/"
}

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
		host := r.Host
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		id, _, _ = strings.Cut(host, ".")
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
func (s *Server) serveFunctionURL(w http.ResponseWriter, r *http.Request) {
	f, path := s.functionByURL(r)
	if f == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"Message":"Not Found"}`))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 6<<20))
	event := urlEvent(r, path, body, f)
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
	writeURLResponse(w, res.Payload)
}

type functionError struct{ res lambdaruntime.Result }

func (e functionError) Error() string            { return e.res.FunctionErr + ": " + string(e.res.Payload) }
func errFunction(res lambdaruntime.Result) error { return functionError{res} }

// urlEvent is the payload format 2.0 event a function URL delivers.
func urlEvent(r *http.Request, path string, body []byte, f *Function) map[string]any {
	headers := map[string]string{}
	var cookies []string
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if lk == "cookie" {
			for _, v := range vs {
				for _, c := range strings.Split(v, ";") {
					if c = strings.TrimSpace(c); c != "" {
						cookies = append(cookies, c)
					}
				}
			}
			continue
		}
		headers[lk] = strings.Join(vs, ",")
	}
	headers["host"] = r.Host
	query := map[string]string{}
	for k, vs := range r.URL.Query() {
		query[k] = strings.Join(vs, ",")
	}
	isBinary := !isText(r.Header.Get("Content-Type"))
	bodyStr := string(body)
	if isBinary && len(body) > 0 {
		bodyStr = base64.StdEncoding.EncodeToString(body)
	}
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i >= 0 {
		ip = ip[:i]
	}
	now := time.Now()
	ev := map[string]any{
		"version":        "2.0",
		"routeKey":       "$default",
		"rawPath":        path,
		"rawQueryString": r.URL.RawQuery,
		"headers":        headers,
		"requestContext": map[string]any{
			"accountId":    "anonymous",
			"apiId":        f.URLId,
			"domainName":   f.URLId + ".lambda-url." + awsident.Region + ".on.aws",
			"domainPrefix": f.URLId,
			"http": map[string]any{
				"method": r.Method, "path": path, "protocol": r.Proto,
				"sourceIp": ip, "userAgent": r.UserAgent(),
			},
			"requestId": lambdaruntime.NewRequestID(),
			"routeKey":  "$default",
			"stage":     "$default",
			"time":      now.UTC().Format("02/Jan/2006:15:04:05 -0700"),
			"timeEpoch": now.UnixMilli(),
		},
		"body":            bodyStr,
		"isBase64Encoded": isBinary && len(body) > 0,
	}
	if len(query) > 0 {
		ev["queryStringParameters"] = query
	}
	if len(cookies) > 0 {
		ev["cookies"] = cookies
	}
	return ev
}

func isText(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "" || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") ||
		strings.Contains(ct, "x-www-form-urlencoded") || strings.Contains(ct, "javascript")
}

// writeURLResponse decodes what the function returned, by AWS's rule: an
// object with statusCode is the whole response; anything else is the body
// of a 200 application/json.
func writeURLResponse(w http.ResponseWriter, out []byte) {
	var resp struct {
		StatusCode      int               `json:"statusCode"`
		Headers         map[string]string `json:"headers"`
		Cookies         []string          `json:"cookies"`
		Body            string            `json:"body"`
		IsBase64Encoded bool              `json:"isBase64Encoded"`
	}
	trimmed := strings.TrimSpace(string(out))
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal(out, &resp) != nil || resp.StatusCode == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(out)
		return
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	for _, c := range resp.Cookies {
		w.Header().Add("Set-Cookie", c)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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
