package iam_test

import (
	"context"
	"testing"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/doze-dev/doze-aws/iam"
)

// aws:SecureTransport is "true" for a local request (plan decision D7): the
// endpoint is plain HTTP on a loopback address, and it stands in for the HTTPS
// endpoint the same SDK would use on AWS. This is the identity-policy half,
// asked of the running stack: a grant that requires TLS admits, and one that
// requires its absence does not. The condition context used to be measured
// from the connection here, which made the first of these refuse every call.
func TestAnIdentityPolicyThatRequiresTLSAdmitsLocally(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stack(t, iam.ModeEnforce)

	allowWhen := func(tls string) string {
		return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:ListQueues","Resource":"*",` +
			`"Condition":{"Bool":{"aws:SecureTransport":"` + tls + `"}}}]}`
	}
	requires := sqsClient(userConfig(t, root, "tls-required", allowWhen("true")), endpoint)
	if _, err := requires.ListQueues(ctx, &awssqs.ListQueuesInput{}); err != nil {
		t.Errorf("a grant that requires TLS refused a local call: %v", err)
	}
	forbids := sqsClient(userConfig(t, root, "tls-forbidden", allowWhen("false")), endpoint)
	if _, err := forbids.ListQueues(ctx, &awssqs.ListQueuesInput{}); err == nil {
		t.Error("a grant that requires NO TLS admitted: the key is not true")
	}
}
