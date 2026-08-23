package apigateway

// API Gateway's model-derived input validation: the constraint tables, walked
// by internal/modelcheck, and the route table that says which operation a
// request is.
//
// API Gateway speaks restJson1. There is no target header naming the operation
// and no single encoded body — the input is spread across the URI, the query
// string and the JSON body — so the validator has to identify the operation
// from the method and the path before it can look up anything. Both tables are
// generated from AWS's own service model, so a route and the constraints it
// selects cannot drift apart, and both are replayed case by case in
// apigateway/rejection_parity_test.go.

import (
	"regexp"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// route is one operation's REST binding. Segs is the path template with an
// empty string where a label goes, and Labels names that label.
type route struct {
	Op     string
	Method string
	Segs   []string
	Labels []string
	// Query maps a query-string parameter to the input member it carries, for
	// the members the model binds with @httpQuery.
	Query map[string]string
}

// routes are ordered most-specific first, so a longer template wins over a
// shorter one that would also match.
var routes = []route{
	{Op: "DeleteIntegrationResponse", Method: "DELETE", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "", "statusCode"}},
	{Op: "GetIntegrationResponse", Method: "GET", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "", "statusCode"}},
	{Op: "PutIntegrationResponse", Method: "PUT", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "", "statusCode"}},
	{Op: "DeleteMethodResponse", Method: "DELETE", Segs: []string{"restapis", "", "resources", "", "methods", "", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "statusCode"}},
	{Op: "GetMethodResponse", Method: "GET", Segs: []string{"restapis", "", "resources", "", "methods", "", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "statusCode"}},
	{Op: "PutMethodResponse", Method: "PUT", Segs: []string{"restapis", "", "resources", "", "methods", "", "responses", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", "", "statusCode"}},
	{Op: "DeleteIntegration", Method: "DELETE", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration"}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", ""}},
	{Op: "GetIntegration", Method: "GET", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration"}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", ""}},
	{Op: "PutIntegration", Method: "PUT", Segs: []string{"restapis", "", "resources", "", "methods", "", "integration"}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod", ""}},
	{Op: "DeleteMethod", Method: "DELETE", Segs: []string{"restapis", "", "resources", "", "methods", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod"}},
	{Op: "GetMethod", Method: "GET", Segs: []string{"restapis", "", "resources", "", "methods", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod"}},
	{Op: "PutMethod", Method: "PUT", Segs: []string{"restapis", "", "resources", "", "methods", ""}, Labels: []string{"", "restApiId", "", "resourceId", "", "httpMethod"}},
	{Op: "DeleteDeployment", Method: "DELETE", Segs: []string{"restapis", "", "deployments", ""}, Labels: []string{"", "restApiId", "", "deploymentId"}},
	{Op: "GetDeployment", Method: "GET", Segs: []string{"restapis", "", "deployments", ""}, Labels: []string{"", "restApiId", "", "deploymentId"}, Query: map[string]string{"embed": "embed"}},
	{Op: "CreateResource", Method: "POST", Segs: []string{"restapis", "", "resources", ""}, Labels: []string{"", "restApiId", "", "parentId"}},
	{Op: "DeleteResource", Method: "DELETE", Segs: []string{"restapis", "", "resources", ""}, Labels: []string{"", "restApiId", "", "resourceId"}},
	{Op: "GetResource", Method: "GET", Segs: []string{"restapis", "", "resources", ""}, Labels: []string{"", "restApiId", "", "resourceId"}, Query: map[string]string{"embed": "embed"}},
	{Op: "UpdateResource", Method: "PATCH", Segs: []string{"restapis", "", "resources", ""}, Labels: []string{"", "restApiId", "", "resourceId"}},
	{Op: "DeleteStage", Method: "DELETE", Segs: []string{"restapis", "", "stages", ""}, Labels: []string{"", "restApiId", "", "stageName"}},
	{Op: "GetStage", Method: "GET", Segs: []string{"restapis", "", "stages", ""}, Labels: []string{"", "restApiId", "", "stageName"}},
	{Op: "UpdateStage", Method: "PATCH", Segs: []string{"restapis", "", "stages", ""}, Labels: []string{"", "restApiId", "", "stageName"}},
	{Op: "CreateDeployment", Method: "POST", Segs: []string{"restapis", "", "deployments"}, Labels: []string{"", "restApiId", ""}},
	{Op: "GetDeployments", Method: "GET", Segs: []string{"restapis", "", "deployments"}, Labels: []string{"", "restApiId", ""}, Query: map[string]string{"limit": "limit", "position": "position"}},
	{Op: "GetResources", Method: "GET", Segs: []string{"restapis", "", "resources"}, Labels: []string{"", "restApiId", ""}, Query: map[string]string{"embed": "embed", "limit": "limit", "position": "position"}},
	{Op: "CreateStage", Method: "POST", Segs: []string{"restapis", "", "stages"}, Labels: []string{"", "restApiId", ""}},
	{Op: "GetStages", Method: "GET", Segs: []string{"restapis", "", "stages"}, Labels: []string{"", "restApiId", ""}, Query: map[string]string{"deploymentId": "deploymentId"}},
	{Op: "DeleteRestApi", Method: "DELETE", Segs: []string{"restapis", ""}, Labels: []string{"", "restApiId"}},
	{Op: "GetRestApi", Method: "GET", Segs: []string{"restapis", ""}, Labels: []string{"", "restApiId"}},
	{Op: "UpdateRestApi", Method: "PATCH", Segs: []string{"restapis", ""}, Labels: []string{"", "restApiId"}},
	{Op: "CreateRestApi", Method: "POST", Segs: []string{"restapis"}, Labels: []string{""}},
}

var constraintTables = map[string][]modelcheck.Constraint{
	"CreateDeployment": {
		{Path: "cacheClusterSize", Kind: modelcheck.KindEnum, Enum: []string{"6.1", "13.5", "28.4", "58.2", "118", "237", "0.5", "1.6"}},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"CreateResource": {
		{Path: "parentId", Kind: modelcheck.KindRequired},
		{Path: "pathPart", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"CreateRestApi": {
		{Path: "apiKeySource", Kind: modelcheck.KindEnum, Enum: []string{"HEADER", "AUTHORIZER"}},
		{Path: "endpointAccessMode", Kind: modelcheck.KindEnum, Enum: []string{"BASIC", "STRICT"}},
		{Path: "endpointConfiguration.ipAddressType", Kind: modelcheck.KindEnum, Enum: []string{"ipv4", "dualstack"}},
		{Path: "endpointConfiguration.types[]", Kind: modelcheck.KindEnum, Enum: []string{"REGIONAL", "EDGE", "PRIVATE"}},
		{Path: "name", Kind: modelcheck.KindRequired},
		{Path: "securityPolicy", Kind: modelcheck.KindEnum, Enum: []string{"SecurityPolicy_TLS13_1_2_PFS_PQ_2025_09", "SecurityPolicy_TLS13_1_2_2021_06", "TLS_1_0", "SecurityPolicy_TLS13_1_3_2025_09", "SecurityPolicy_TLS13_1_2_FIPS_PQ_2025_09", "SecurityPolicy_TLS13_1_2_FIPS_PFS_PQ_2025_09", "SecurityPolicy_TLS13_1_2_PQ_2025_09", "SecurityPolicy_TLS13_2025_EDGE", "SecurityPolicy_TLS12_PFS_2025_EDGE", "SecurityPolicy_TLS12_2018_EDGE", "TLS_1_2", "SecurityPolicy_TLS13_1_3_FIPS_2025_09"}},
	},
	"CreateStage": {
		{Path: "cacheClusterSize", Kind: modelcheck.KindEnum, Enum: []string{"1.6", "6.1", "13.5", "28.4", "58.2", "118", "237", "0.5"}},
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"DeleteDeployment": {
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"DeleteIntegration": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"DeleteIntegrationResponse": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"DeleteMethod": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"DeleteMethodResponse": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"DeleteResource": {
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"DeleteRestApi": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"DeleteStage": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"GetDeployment": {
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetDeployments": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetIntegration": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetIntegrationResponse": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"GetMethod": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetMethodResponse": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"GetResource": {
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetResources": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetRestApi": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"GetStage": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"GetStages": {
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"PutIntegration": {
		{Path: "connectionType", Kind: modelcheck.KindEnum, Enum: []string{"INTERNET", "VPC_LINK"}},
		{Path: "contentHandling", Kind: modelcheck.KindEnum, Enum: []string{"CONVERT_TO_BINARY", "CONVERT_TO_TEXT"}},
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "responseTransferMode", Kind: modelcheck.KindEnum, Enum: []string{"BUFFERED", "STREAM"}},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "type", Kind: modelcheck.KindEnum, Enum: []string{"HTTP", "AWS", "MOCK", "HTTP_PROXY", "AWS_PROXY"}},
		{Path: "type", Kind: modelcheck.KindRequired},
	},
	"PutIntegrationResponse": {
		{Path: "contentHandling", Kind: modelcheck.KindEnum, Enum: []string{"CONVERT_TO_BINARY", "CONVERT_TO_TEXT"}},
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"PutMethod": {
		{Path: "authorizationType", Kind: modelcheck.KindRequired},
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"PutMethodResponse": {
		{Path: "httpMethod", Kind: modelcheck.KindRequired},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "statusCode", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[1-5]\d\d$`)},
		{Path: "statusCode", Kind: modelcheck.KindRequired},
	},
	"UpdateResource": {
		{Path: "patchOperations[].op", Kind: modelcheck.KindEnum, Enum: []string{"remove", "replace", "move", "copy", "test", "add"}},
		{Path: "resourceId", Kind: modelcheck.KindRequired},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"UpdateRestApi": {
		{Path: "patchOperations[].op", Kind: modelcheck.KindEnum, Enum: []string{"add", "remove", "replace", "move", "copy", "test"}},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
	},
	"UpdateStage": {
		{Path: "patchOperations[].op", Kind: modelcheck.KindEnum, Enum: []string{"move", "copy", "test", "add", "remove", "replace"}},
		{Path: "restApiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
}
