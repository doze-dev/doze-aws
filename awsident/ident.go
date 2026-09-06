// Package awsident defines the fixed local-AWS identity that every doze-aws
// service assumes: one region, one account, one set of throwaway credentials.
//
// The values match what LocalStack uses, so tools and copy-pasted snippets that
// assume them keep working. Signatures are parsed but never verified (this is a
// local emulator — the identity is fixed, not authenticated), so the credential
// values only need to exist for SDKs that refuse to sign without them.
package awsident

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

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

// FunctionURLID is the 32-character lowercase id a Lambda function URL
// carries, derived from the function name so a redeploy addresses the same
// URL and a template can know it before the function exists.
func FunctionURLID(name string) string {
	sum := sha256.Sum256([]byte("function-url:" + name))
	id := hex.EncodeToString(sum[:])[:32]
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0') // AWS ids are letters and digits; all letters is still valid
		}
		return r
	}, id)
}

// FunctionURL is the URL a function URL config reports: the gateway's path
// form when the endpoint is known, AWS's own host form otherwise. The
// gateway serves both.
func FunctionURL(id, endpoint string) string {
	if endpoint != "" {
		return strings.TrimRight(endpoint, "/") + "/_aws/lambda-url/" + id + "/"
	}
	return "https://" + id + ".lambda-url." + Region + ".on.aws/"
}
