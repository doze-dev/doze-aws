package console

// API Gateway console client (REST-JSON control plane).
//
// The question someone has about an API is "what happens when a request comes
// in", so the page is a route tree with each method's integration attached,
// and a way to actually send a request through the deployed stage. An API you
// can read but not call tells you half of what you need.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// RestAPI is one REST API.
type RestAPI struct {
	ID             string
	Name           string
	Description    string
	Created        string
	APIKeySource   string
	MinCompression int
	DisableExecute bool
	EndpointTypes  []string
	RootID         string
	Routes         int
}

// APIMethod is one method on a resource, with the integration behind it.
type APIMethod struct {
	HTTPMethod  string
	AuthType    string
	APIKeyReq   bool
	Operation   string
	Integration APIIntegration
}

// APIIntegration is what a method forwards to.
type APIIntegration struct {
	Type       string // AWS_PROXY, HTTP, MOCK, ...
	HTTPMethod string
	URI        string
	Timeout    int
	// Target is the integration reduced to the thing it actually hits — the
	// Lambda function name for a proxy integration, the URL for HTTP. The raw
	// URI is an ARN inside an ARN and unreadable at a glance.
	Target string
	Svc    string
	Href   string
}

// APIResource is one node of the route tree.
type APIResource struct {
	ID       string
	Path     string
	ParentID string
	Methods  []APIMethod
	// Depth indents the tree without the template having to count slashes.
	Depth int
}

// APIStage is one deployed stage.
type APIStage struct {
	Name         string
	DeploymentID string
	Description  string
	Created      string
	Variables    map[string]string
	Tracing      bool
	CacheEnabled bool
	// InvokeBase is where a request to this stage goes.
	InvokeBase string
}

