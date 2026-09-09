package cloudformation

// A nested stack's resources are looked up by what !Ref yields, which is
// the prefixed name: a child carrying a REST route tree or an HTTP API
// (the CDK's usual nested shape) transpiles inside its parent.

import (
	"strings"
	"testing"
)

const nestedAPIParent = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Fn:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: handler
      Runtime: provided.al2
      Handler: bootstrap
      Role: arn:aws:iam::000000000000:role/x
      Code: {S3Bucket: b, S3Key: k}
  Apis:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: https://s3.us-east-1.amazonaws.com/staging/apis.yaml
      Parameters:
        FnArn: !GetAtt Fn.Arn
`

const nestedAPIChild = `
AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  FnArn:
    Type: String
Resources:
  Rest:
    Type: AWS::ApiGateway::RestApi
    Properties:
      Name: rest
  Items:
    Type: AWS::ApiGateway::Resource
    Properties:
      RestApiId: !Ref Rest
      ParentId: !GetAtt Rest.RootResourceId
      PathPart: items
  GetItems:
    Type: AWS::ApiGateway::Method
    Properties:
      RestApiId: !Ref Rest
      ResourceId: !Ref Items
      HttpMethod: GET
      AuthorizationType: NONE
      Integration:
        Type: AWS_PROXY
        IntegrationHttpMethod: POST
        Uri: !Sub arn:aws:apigateway:${AWS::Region}:lambda:path/2015-03-31/functions/${FnArn}/invocations
  Http:
    Type: AWS::ApiGatewayV2::Api
    Properties:
      Name: http
      ProtocolType: HTTP
  Integ:
    Type: AWS::ApiGatewayV2::Integration
    Properties:
      ApiId: !Ref Http
      IntegrationType: AWS_PROXY
      IntegrationUri: !Ref FnArn
      PayloadFormatVersion: "2.0"
  Route:
    Type: AWS::ApiGatewayV2::Route
    Properties:
      ApiId: !Ref Http
      RouteKey: GET /things
      Target: !Join ["/", ["integrations", !Ref Integ]]
`

func TestNestedStackWithAPIsTranspiles(t *testing.T) {
	fetch := mapFetcher(map[string]string{"https://s3.us-east-1.amazonaws.com/staging/apis.yaml": nestedAPIChild})
	tmpl, err := Parse([]byte(nestedAPIParent))
	if err != nil {
		t.Fatal(err)
	}
	sf, rep, err := Transpile(tmpl, TranspileOptions{StackName: "app", FetchTemplate: fetch})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, rejected := rep.Counts(); rejected > 0 {
		t.Fatalf("rejected: %+v", rep.Entries)
	}
	rest, ok := sf.APIs["rest"]
	if !ok || len(rest.Routes) != 1 || rest.Routes[0].Path != "/items" || rest.Routes[0].Lambda != "handler" {
		t.Fatalf("REST route tree under the nested prefix: %+v (apis %v)", rest, sf.APIs)
	}
	http, ok := sf.APIs["http"]
	if !ok || http.Protocol != "HTTP" || len(http.Routes) != 1 || http.Routes[0].Path != "/things" || http.Routes[0].Lambda != "handler" {
		t.Fatalf("HTTP API under the nested prefix: %+v", http)
	}
	// The child's resources are reported under the prefixed names the
	// parent's scope refers to them by.
	var prefixed bool
	for _, e := range rep.Nested[0].Report.Entries {
		if strings.HasPrefix(e.Name, "Apis-") {
			prefixed = true
		}
	}
	if !prefixed {
		t.Fatalf("child resources were not prefixed: %+v", rep.Nested[0].Report.Entries)
	}
}
