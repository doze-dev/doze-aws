package ssm_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestSDKParameterAdmin(t *testing.T) {
	ctx := context.Background()
	c := ssmClient(t)

	if _, err := c.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/app/db"), Value: aws.String("v1"), Type: ssmtypes.ParameterTypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}
	// New version + label it, then unlabel.
	pv, _ := c.PutParameter(ctx, &awsssm.PutParameterInput{
		Name: aws.String("/app/db"), Value: aws.String("v2"), Type: ssmtypes.ParameterTypeString, Overwrite: aws.Bool(true),
	})
	if _, err := c.LabelParameterVersion(ctx, &awsssm.LabelParameterVersionInput{
		Name: aws.String("/app/db"), ParameterVersion: aws.Int64(pv.Version), Labels: []string{"prod"},
	}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}
	if _, err := c.UnlabelParameterVersion(ctx, &awsssm.UnlabelParameterVersionInput{
		Name: aws.String("/app/db"), ParameterVersion: aws.Int64(pv.Version), Labels: []string{"prod"},
	}); err != nil {
		t.Fatalf("UnlabelParameterVersion: %v", err)
	}

	// Tags.
	if _, err := c.AddTagsToResource(ctx, &awsssm.AddTagsToResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter, ResourceId: aws.String("/app/db"),
		Tags: []ssmtypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	}); err != nil {
		t.Fatalf("AddTagsToResource: %v", err)
	}
	if _, err := c.RemoveTagsFromResource(ctx, &awsssm.RemoveTagsFromResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter, ResourceId: aws.String("/app/db"), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("RemoveTagsFromResource: %v", err)
	}

	// DeleteParameter.
	if _, err := c.DeleteParameter(ctx, &awsssm.DeleteParameterInput{Name: aws.String("/app/db")}); err != nil {
		t.Fatalf("DeleteParameter: %v", err)
	}
}
