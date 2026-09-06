package cloudformation

import "testing"

// A raw AWS::Lambda::Function with {S3Bucket, S3Key} — what the CDK emits
// after uploading an asset to its bootstrap bucket — has to reach the
// provisioner as an s3:// reference. Dropping the bucket turned the key into
// a local path and failed a real `cdk deploy` on "code path … does not
// exist" with the zip sitting in S3.
func TestTranspileFunctionCodeFromS3(t *testing.T) {
	tmpl := `
Resources:
  Fn:
    Type: AWS::Lambda::Function
    Properties:
      Runtime: provided.al2
      Handler: bootstrap
      Code:
        S3Bucket: cdk-hnb659fds-assets-000000000000-us-east-1
        S3Key: 2629e4a5.zip
  Local:
    Type: AWS::Lambda::Function
    Properties:
      Runtime: provided.al2
      Handler: bootstrap
      Code:
        S3Bucket: _local_
        S3Key: /abs/dir
`
	parsed, _ := Parse([]byte(tmpl))
	stack, _, err := Transpile(parsed, TranspileOptions{})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if got := stack.Functions["Fn"].Code; got != "s3://cdk-hnb659fds-assets-000000000000-us-east-1/2629e4a5.zip" {
		t.Errorf("S3 code = %q", got)
	}
	if got := stack.Functions["Local"].Code; got != "/abs/dir" {
		t.Errorf("_local_ code = %q", got)
	}
}
