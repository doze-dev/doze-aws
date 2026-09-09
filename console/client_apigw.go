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

	"github.com/doze-dev/doze-aws/awsident"
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
	// AccessLogGroup is the access log's group, "" when none is set;
	// ExecutionLogGroup is the execution log's group when the stage-wide
	// level is INFO or ERROR, "" when logging is off.
	AccessLogGroup    string
	ExecutionLogGroup string
	LogLevel          string
}

// apigwSign stamps the SigV4-shaped credential scope the gateway routes
// unprefixed paths by. /restapis is recognised by path; /tags/{arn} is not —
// a real SDK's signature names the service, so the console's does too.
func apigwSign(req *http.Request) {
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260101/"+awsident.Region+"/apigateway/aws4_request")
}

func (b *backend) apigwGet(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", b.base+path, nil)
	apigwSign(req)
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
			AccessLog      struct {
				DestinationArn string `json:"destinationArn"`
			} `json:"accessLogSettings"`
			MethodSettings map[string]struct {
				LoggingLevel string `json:"loggingLevel"`
			} `json:"methodSettings"`
		} `json:"item"`
	}
	if err := b.apigwGet(ctx, "/restapis/"+url.PathEscape(apiID)+"/stages", &out); err != nil {
		return nil, err
	}
	stages := make([]APIStage, 0, len(out.Item))
	for _, s := range out.Item {
		st := APIStage{
			Name: s.StageName, DeploymentID: s.DeploymentID, Description: s.Description,
			Created: epochTime(s.CreatedDate), Variables: s.Variables,
			Tracing: s.TracingEnabled, CacheEnabled: s.CacheEnabled,
			InvokeBase: "http://" + endpoint + "/_aws/execute-api/" + apiID + "/" + s.StageName,
		}
		if _, rest, ok := strings.Cut(s.AccessLog.DestinationArn, ":log-group:"); ok {
			st.AccessLogGroup = strings.TrimSuffix(rest, ":*")
		}
		if level := s.MethodSettings["*/*"].LoggingLevel; level != "" && level != "OFF" {
			st.LogLevel = level
			st.ExecutionLogGroup = "API-Gateway-Execution-Logs_" + apiID + "/" + s.StageName
		}
		stages = append(stages, st)
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

// ---- control-plane mutations: the build-an-API path ----

// apigwJSON sends one control-plane request with a JSON body (or none).
func (b *backend) apigwJSON(ctx context.Context, method, path string, in any) ([]byte, error) {
	var body io.Reader
	if in != nil {
		buf, _ := json.Marshal(in)
		body = bytes.NewReader(buf)
	}
	req, _ := http.NewRequestWithContext(ctx, method, b.base+path, body)
	apigwSign(req)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return b.do(req)
}

// patchOps encodes API Gateway's JSON-patch dialect.
func patchOps(ops map[string]string) map[string]any {
	list := make([]map[string]string, 0, len(ops))
	for _, p := range sortedKeysOf(ops) {
		list = append(list, map[string]string{"op": "replace", "path": p, "value": ops[p]})
	}
	return map[string]any{"patchOperations": list}
}

// CreateRestAPI makes an empty API — a root resource and nothing else.
func (b *backend) CreateRestAPI(ctx context.Context, name, description string) (string, error) {
	body, err := b.apigwJSON(ctx, "POST", "/restapis",
		map[string]any{"name": name, "description": description})
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateRestAPI renames or re-describes an API (UpdateRestApi, PATCH).
func (b *backend) UpdateRestAPI(ctx context.Context, id string, ops map[string]string) error {
	_, err := b.apigwJSON(ctx, "PATCH", "/restapis/"+url.PathEscape(id), patchOps(ops))
	return err
}

// DeleteRestAPI removes the API and everything under it.
func (b *backend) DeleteRestAPI(ctx context.Context, id string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/restapis/"+url.PathEscape(id), nil)
	return err
}

// CreateAPIResource adds one path part under a parent (CreateResource).
func (b *backend) CreateAPIResource(ctx context.Context, apiID, parentID, pathPart string) error {
	_, err := b.apigwJSON(ctx, "POST",
		"/restapis/"+url.PathEscape(apiID)+"/resources/"+url.PathEscape(parentID),
		map[string]any{"pathPart": pathPart})
	return err
}

// DeleteAPIResource removes a resource and, with it, its subtree.
func (b *backend) DeleteAPIResource(ctx context.Context, apiID, resourceID string) error {
	_, err := b.apigwJSON(ctx, "DELETE",
		"/restapis/"+url.PathEscape(apiID)+"/resources/"+url.PathEscape(resourceID), nil)
	return err
}

// RenameAPIResource changes a resource's path part (UpdateResource, PATCH).
func (b *backend) RenameAPIResource(ctx context.Context, apiID, resourceID, pathPart string) error {
	_, err := b.apigwJSON(ctx, "PATCH",
		"/restapis/"+url.PathEscape(apiID)+"/resources/"+url.PathEscape(resourceID),
		patchOps(map[string]string{"/pathPart": pathPart}))
	return err
}

func methodPath(apiID, resourceID, verb string) string {
	return "/restapis/" + url.PathEscape(apiID) + "/resources/" + url.PathEscape(resourceID) +
		"/methods/" + url.PathEscape(verb)
}

// PutAPIMethod declares a verb on a resource (PutMethod). A re-put keeps the
// integration, so editing auth does not unwire the backend.
func (b *backend) PutAPIMethod(ctx context.Context, apiID, resourceID, verb, authType, authorizerID string, apiKey bool) error {
	in := map[string]any{"authorizationType": authType, "apiKeyRequired": apiKey}
	if authType == "CUSTOM" {
		in["authorizerId"] = authorizerID
	}
	_, err := b.apigwJSON(ctx, "PUT", methodPath(apiID, resourceID, verb), in)
	return err
}

// DeleteAPIMethod removes the verb, integration and all.
func (b *backend) DeleteAPIMethod(ctx context.Context, apiID, resourceID, verb string) error {
	_, err := b.apigwJSON(ctx, "DELETE", methodPath(apiID, resourceID, verb), nil)
	return err
}

// PutAPIIntegration wires a method to its backend (PutIntegration). A Lambda
// target is spelled as the proxy-invocation URI AWS uses; HTTP targets pass
// the URL through; MOCK needs nothing.
func (b *backend) PutAPIIntegration(ctx context.Context, apiID, resourceID, verb, typ, target string) error {
	in := map[string]any{"type": typ}
	switch typ {
	case "AWS_PROXY", "AWS":
		in["uri"] = "arn:aws:apigateway:" + awsident.Region + ":lambda:path/2015-03-31/functions/" +
			awsident.ARN("lambda", "function:"+target) + "/invocations"
		in["integrationHttpMethod"] = "POST"
	case "HTTP", "HTTP_PROXY":
		in["uri"] = target
		in["integrationHttpMethod"] = verb
	}
	_, err := b.apigwJSON(ctx, "PUT", methodPath(apiID, resourceID, verb)+"/integration", in)
	return err
}

// DeleteAPIIntegration unwires the backend; the method stays.
func (b *backend) DeleteAPIIntegration(ctx context.Context, apiID, resourceID, verb string) error {
	_, err := b.apigwJSON(ctx, "DELETE", methodPath(apiID, resourceID, verb)+"/integration", nil)
	return err
}

// PutAPIMethodResponse / PutAPIIntegrationResponse declare one status code on
// each half of the response contract (PutMethodResponse /
// PutIntegrationResponse) — the pair AWS's own console writes together when
// you add a response.
func (b *backend) PutAPIMethodResponse(ctx context.Context, apiID, resourceID, verb, status string) error {
	_, err := b.apigwJSON(ctx, "PUT", methodPath(apiID, resourceID, verb)+"/responses/"+url.PathEscape(status),
		map[string]any{})
	return err
}

func (b *backend) DeleteAPIMethodResponse(ctx context.Context, apiID, resourceID, verb, status string) error {
	_, err := b.apigwJSON(ctx, "DELETE", methodPath(apiID, resourceID, verb)+"/responses/"+url.PathEscape(status), nil)
	return err
}

func (b *backend) PutAPIIntegrationResponse(ctx context.Context, apiID, resourceID, verb, status, template string) error {
	in := map[string]any{}
	if template != "" {
		in["responseTemplates"] = map[string]string{"application/json": template}
	}
	_, err := b.apigwJSON(ctx, "PUT",
		methodPath(apiID, resourceID, verb)+"/integration/responses/"+url.PathEscape(status), in)
	return err
}

func (b *backend) DeleteAPIIntegrationResponse(ctx context.Context, apiID, resourceID, verb, status string) error {
	_, err := b.apigwJSON(ctx, "DELETE",
		methodPath(apiID, resourceID, verb)+"/integration/responses/"+url.PathEscape(status), nil)
	return err
}

// MethodDetail is the full picture of one verb: method, both response halves,
// and the integration (GetMethod / GetIntegration / GetMethodResponse /
// GetIntegrationResponse feed it).
type MethodDetail struct {
	Verb, AuthType   string
	AuthorizerID     string
	APIKeyReq        bool
	Integration      APIIntegration
	MethodResponses  []string
	IntegrationResps []APIIntegrationResp
	ResourceID, Path string
}

// APIIntegrationResp is one integration response row.
type APIIntegrationResp struct {
	Status, Template string
}

// APIMethodDetail reads one method in full.
func (b *backend) APIMethodDetail(ctx context.Context, apiID, resourceID, verb string) (*MethodDetail, error) {
	var m struct {
		HTTPMethod        string `json:"httpMethod"`
		AuthorizationType string `json:"authorizationType"`
		AuthorizerID      string `json:"authorizerId"`
		APIKeyRequired    bool   `json:"apiKeyRequired"`
		MethodResponses   map[string]struct {
			StatusCode string `json:"statusCode"`
		} `json:"methodResponses"`
		MethodIntegration struct {
			Type                 string `json:"type"`
			HTTPMethod           string `json:"httpMethod"`
			URI                  string `json:"uri"`
			TimeoutInMillis      int    `json:"timeoutInMillis"`
			IntegrationResponses map[string]struct {
				StatusCode        string            `json:"statusCode"`
				ResponseTemplates map[string]string `json:"responseTemplates"`
			} `json:"integrationResponses"`
		} `json:"methodIntegration"`
	}
	if err := b.apigwGet(ctx, methodPath(apiID, resourceID, verb), &m); err != nil {
		return nil, err
	}
	d := &MethodDetail{
		Verb: m.HTTPMethod, AuthType: m.AuthorizationType, AuthorizerID: m.AuthorizerID, APIKeyReq: m.APIKeyRequired,
		ResourceID: resourceID,
	}
	mi := m.MethodIntegration
	d.Integration = APIIntegration{Type: mi.Type, HTTPMethod: mi.HTTPMethod, URI: mi.URI, Timeout: mi.TimeoutInMillis}
	d.Integration.Target, d.Integration.Svc, d.Integration.Href = integrationTarget(mi.Type, mi.URI)
	for s := range m.MethodResponses {
		d.MethodResponses = append(d.MethodResponses, s)
	}
	sort.Strings(d.MethodResponses)
	for s, ir := range mi.IntegrationResponses {
		d.IntegrationResps = append(d.IntegrationResps, APIIntegrationResp{
			Status: s, Template: ir.ResponseTemplates["application/json"],
		})
	}
	sort.Slice(d.IntegrationResps, func(i, j int) bool { return d.IntegrationResps[i].Status < d.IntegrationResps[j].Status })
	return d, nil
}

// APIDeployment is one immutable deployment snapshot.
type APIDeployment struct {
	ID, Description, Created string
}

// APIDeployments lists an API's deployments (GetDeployments).
func (b *backend) APIDeployments(ctx context.Context, apiID string) ([]APIDeployment, error) {
	var out struct {
		Item []struct {
			ID          string  `json:"id"`
			Description string  `json:"description"`
			CreatedDate float64 `json:"createdDate"`
		} `json:"item"`
	}
	if err := b.apigwGet(ctx, "/restapis/"+url.PathEscape(apiID)+"/deployments", &out); err != nil {
		return nil, err
	}
	deps := make([]APIDeployment, 0, len(out.Item))
	for _, d := range out.Item {
		deps = append(deps, APIDeployment{ID: d.ID, Description: d.Description, Created: epochTime(d.CreatedDate)})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Created > deps[j].Created })
	return deps, nil
}

// CreateAPIDeployment snapshots the current definition and (optionally, with
// a stage name) points a stage at it — the step that makes edits callable.
func (b *backend) CreateAPIDeployment(ctx context.Context, apiID, stage, description string) error {
	in := map[string]any{"description": description}
	if stage != "" {
		in["stageName"] = stage
	}
	_, err := b.apigwJSON(ctx, "POST", "/restapis/"+url.PathEscape(apiID)+"/deployments", in)
	return err
}

// DeleteAPIDeployment removes one snapshot (DeleteDeployment). A stage still
// pointing at it keeps serving; only the record goes.
func (b *backend) DeleteAPIDeployment(ctx context.Context, apiID, depID string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/restapis/"+url.PathEscape(apiID)+"/deployments/"+url.PathEscape(depID), nil)
	return err
}

// CreateAPIStage points a named stage at an existing deployment (CreateStage).
func (b *backend) CreateAPIStage(ctx context.Context, apiID, name, deploymentID, description string) error {
	_, err := b.apigwJSON(ctx, "POST", "/restapis/"+url.PathEscape(apiID)+"/stages",
		map[string]any{"stageName": name, "deploymentId": deploymentID, "description": description})
	return err
}

// UpdateAPIStage repoints or re-describes a stage (UpdateStage, PATCH).
func (b *backend) UpdateAPIStage(ctx context.Context, apiID, name string, ops map[string]string) error {
	_, err := b.apigwJSON(ctx, "PATCH",
		"/restapis/"+url.PathEscape(apiID)+"/stages/"+url.PathEscape(name), patchOps(ops))
	return err
}

// DeleteAPIStage removes the stage; its deployments stay.
func (b *backend) DeleteAPIStage(ctx context.Context, apiID, name string) error {
	_, err := b.apigwJSON(ctx, "DELETE", "/restapis/"+url.PathEscape(apiID)+"/stages/"+url.PathEscape(name), nil)
	return err
}
