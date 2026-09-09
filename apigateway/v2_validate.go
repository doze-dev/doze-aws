package apigateway

// HTTP API (apigatewayv2) input validation: the route table that names the
// operation a /v2/... request is, and the constraints each one carries.
// Generated from AWS's apigatewayv2 model (dzaudit cases apigatewayv2 and
// dzaudit routes apigatewayv2) for the operations doze-aws serves, with
// members in the wire spelling; replayed by v2_parity_test.go. Do not edit.

import (
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// routesV2 are ordered most-specific first, like routes.
var routesV2 = []route{
	{Op: "DeleteRouteSettings", Method: "DELETE", Segs: []string{"v2", "apis", "", "stages", "", "routesettings", ""}, Labels: []string{"", "", "apiId", "", "stageName", "", "routeKey"}},
	{Op: "ResetAuthorizersCache", Method: "DELETE", Segs: []string{"v2", "apis", "", "stages", "", "cache", "authorizers"}, Labels: []string{"", "", "apiId", "", "stageName", "", ""}},
	{Op: "DeleteAccessLogSettings", Method: "DELETE", Segs: []string{"v2", "apis", "", "stages", "", "accesslogsettings"}, Labels: []string{"", "", "apiId", "", "stageName", ""}},
	{Op: "DeleteAuthorizer", Method: "DELETE", Segs: []string{"v2", "apis", "", "authorizers", ""}, Labels: []string{"", "", "apiId", "", "authorizerId"}},
	{Op: "DeleteDeployment", Method: "DELETE", Segs: []string{"v2", "apis", "", "deployments", ""}, Labels: []string{"", "", "apiId", "", "deploymentId"}},
	{Op: "DeleteIntegration", Method: "DELETE", Segs: []string{"v2", "apis", "", "integrations", ""}, Labels: []string{"", "", "apiId", "", "integrationId"}},
	{Op: "DeleteRoute", Method: "DELETE", Segs: []string{"v2", "apis", "", "routes", ""}, Labels: []string{"", "", "apiId", "", "routeId"}},
	{Op: "DeleteStage", Method: "DELETE", Segs: []string{"v2", "apis", "", "stages", ""}, Labels: []string{"", "", "apiId", "", "stageName"}},
	{Op: "GetAuthorizer", Method: "GET", Segs: []string{"v2", "apis", "", "authorizers", ""}, Labels: []string{"", "", "apiId", "", "authorizerId"}},
	{Op: "GetDeployment", Method: "GET", Segs: []string{"v2", "apis", "", "deployments", ""}, Labels: []string{"", "", "apiId", "", "deploymentId"}},
	{Op: "GetIntegration", Method: "GET", Segs: []string{"v2", "apis", "", "integrations", ""}, Labels: []string{"", "", "apiId", "", "integrationId"}},
	{Op: "GetRoute", Method: "GET", Segs: []string{"v2", "apis", "", "routes", ""}, Labels: []string{"", "", "apiId", "", "routeId"}},
	{Op: "GetStage", Method: "GET", Segs: []string{"v2", "apis", "", "stages", ""}, Labels: []string{"", "", "apiId", "", "stageName"}},
	{Op: "UpdateAuthorizer", Method: "PATCH", Segs: []string{"v2", "apis", "", "authorizers", ""}, Labels: []string{"", "", "apiId", "", "authorizerId"}},
	{Op: "UpdateDeployment", Method: "PATCH", Segs: []string{"v2", "apis", "", "deployments", ""}, Labels: []string{"", "", "apiId", "", "deploymentId"}},
	{Op: "UpdateIntegration", Method: "PATCH", Segs: []string{"v2", "apis", "", "integrations", ""}, Labels: []string{"", "", "apiId", "", "integrationId"}},
	{Op: "UpdateRoute", Method: "PATCH", Segs: []string{"v2", "apis", "", "routes", ""}, Labels: []string{"", "", "apiId", "", "routeId"}},
	{Op: "UpdateStage", Method: "PATCH", Segs: []string{"v2", "apis", "", "stages", ""}, Labels: []string{"", "", "apiId", "", "stageName"}},
	{Op: "CreateAuthorizer", Method: "POST", Segs: []string{"v2", "apis", "", "authorizers"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "CreateDeployment", Method: "POST", Segs: []string{"v2", "apis", "", "deployments"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "CreateIntegration", Method: "POST", Segs: []string{"v2", "apis", "", "integrations"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "CreateRoute", Method: "POST", Segs: []string{"v2", "apis", "", "routes"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "CreateStage", Method: "POST", Segs: []string{"v2", "apis", "", "stages"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "DeleteCorsConfiguration", Method: "DELETE", Segs: []string{"v2", "apis", "", "cors"}, Labels: []string{"", "", "apiId", ""}},
	{Op: "GetAuthorizers", Method: "GET", Segs: []string{"v2", "apis", "", "authorizers"}, Labels: []string{"", "", "apiId", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
	{Op: "GetDeployments", Method: "GET", Segs: []string{"v2", "apis", "", "deployments"}, Labels: []string{"", "", "apiId", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
	{Op: "GetIntegrations", Method: "GET", Segs: []string{"v2", "apis", "", "integrations"}, Labels: []string{"", "", "apiId", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
	{Op: "GetRoutes", Method: "GET", Segs: []string{"v2", "apis", "", "routes"}, Labels: []string{"", "", "apiId", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
	{Op: "GetStages", Method: "GET", Segs: []string{"v2", "apis", "", "stages"}, Labels: []string{"", "", "apiId", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
	{Op: "DeleteApi", Method: "DELETE", Segs: []string{"v2", "apis", ""}, Labels: []string{"", "", "apiId"}},
	{Op: "GetApi", Method: "GET", Segs: []string{"v2", "apis", ""}, Labels: []string{"", "", "apiId"}},
	{Op: "GetTags", Method: "GET", Segs: []string{"v2", "tags", ""}, Labels: []string{"", "", "resourceArn"}},
	{Op: "TagResource", Method: "POST", Segs: []string{"v2", "tags", ""}, Labels: []string{"", "", "resourceArn"}},
	{Op: "UntagResource", Method: "DELETE", Segs: []string{"v2", "tags", ""}, Labels: []string{"", "", "resourceArn"}, Query: map[string]string{"tagKeys": "tagKeys"}, QueryList: map[string]bool{"tagKeys": true}},
	{Op: "UpdateApi", Method: "PATCH", Segs: []string{"v2", "apis", ""}, Labels: []string{"", "", "apiId"}},
	{Op: "CreateApi", Method: "POST", Segs: []string{"v2", "apis"}, Labels: []string{"", ""}},
	{Op: "GetApis", Method: "GET", Segs: []string{"v2", "apis"}, Labels: []string{"", ""}, Query: map[string]string{"maxResults": "maxResults", "nextToken": "nextToken"}},
}

var constraintTablesV2 = map[string][]modelcheck.Constraint{
	"CreateApi": {
		{Path: "corsConfiguration.maxAge", Kind: modelcheck.KindRange, Min: -1, Max: 86400},
		{Path: "ipAddressType", Kind: modelcheck.KindEnum, Enum: []string{"ipv4", "dualstack"}},
		{Path: "name", Kind: modelcheck.KindRequired},
		{Path: "protocolType", Kind: modelcheck.KindEnum, Enum: []string{"HTTP", "WEBSOCKET"}},
		{Path: "protocolType", Kind: modelcheck.KindRequired},
	},
	"CreateAuthorizer": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizerResultTtlInSeconds", Kind: modelcheck.KindRange, Min: 0, Max: 3600},
		{Path: "authorizerType", Kind: modelcheck.KindEnum, Enum: []string{"REQUEST", "JWT"}},
		{Path: "authorizerType", Kind: modelcheck.KindRequired},
		{Path: "identitySource", Kind: modelcheck.KindRequired},
		{Path: "name", Kind: modelcheck.KindRequired},
	},
	"CreateDeployment": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"CreateIntegration": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "connectionType", Kind: modelcheck.KindEnum, Enum: []string{"INTERNET", "VPC_LINK"}},
		{Path: "contentHandlingStrategy", Kind: modelcheck.KindEnum, Enum: []string{"CONVERT_TO_BINARY", "CONVERT_TO_TEXT"}},
		{Path: "integrationType", Kind: modelcheck.KindEnum, Enum: []string{"HTTP_PROXY", "AWS_PROXY", "AWS", "HTTP", "MOCK"}},
		{Path: "integrationType", Kind: modelcheck.KindRequired},
		{Path: "passthroughBehavior", Kind: modelcheck.KindEnum, Enum: []string{"NEVER", "WHEN_NO_TEMPLATES", "WHEN_NO_MATCH"}},
		{Path: "timeoutInMillis", Kind: modelcheck.KindRange, Min: 50, Max: 30000},
	},
	"CreateRoute": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizationType", Kind: modelcheck.KindEnum, Enum: []string{"NONE", "AWS_IAM", "CUSTOM", "JWT"}},
		{Path: "routeKey", Kind: modelcheck.KindRequired},
	},
	"CreateStage": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "defaultRouteSettings.loggingLevel", Kind: modelcheck.KindEnum, Enum: []string{"ERROR", "INFO", "OFF"}},
		{Path: "routeSettings{}.loggingLevel", Kind: modelcheck.KindEnum, Enum: []string{"ERROR", "INFO", "OFF"}},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"DeleteAccessLogSettings": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"DeleteApi": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"DeleteAuthorizer": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizerId", Kind: modelcheck.KindRequired},
	},
	"DeleteCorsConfiguration": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"DeleteDeployment": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
	},
	"DeleteIntegration": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "integrationId", Kind: modelcheck.KindRequired},
	},
	"DeleteRoute": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "routeId", Kind: modelcheck.KindRequired},
	},
	"DeleteRouteSettings": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "routeKey", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"DeleteStage": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"GetApi": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetAuthorizer": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizerId", Kind: modelcheck.KindRequired},
	},
	"GetAuthorizers": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetDeployment": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
	},
	"GetDeployments": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetIntegration": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "integrationId", Kind: modelcheck.KindRequired},
	},
	"GetIntegrations": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetRoute": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "routeId", Kind: modelcheck.KindRequired},
	},
	"GetRoutes": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetStage": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"GetStages": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
	},
	"GetTags": {
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
	},
	"ResetAuthorizersCache": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
	"TagResource": {
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
	},
	"UntagResource": {
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
		{Path: "tagKeys", Kind: modelcheck.KindRequired},
	},
	"UpdateApi": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "corsConfiguration.maxAge", Kind: modelcheck.KindRange, Min: -1, Max: 86400},
		{Path: "ipAddressType", Kind: modelcheck.KindEnum, Enum: []string{"ipv4", "dualstack"}},
	},
	"UpdateAuthorizer": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizerId", Kind: modelcheck.KindRequired},
		{Path: "authorizerResultTtlInSeconds", Kind: modelcheck.KindRange, Min: 0, Max: 3600},
		{Path: "authorizerType", Kind: modelcheck.KindEnum, Enum: []string{"REQUEST", "JWT"}},
	},
	"UpdateDeployment": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "deploymentId", Kind: modelcheck.KindRequired},
	},
	"UpdateIntegration": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "connectionType", Kind: modelcheck.KindEnum, Enum: []string{"INTERNET", "VPC_LINK"}},
		{Path: "contentHandlingStrategy", Kind: modelcheck.KindEnum, Enum: []string{"CONVERT_TO_BINARY", "CONVERT_TO_TEXT"}},
		{Path: "integrationId", Kind: modelcheck.KindRequired},
		{Path: "integrationType", Kind: modelcheck.KindEnum, Enum: []string{"AWS", "HTTP", "MOCK", "HTTP_PROXY", "AWS_PROXY"}},
		{Path: "passthroughBehavior", Kind: modelcheck.KindEnum, Enum: []string{"WHEN_NO_MATCH", "NEVER", "WHEN_NO_TEMPLATES"}},
		{Path: "timeoutInMillis", Kind: modelcheck.KindRange, Min: 50, Max: 30000},
	},
	"UpdateRoute": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "authorizationType", Kind: modelcheck.KindEnum, Enum: []string{"CUSTOM", "JWT", "NONE", "AWS_IAM"}},
		{Path: "routeId", Kind: modelcheck.KindRequired},
	},
	"UpdateStage": {
		{Path: "apiId", Kind: modelcheck.KindRequired},
		{Path: "defaultRouteSettings.loggingLevel", Kind: modelcheck.KindEnum, Enum: []string{"INFO", "OFF", "ERROR"}},
		{Path: "routeSettings{}.loggingLevel", Kind: modelcheck.KindEnum, Enum: []string{"ERROR", "INFO", "OFF"}},
		{Path: "stageName", Kind: modelcheck.KindRequired},
	},
}
