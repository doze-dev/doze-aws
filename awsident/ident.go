// Package awsident defines the local-AWS identity a doze-aws instance presents:
// a region, an account, and one set of throwaway credentials.
//
// The default values match what LocalStack uses, so tools and copy-pasted
// snippets that assume them keep working. Signatures are parsed but never
// verified (this is a local emulator — the identity is asserted, not
// authenticated), so the credential values only need to exist for SDKs that
// refuse to sign without them.
//
// # Identity is a value, not a constant
//
// Region and AccountID began as package constants, which meant every ARN in the
// tree was minted from the same two strings and an instance could not be given
// an account of its own. They are now the DEFAULTS behind an Identity value that
// a Stack carries and hands to each service.
//
// The package-level ARN and GlobalARN remain as wrappers over Default(), so the
// ~80 files that mint ARNs can move onto an instance's identity a package at a
// time rather than in one commit. They are the migration's remaining tail: when
// nothing calls them, they go, and the compiler finds anything left behind.
package awsident

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// The conventional local-AWS identity, and the default for any field an
// Identity leaves empty.
const (
	Region          = "us-east-1"
	AccountID       = "000000000000"
	AccessKeyID     = "test"
	SecretAccessKey = "test"
)

// Identity is the region and account one instance mints ARNs for.
//
// The zero value is usable and means the defaults above. That is deliberate:
// during the migration off the package-level constants, a service whose identity
// has not been plumbed yet keeps minting the ARNs it always did, so the tree
// stays green while it moves package by package.
//
// It also means an unplumbed site cannot be spotted by reading the ARN — it
// looks exactly right. The guard is a test that runs a stack on a NON-default
// identity and asserts every ARN follows it; that is what turns a missed site
// from invisible into a failure.
type Identity struct {
	Region    string
	AccountID string
}

// Default is the identity an instance gets when it is not configured.
func Default() Identity { return Identity{Region: Region, AccountID: AccountID} }

// withDefaults fills empty fields. See the note on Identity about why the zero
// value resolves rather than erroring.
func (id Identity) withDefaults() Identity {
	if id.Region == "" {
		id.Region = Region
	}
	if id.AccountID == "" {
		id.AccountID = AccountID
	}
	return id
}

// Account is the account id, resolved through the defaults. Read this rather
// than the field: a zero Identity has an empty AccountID and means the default,
// so the field alone would put an empty account into a URL.
func (id Identity) Account() string { return id.withDefaults().AccountID }

// RegionName is the region, resolved through the defaults. The method cannot be
// called Region because that is the field's name.
func (id Identity) RegionName() string { return id.withDefaults().Region }

// ARN builds an ARN for a resource of the given service, e.g.
// ARN("sqs", "my-queue") -> arn:aws:sqs:us-east-1:000000000000:my-queue.
func (id Identity) ARN(service, resource string) string {
	id = id.withDefaults()
	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", service, id.Region, id.AccountID, resource)
}

// GlobalARN builds an ARN for a service with no region component (IAM, STS),
// e.g. GlobalARN("iam", "user/test") -> arn:aws:iam::000000000000:user/test.
func (id Identity) GlobalARN(service, resource string) string {
	return fmt.Sprintf("arn:aws:%s::%s:%s", service, id.withDefaults().AccountID, resource)
}

// FunctionURL is the URL a function URL config reports: the gateway's path form
// when the endpoint is known, AWS's own host form otherwise. The gateway serves
// both.
func (id Identity) FunctionURL(urlID, endpoint string) string {
	if endpoint != "" {
		return strings.TrimRight(endpoint, "/") + "/_aws/lambda-url/" + urlID + "/"
	}
	return "https://" + urlID + ".lambda-url." + id.withDefaults().Region + ".on.aws/"
}

// ARN builds an ARN under the default identity.
//
// Deprecated: use an instance's Identity. This exists so the tree can migrate a
// package at a time; it will be removed once nothing calls it.
func ARN(service, resource string) string { return Default().ARN(service, resource) }

// GlobalARN builds a region-less ARN under the default identity.
//
// Deprecated: use an instance's Identity, for the reason on ARN.
func GlobalARN(service, resource string) string { return Default().GlobalARN(service, resource) }

// FunctionURL reports a function URL under the default identity.
//
// Deprecated: use an instance's Identity, for the reason on ARN.
func FunctionURL(id, endpoint string) string { return Default().FunctionURL(id, endpoint) }

// FunctionURLID is the 32-character lowercase id a Lambda function URL carries,
// derived from the function name so a redeploy addresses the same URL and a
// template can know it before the function exists.
//
// It depends on no identity — it is a hash of the name — so it stays a plain
// function.
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
