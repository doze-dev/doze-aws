// Package httpevent is the Lambda payload format 2.0: the event an HTTP API
// route or a function URL hands a function, and the response shape the
// function answers with. Both surfaces deliver the same document on AWS, so
// one builder serves both here.
package httpevent

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Request is what the caller knows about the HTTP request and the route it
// matched. Zero fields take the function-URL defaults ($default route and
// stage, anonymous account).
type Request struct {
	R    *http.Request
	Path string // the path as the function should see it (stage prefix removed)
	Body []byte

	RouteKey   string // "$default", or "GET /items/{id}"
	Stage      string
	APIID      string
	DomainName string
	AccountID  string
	RequestID  string
	Now        time.Time

	PathParameters map[string]string
	StageVariables map[string]string
	// Authorizer is the requestContext.authorizer block: {"lambda": {...}}
	// for a Lambda authorizer's context, as AWS nests it in 2.0.
	Authorizer map[string]any
}

// Event builds the 2.0 event document.
func Event(req Request) map[string]any {
	r := req.R
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
	isBinary := !IsText(r.Header.Get("Content-Type"))
	bodyStr := string(req.Body)
	if isBinary && len(req.Body) > 0 {
		bodyStr = base64.StdEncoding.EncodeToString(req.Body)
	}
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i >= 0 {
		ip = ip[:i]
	}
	routeKey, stage, account := req.RouteKey, req.Stage, req.AccountID
	if routeKey == "" {
		routeKey = "$default"
	}
	if stage == "" {
		stage = "$default"
	}
	if account == "" {
		account = "anonymous"
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	domain := req.DomainName
	prefix, _, _ := strings.Cut(domain, ".")
	rc := map[string]any{
		"accountId":    account,
		"apiId":        req.APIID,
		"domainName":   domain,
		"domainPrefix": prefix,
		"http": map[string]any{
			"method": r.Method, "path": req.Path, "protocol": r.Proto,
			"sourceIp": ip, "userAgent": r.UserAgent(),
		},
		"requestId": req.RequestID,
		"routeKey":  routeKey,
		"stage":     stage,
		"time":      now.UTC().Format("02/Jan/2006:15:04:05 -0700"),
		"timeEpoch": now.UnixMilli(),
	}
	if len(req.Authorizer) > 0 {
		rc["authorizer"] = req.Authorizer
	}
	ev := map[string]any{
		"version":         "2.0",
		"routeKey":        routeKey,
		"rawPath":         req.Path,
		"rawQueryString":  r.URL.RawQuery,
		"headers":         headers,
		"requestContext":  rc,
		"body":            bodyStr,
		"isBase64Encoded": isBinary && len(req.Body) > 0,
	}
	if len(query) > 0 {
		ev["queryStringParameters"] = query
	}
	if len(cookies) > 0 {
		ev["cookies"] = cookies
	}
	if len(req.PathParameters) > 0 {
		ev["pathParameters"] = req.PathParameters
	}
	if len(req.StageVariables) > 0 {
		ev["stageVariables"] = req.StageVariables
	}
	return ev
}

// IsText reports whether a content type travels as text in the event; any
// other body is base64-encoded, as AWS does.
func IsText(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "" || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") ||
		strings.Contains(ct, "x-www-form-urlencoded") || strings.Contains(ct, "javascript")
}

// WriteResponse decodes what the function returned, by AWS's 2.0 rule: an
// object with statusCode is the whole response (headers, cookies, body,
// base64 flag); anything else is the body of a 200 application/json.
func WriteResponse(w http.ResponseWriter, out []byte) {
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
