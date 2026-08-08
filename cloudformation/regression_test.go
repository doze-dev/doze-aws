package cloudformation

// Regressions found by running the real CLIs. Every case here is a bug that
// the unit tests passed straight through and only `sam deploy`, `cdk bootstrap`
// or `serverless` exposed.

import (
	"strings"
	"testing"
)

// TestListParameterDefaultRefsToList is what broke `cdk bootstrap`.
//
// Its template declares `TrustedAccountsForLookup` as a CommaDelimitedList
// with an empty default, then Fn::Joins over `!Ref` of it. A default that was
// not coerced by declared type made Ref yield a string, and Join rejected it.
func TestListParameterDefaultRefsToList(t *testing.T) {
	tmpl := `
Parameters:
  TrustedAccounts:
    Type: CommaDelimitedList
    Default: ""
  Regions:
    Type: List<String>
    Default: "us-east-1,eu-west-1"
Conditions:
  HasTrusted: !Not [!Equals ["", !Join ["", !Ref TrustedAccounts]]]
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Join ["-", !Ref Regions]
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("a list-typed default must Ref to a list: %v", err)
	}
	if _, ok := stack.Queues["us-east-1-eu-west-1"]; !ok {
		t.Fatalf("Join over a list parameter = %v", stack.Queues)
	}
}

// TestIgnoredResourcesKeepIdentity is what broke `cdk bootstrap` a second time,
// and would have broken essentially every real template.
//
// An IAM role is ignored — but templates GetAtt its ARN constantly, and CDK's
// bootstrap Refs an ECR repository from an Output. An ignored resource must
// still resolve.
func TestIgnoredResourcesKeepIdentity(t *testing.T) {
	tmpl := `
Resources:
  ExecutionRole:
    Type: AWS::IAM::Role
    Properties:
      RoleName: my-exec-role
      AssumeRolePolicyDocument: {}
  Registry:
    Type: AWS::ECR::Repository
    Properties:
      RepositoryName: assets
  Logs:
    Type: AWS::Logs::LogGroup
  Fn:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: worker
      Runtime: provided.al2
      Handler: bootstrap
      Role: !GetAtt ExecutionRole.Arn
      Code: {S3Bucket: _local_, S3Key: ./build}
Outputs:
  RoleArn:
    Value: !GetAtt ExecutionRole.Arn
  RepoName:
    Value: !Ref Registry
  RepoUri:
    Value: !Sub "${Registry.RepositoryUri}"
  LogGroup:
    Value: !Ref Logs
`
	parsed, err := Parse([]byte(tmpl))
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("references to ignored resources must resolve: %v", err)
	}
	// An explicit name on an ignored resource is honoured.
	if got := rep.Outputs["RoleArn"]; got != "arn:aws:iam::000000000000:role/my-exec-role" {
		t.Errorf("RoleArn = %q", got)
	}
	if got := rep.Outputs["RepoName"]; got != "assets" {
		t.Errorf("RepoName = %q", got)
	}
	if got := rep.Outputs["RepoUri"]; !strings.Contains(got, "assets") {
		t.Errorf("RepoUri = %q", got)
	}
	// With no explicit name, the logical ID stands in.
	if got := rep.Outputs["LogGroup"]; got != "Logs" {
		t.Errorf("LogGroup = %q", got)
	}
	// They are still reported as skipped, not silently mapped.
	if _, ignored, _ := rep.Counts(); ignored != 3 {
		t.Errorf("ignored = %d, want 3", ignored)
	}
}

// TestDerivedBucketNameIsValid is what broke the Serverless Framework.
//
// It declares `ServerlessDeploymentBucket` with no BucketName, so the name
// falls back to the logical ID — which S3 rejects, because bucket names must
// be lowercase.
func TestDerivedBucketNameIsValid(t *testing.T) {
	parsed, err := Parse([]byte(`
Resources:
  ServerlessDeploymentBucket:
    Type: AWS::S3::Bucket
`))
	if err != nil {
		t.Fatal(err)
	}
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stack.Buckets["serverlessdeploymentbucket"]; !ok {
		t.Fatalf("a derived bucket name must be S3-legal, got %v", stack.Buckets)
	}

	// An EXPLICIT name is passed through untouched, so a template real
	// CloudFormation would reject is rejected here too rather than rewritten.
	parsed2, _ := Parse([]byte(`
Resources:
  B:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: KeepMyCase
`))
	stack2, _, err := Transpile(parsed2, TranspileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stack2.Buckets["KeepMyCase"]; !ok {
		t.Fatalf("an explicit name must not be rewritten, got %v", stack2.Buckets)
	}
}

func TestS3SafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ServerlessDeploymentBucket", "serverlessdeploymentbucket"},
		{"My_Bucket", "my-bucket"},
		{"already-fine", "already-fine"},
		{"AB", "ab0"},
		{"--leading", "leading"},
		{"with.dots", "with.dots"},
	}
	for _, c := range cases {
		if got := s3SafeName(c.in); got != c.want {
			t.Errorf("s3SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := s3SafeName(strings.Repeat("x", 100)); len(got) != 63 {
		t.Errorf("long name = %d chars, want 63", len(got))
	}
}
