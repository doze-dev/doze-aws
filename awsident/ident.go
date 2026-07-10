// Package awsident defines the fixed local-AWS identity that every doze-aws
// service assumes: one region, one account, one set of throwaway credentials.
//
// The values match what LocalStack uses, so tools and copy-pasted snippets that
// assume them keep working. Signatures are parsed but never verified (this is a
// local emulator — the identity is fixed, not authenticated), so the credential
// values only need to exist for SDKs that refuse to sign without them.
package awsident

import "fmt"

// Conventional local-AWS identity.
const (
	Region          = "us-east-1"
	AccountID       = "000000000000"
	AccessKeyID     = "test"
	SecretAccessKey = "test"
)

// ARN builds an AWS ARN for a resource of the given service, e.g.
// ARN("sqs", "my-queue") -> arn:aws:sqs:us-east-1:000000000000:my-queue.
func ARN(service, resource string) string {
	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", service, Region, AccountID, resource)
}

// GlobalARN builds an ARN for a service without a region component (IAM, STS),
// e.g. GlobalARN("iam", "user/test") -> arn:aws:iam::000000000000:user/test.
func GlobalARN(service, resource string) string {
	return fmt.Sprintf("arn:aws:%s::%s:%s", service, AccountID, resource)
}
