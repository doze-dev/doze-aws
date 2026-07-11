package sts_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
)

func TestSDKAssumeRoleWithSAML(t *testing.T) {
	ctx := context.Background()
	c := stsClient(startStack(t))

	out, err := c.AssumeRoleWithSAML(ctx, &awssts.AssumeRoleWithSAMLInput{
		PrincipalArn:  aws.String("arn:aws:iam::000000000000:saml-provider/p"),
		RoleArn:       aws.String("arn:aws:iam::000000000000:role/r"),
		SAMLAssertion: aws.String("PHNhbWw+"),
	})
	if err != nil || out.Credentials == nil {
		t.Fatalf("AssumeRoleWithSAML = %+v err=%v", out, err)
	}
}
