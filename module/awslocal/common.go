// Package awslocal is the shared plumbing for doze's AWS-backed plugin
// modules (S3, SQS, SNS, DynamoDB, …). Each module ships as an out-of-process
// plugin whose service is a plain net/http handler from doze-aws; the plugin
// re-execs ITSELF with a hidden `__serve <service>` argv (see BaseDriver and
// Serve) to become that server, so core supervises it like any other engine —
// lazy boot-on-connect, idle reaping, no external binary, no Docker, no JVM.
//
// This package is a leaf shared by all AWS modules: BaseDriver (plan/serve
// scaffold), PluginMain (whole plugin main()), the wire-protocol clients
// (JSONCallDecode/QueryCall/RESTPost), peer-endpoint env plumbing (peerenv),
// and small formatting helpers. It imports no module, so there is no cycle.
package awslocal

import (
	"fmt"
	"sort"
	"strings"
)

// Conventional local-AWS identity. These match the values LocalStack uses, so
// tools and copy-pasted snippets that assume them keep working.
const (
	Region          = "us-east-1"
	AccountID       = "000000000000"
	AccessKeyID     = "test"
	SecretAccessKey = "test"

	// HealthPath is the readiness endpoint Serve always mounts and WaitReady
	// polls; it is namespaced so it can never collide with a real AWS route.
	HealthPath = "/_doze/health"
)

// ARN builds an AWS ARN for a resource of the given service (e.g.
// ARN("sqs", "my-queue") -> arn:aws:sqs:us-east-1:000000000000:my-queue).
func ARN(service, resource string) string {
	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", service, Region, AccountID, resource)
}

// SortedKeys returns m's keys sorted — the deterministic iteration order every
// human-facing rendering of a map (attribute lines, form entries) wants.
func SortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// KVLine renders a string map as a compact, deterministic "k=v k=v" line —
// the shape Admin result lines use to echo message/publish attributes.
func KVLine(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range SortedKeys(m) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}