func (b *backend) apigwGet(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+path, nil)
	body, err := b.do(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (b *backend) ListRestAPIs(ctx context.Context) ([]RestAPI, error) {
	var out struct {
		Items []struct {
			ID                    string  `json:"id"`
			Name                  string  `json:"name"`
			Description           string  `json:"description"`
			CreatedDate           float64 `json:"createdDate"`
			APIKeySource          string  `json:"apiKeySource"`
			MinimumCompression    int     `json:"minimumCompressionSize"`
			DisableExecuteAPI     bool    `json:"disableExecuteApiEndpoint"`
			RootResourceID        string  `json:"rootResourceId"`
			EndpointConfiguration struct {
				Types []string `json:"types"`
			} `json:"endpointConfiguration"`
		} `json:"item"`
	}
	if err := b.apigwGet(ctx, "/restapis", &out); err != nil {
		return nil, err
	}
	apis := make([]RestAPI, 0, len(out.Items))
	for _, a := range out.Items {
		apis = append(apis, RestAPI{
			ID: a.ID, Name: a.Name, Description: a.Description,
			Created: epochTime(a.CreatedDate), APIKeySource: a.APIKeySource,
			MinCompression: a.MinimumCompression, DisableExecute: a.DisableExecuteAPI,
			EndpointTypes: a.EndpointConfiguration.Types, RootID: a.RootResourceID,
		})
	}
	sort.Slice(apis, func(i, j int) bool { return apis[i].Name < apis[j].Name })
	return apis, nil
}

// CountRestAPIs is the cheap probe for the nav badge.
func (b *backend) CountRestAPIs(ctx context.Context) (int, error) {
	apis, err := b.ListRestAPIs(ctx)
	return len(apis), err
}

func (b *backend) RestAPI(ctx context.Context, id string) (*RestAPI, error) {
	apis, err := b.ListRestAPIs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range apis {
		if apis[i].ID == id {
			return &apis[i], nil
		}
	}
	return nil, fmt.Errorf("rest api %s does not exist", id)
}

// APIRoutes returns the resource tree with each method's integration resolved.
func (b *backend) APIRoutes(ctx context.Context, apiID string) ([]APIResource, error) {
	var out struct {
		Items []struct {
			ID              string `json:"id"`
			ParentID        string `json:"parentId"`
			Path            string `json:"path"`
			ResourceMethods map[string]struct {
				HTTPMethod        string `json:"httpMethod"`
				AuthorizationType string `json:"authorizationType"`
				APIKeyRequired    bool   `json:"apiKeyRequired"`
				OperationName     string `json:"operationName"`
				MethodIntegration struct {
					Type       string `json:"type"`
					HTTPMethod string `json:"httpMethod"`
					URI        string `json:"uri"`
					Timeout    int    `json:"timeoutInMillis"`
				} `json:"methodIntegration"`
			} `json:"resourceMethods"`
		} `json:"item"`
	}
	if err := b.apigwGet(ctx, "/restapis/"+url.PathEscape(apiID)+"/resources", &out); err != nil {
		return nil, err
	}
	res := make([]APIResource, 0, len(out.Items))
	for _, r := range out.Items {
		node := APIResource{ID: r.ID, Path: r.Path, ParentID: r.ParentID, Depth: pathDepth(r.Path)}
		for _, m := range r.ResourceMethods {
			mi := m.MethodIntegration
			integ := APIIntegration{
				Type: mi.Type, HTTPMethod: mi.HTTPMethod, URI: mi.URI, Timeout: mi.Timeout,
			}
			integ.Target, integ.Svc, integ.Href = integrationTarget(mi.Type, mi.URI)
			node.Methods = append(node.Methods, APIMethod{
				HTTPMethod: m.HTTPMethod, AuthType: m.AuthorizationType,
				APIKeyReq: m.APIKeyRequired, Operation: m.OperationName, Integration: integ,
			})
		}
		sort.Slice(node.Methods, func(i, j int) bool { return node.Methods[i].HTTPMethod < node.Methods[j].HTTPMethod })
		res = append(res, node)
	}
	// Path order is tree order once "/" sorts first.
	sort.Slice(res, func(i, j int) bool { return res[i].Path < res[j].Path })
	return res, nil
}

// epochTime renders API Gateway's numeric createdDate, which arrives as epoch
// seconds rather than as an ISO string like the other services use.
func epochTime(sec float64) string {
	if sec == 0 {
		return ""
	}
	return time.Unix(int64(sec), 0).UTC().Format("2006-01-02 15:04:05")
}

// pathDepth counts the segments in a resource path, for indenting the tree.
func pathDepth(p string) int {
	p = strings.Trim(p, "/")
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// integrationTarget reduces an integration URI to the thing it actually hits.
// A Lambda proxy URI is an ARN wrapped in an ARN wrapped in a path, which says
// nothing at a glance; the function name says everything.
func integrationTarget(typ, uri string) (target, svc, href string) {
	if uri == "" {
		return "", "", ""
	}
	if strings.HasPrefix(typ, "AWS") && strings.Contains(uri, ":lambda:path/") {
		// .../functions/arn:aws:lambda:region:acct:function:NAME/invocations
		if i := strings.Index(uri, ":function:"); i >= 0 {
			name := uri[i+len(":function:"):]
			name = strings.TrimSuffix(name, "/invocations")
			if j := strings.Index(name, "/"); j >= 0 {
				name = name[:j]
			}
			return name, "lambda", "/lambda/" + name
		}
	}
	if typ == "HTTP" || typ == "HTTP_PROXY" {
		return uri, "", ""
	}
	return uri, "", ""
}

func (b *backend) APIStages(ctx context.Context, apiID, endpoint string) ([]APIStage, error) {
	var out struct {
		Item []struct {
			StageName      string            `json:"stageName"`
			DeploymentID   string            `json:"deploymentId"`
			Description    string            `json:"description"`
			CreatedDate    float64           `json:"createdDate"`
			Variables      map[string]string `json:"variables"`
			TracingEnabled bool              `json:"tracingEnabled"`
			CacheEnabled   bool              `json:"cacheClusterEnabled"`
		} `json:"item"`
	}
	if err := b.apigwGet(ctx, "/restapis/"+url.PathEscape(apiID)+"/stages", &out); err != nil {
		return nil, err
	}
	stages := make([]APIStage, 0, len(out.Item))
	for _, s := range out.Item {
		stages = append(stages, APIStage{
			Name: s.StageName, DeploymentID: s.DeploymentID, Description: s.Description,
			Created: epochTime(s.CreatedDate), Variables: s.Variables,
			Tracing: s.TracingEnabled, CacheEnabled: s.CacheEnabled,
			InvokeBase: "http://" + endpoint + "/_aws/execute-api/" + apiID + "/" + s.StageName,
		})
	}
	sort.Slice(stages, func(i, j int) bool { return stages[i].Name < stages[j].Name })
	return stages, nil
}

// APICallResult is one request sent through the execute-api plane.
type APICallResult struct {
	Status  int
	Headers map[string]string
	Body    string
	Took    string
	URL     string
	Method  string
}

// InvokeAPI sends a request through the deployed stage, exactly as an outside
// caller would. This is the part of an API that cannot be read off a
// definition: whether the integration actually answers.
func (b *backend) InvokeAPI(ctx context.Context, apiID, stage, method, path, body string) (*APICallResult, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target := b.base + "/_aws/execute-api/" + url.PathEscape(apiID) + "/" + url.PathEscape(stage) + path
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), target, rdr)
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := b.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	out := &APICallResult{
		Status: resp.StatusCode, Body: string(raw), Method: strings.ToUpper(method),
		Took:    time.Since(start).Round(time.Millisecond).String(),
		URL:     "/_aws/execute-api/" + apiID + "/" + stage + path,
		Headers: map[string]string{},
	}
	for _, h := range []string{"Content-Type", "X-Amzn-Requestid", "X-Amzn-Errortype"} {
		if v := resp.Header.Get(h); v != "" {
			out.Headers[h] = v
		}
	}
	return out, nil
}
