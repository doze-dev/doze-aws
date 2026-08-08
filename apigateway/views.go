package apigateway

// Wire shapes.
//
// API Gateway's REST-JSON responses use lowerCamelCase field names and omit
// empty values, and clients are strict about both — a `null` where a map is
// expected is a decode error in the Go SDK, so empty collections are rendered
// as `{}` rather than dropped where AWS does the same.

import (
	"sort"
	"strconv"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func viewAPI(api *RestAPI) map[string]any {
	v := map[string]any{
		"id":           api.ID,
		"name":         api.Name,
		"createdDate":  api.Created,
		"apiKeySource": api.APIKeySource,
		"endpointConfiguration": map[string]any{
			"types": orEmptyList(api.EndpointTypes),
		},
		"disableExecuteApiEndpoint": api.DisableExecuteAPI,
		"rootResourceId":            rootID(api),
	}
	putIfStr(v, "description", api.Description)
	putIfStr(v, "version", api.Version)
	putIfStr(v, "policy", api.Policy)
	if len(api.BinaryMediaTypes) > 0 {
		v["binaryMediaTypes"] = api.BinaryMediaTypes
	}
	if api.MinimumCompressionSize != nil {
		v["minimumCompressionSize"] = *api.MinimumCompressionSize
	}
	if len(api.Tags) > 0 {
		v["tags"] = api.Tags
	}
	return v
}

func rootID(api *RestAPI) string {
	if r := api.Root(); r != nil {
		return r.ID
	}
	return ""
}

func viewResource(res *Resource) map[string]any {
	v := map[string]any{"id": res.ID, "path": res.Path}
	putIfStr(v, "parentId", res.ParentID)
	putIfStr(v, "pathPart", res.PathPart)
	if len(res.Methods) > 0 {
		methods := map[string]any{}
		for verb, m := range res.Methods {
			methods[verb] = viewMethod(m)
		}
		v["resourceMethods"] = methods
	}
	return v
}

func viewMethod(m *Method) map[string]any {
	v := map[string]any{
		"httpMethod":        m.HTTPMethod,
		"authorizationType": m.AuthorizationType,
		"apiKeyRequired":    m.APIKeyRequired,
	}
	putIfStr(v, "authorizerId", m.AuthorizerID)
	putIfStr(v, "operationName", m.OperationName)
	putIfStr(v, "requestValidatorId", m.RequestValidatorID)
	if len(m.RequestParameters) > 0 {
		v["requestParameters"] = m.RequestParameters
	}
	if len(m.RequestModels) > 0 {
		v["requestModels"] = m.RequestModels
	}
	if m.Integration != nil {
		v["methodIntegration"] = viewIntegration(m.Integration)
	}
	if len(m.Responses) > 0 {
		out := map[string]any{}
		for code, mr := range m.Responses {
			out[code] = viewMethodResponse(mr)
		}
		v["methodResponses"] = out
	}
	return v
}

func viewIntegration(i *Integration) map[string]any {
	v := map[string]any{"type": i.Type}
	putIfStr(v, "httpMethod", i.HTTPMethod)
	putIfStr(v, "uri", i.URI)
	putIfStr(v, "connectionType", i.ConnectionType)
	putIfStr(v, "credentials", i.Credentials)
	putIfStr(v, "passthroughBehavior", i.PassthroughBehavior)
	putIfStr(v, "contentHandling", i.ContentHandling)
	putIfStr(v, "cacheNamespace", i.CacheNamespace)
	if i.TimeoutInMillis > 0 {
		v["timeoutInMillis"] = i.TimeoutInMillis
	}
	if len(i.RequestTemplates) > 0 {
		v["requestTemplates"] = i.RequestTemplates
	}
	if len(i.RequestParameters) > 0 {
		v["requestParameters"] = i.RequestParameters
	}
	// cacheKeyParameters is a list the SDK decodes into a slice; AWS always
	// emits it, so an absent value would read as a diff in Terraform.
	v["cacheKeyParameters"] = orEmptyList(i.CacheKeyParameters)
	if len(i.Responses) > 0 {
		out := map[string]any{}
		for code, ir := range i.Responses {
			out[code] = viewIntegrationResponse(ir)
		}
		v["integrationResponses"] = out
	}
	return v
}

func viewMethodResponse(mr *MethodResponse) map[string]any {
	v := map[string]any{"statusCode": mr.StatusCode}
	if len(mr.ResponseModels) > 0 {
		v["responseModels"] = mr.ResponseModels
	}
	if len(mr.ResponseParameters) > 0 {
		v["responseParameters"] = mr.ResponseParameters
	}
	return v
}

func viewIntegrationResponse(ir *IntegrationResponse) map[string]any {
	v := map[string]any{"statusCode": ir.StatusCode}
	putIfStr(v, "selectionPattern", ir.SelectionPattern)
	putIfStr(v, "contentHandling", ir.ContentHandling)
	if len(ir.ResponseTemplates) > 0 {
		v["responseTemplates"] = ir.ResponseTemplates
	}
	if len(ir.ResponseParameters) > 0 {
		v["responseParameters"] = ir.ResponseParameters
	}
	return v
}

func viewDeployment(d *Deployment) map[string]any {
	v := map[string]any{"id": d.ID, "createdDate": d.Created}
	putIfStr(v, "description", d.Description)
	return v
}

func viewStage(apiID string, st *Stage) map[string]any {
	v := map[string]any{
		"stageName":           st.Name,
		"createdDate":         st.Created,
		"lastUpdatedDate":     st.Updated,
		"tracingEnabled":      st.TracingEnabled,
		"cacheClusterEnabled": false,
		"cacheClusterStatus":  "NOT_AVAILABLE",
		"methodSettings":      map[string]any{},
	}
	putIfStr(v, "deploymentId", st.DeploymentID)
	putIfStr(v, "description", st.Description)
	if len(st.Variables) > 0 {
		v["variables"] = st.Variables
	}
	if len(st.Tags) > 0 {
		v["tags"] = st.Tags
	}
	// The invoke URL is the practical output: it is what you paste into curl.
	v["invokeUrl"] = InvokeURL(apiID, st.Name)
	return v
}

// InvokeURL is where a deployed stage answers. It is exported because
// CloudFormation and the console both surface it.
func InvokeURL(apiID, stage string) string {
	return "http://127.0.0.1:4566" + ExecutePrefix + apiID + "/" + stage
}

// APIARN is the ARN used to tag a REST API.
func APIARN(apiID string) string {
	return "arn:aws:apigateway:" + awsident.Region + "::/restapis/" + apiID
}

// ---- small helpers ----

func putIfStr(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

func orEmptyList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func requestID() string { return awshttp.RequestID() }
