package iam

// AWS-managed policies, synthesized rather than vendored.
//
// moto ships AWS's managed-policy corpus as a multi-megabyte generated Python
// file. doze-aws derives the same documents from the naming convention
// instead, which is a few hundred lines and covers policies for services that
// did not exist when this code was written.
//
// The convention AWS actually follows:
//
//	AdministratorAccess              -> *:*
//	ReadOnlyAccess                   -> Get*/List*/Describe* on everything
//	PowerUserAccess                  -> everything except IAM and account admin
//	Amazon<Service>FullAccess        -> <prefix>:*
//	Amazon<Service>ReadOnlyAccess    -> <prefix>:Get*, List*, Describe*
//	AWSLambda<Source>ExecutionRole   -> the source's read actions, plus logs
//
// A name that matches no pattern is reported as not found rather than invented,
// so a template referencing a policy doze-aws cannot model fails loudly.

import (
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// managedPrefix is the ARN prefix AWS-managed policies live under. Note the
// literal "aws" in the account position — that is what distinguishes them from
// customer-managed policies.
const managedPrefix = "arn:aws:iam::aws:policy/"

func isManagedARN(arn string) bool { return strings.HasPrefix(arn, managedPrefix) }

// servicePrefixes maps the display names AWS embeds in policy names to the
// service prefix used in actions. Only services doze-aws can actually serve
// appear here, plus logs, which every Lambda execution role references.
var servicePrefixes = map[string]string{
	"S3":                   "s3",
	"SQS":                  "sqs",
	"SNS":                  "sns",
	"DynamoDB":             "dynamodb",
	"Kinesis":              "kinesis",
	"Lambda":               "lambda",
	"SSM":                  "ssm",
	"SecretsManager":       "secretsmanager",
	"KeyManagementService": "kms",
	"KMS":                  "kms",
	"EventBridge":          "events",
	"CloudWatchEvents":     "events",
	"CloudWatchLogs":       "logs",
	"IAM":                  "iam",
	"STS":                  "sts",
	"SecurityTokenService": "sts",
	"APIGateway":           "apigateway",
	"CloudFormation":       "cloudformation",
	"StepFunctions":        "states",
	"States":               "states",
}

// readVerbs are the action prefixes a ReadOnly policy grants.
var readVerbs = []string{"Get*", "List*", "Describe*", "BatchGet*"}

// lambdaLogActions is the logs grant every AWSLambda*ExecutionRole carries.
var lambdaLogActions = []string{"logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"}

// managedPolicy synthesizes an AWS-managed policy from its ARN, or reports
// that the name matches no known convention.
func managedPolicy(arn string) (*Policy, bool) {
	if !isManagedARN(arn) {
		return nil, false
	}
	path := "/"
	name := strings.TrimPrefix(arn, managedPrefix)
	// Service-role policies live under a sub-path.
	if i := strings.LastIndex(name, "/"); i >= 0 {
		path = "/" + name[:i+1]
		name = name[i+1:]
	}
	doc, ok := synthesize(name)
	if !ok {
		return nil, false
	}
	return &Policy{
		Name: name, Path: path, ID: "ANPAI" + strings.ToUpper(stableSuffix(name)),
		Description:    "AWS managed policy, synthesized by doze-aws from its name",
		DefaultVersion: "v1",
		Versions:       map[string]string{"v1": doc},
		VersionOrder:   []string{"v1"},
	}, true
}

// synthesize builds a policy document for a managed policy name.
func synthesize(name string) (string, bool) {
	switch name {
	case "AdministratorAccess":
		return allowDoc([]string{"*"}, nil), true
	case "ReadOnlyAccess":
		return allowDoc(readVerbs, nil), true
	case "PowerUserAccess":
		// Everything except identity and account administration.
		return notActionDoc([]string{"iam:*", "organizations:*", "account:*"}), true
	case "IAMReadOnlyAccess":
		return allowDoc(prefixed("iam", readVerbs), nil), true
	case "IAMFullAccess":
		return allowDoc([]string{"iam:*"}, nil), true
	}

	// AWSLambda<Source>ExecutionRole: the poller's read actions plus logs.
	if src, ok := strings.CutPrefix(name, "AWSLambda"); ok {
		if src, ok := strings.CutSuffix(src, "ExecutionRole"); ok {
			switch src {
			case "Basic":
				return allowDoc(lambdaLogActions, nil), true
			case "SQSQueue":
				return allowDoc(append([]string{
					"sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes",
				}, lambdaLogActions...), nil), true
			case "DynamoDB":
				return allowDoc(append([]string{
					"dynamodb:DescribeStream", "dynamodb:GetRecords",
					"dynamodb:GetShardIterator", "dynamodb:ListStreams",
				}, lambdaLogActions...), nil), true
			case "Kinesis":
				return allowDoc(append([]string{
					"kinesis:DescribeStream", "kinesis:DescribeStreamSummary",
					"kinesis:GetRecords", "kinesis:GetShardIterator",
					"kinesis:ListShards", "kinesis:ListStreams",
				}, lambdaLogActions...), nil), true
			}
		}
	}

	// Amazon<Service>FullAccess / <Service>ReadOnlyAccess and the AWS* and
	// bare-name spellings AWS uses inconsistently.
	for _, vendor := range []string{"Amazon", "AWS", ""} {
		rest, ok := strings.CutPrefix(name, vendor)
		if !ok {
			continue
		}
		// AWS sometimes separates with an underscore (AWSLambda_FullAccess).
		rest = strings.TrimPrefix(rest, "_")
		if svc, ok := strings.CutSuffix(rest, "FullAccess"); ok {
			if prefix, ok := lookupService(svc); ok {
				return allowDoc([]string{prefix + ":*"}, nil), true
			}
		}
		if svc, ok := strings.CutSuffix(rest, "ReadOnlyAccess"); ok {
			if prefix, ok := lookupService(svc); ok {
				return allowDoc(prefixed(prefix, readVerbs), nil), true
			}
		}
		if svc, ok := strings.CutSuffix(rest, "ReadWrite"); ok {
			if prefix, ok := lookupService(svc); ok {
				return allowDoc([]string{prefix + ":*"}, nil), true
			}
		}
	}
	return "", false
}

// lookupService resolves a display-name fragment to a service prefix, ignoring
// separators and case ("Lambda_", "lambda", "Lambda" all resolve).
func lookupService(svc string) (string, bool) {
	svc = strings.Trim(svc, "_-")
	if svc == "" {
		return "", false
	}
	if p, ok := servicePrefixes[svc]; ok {
		return p, true
	}
	for name, prefix := range servicePrefixes {
		if strings.EqualFold(name, svc) {
			return prefix, true
		}
	}
	return "", false
}

func prefixed(service string, verbs []string) []string {
	out := make([]string, len(verbs))
	for i, v := range verbs {
		out[i] = service + ":" + v
	}
	return out
}

func allowDoc(actions []string, resources []string) string {
	if resources == nil {
		resources = []string{"*"}
	}
	return jsonDoc("Allow", "Action", actions, resources)
}

func notActionDoc(notActions []string) string {
	return jsonDoc("Allow", "NotAction", notActions, []string{"*"})
}

func jsonDoc(effect, actionKey string, actions, resources []string) string {
	var b strings.Builder
	b.WriteString(`{"Version":"2012-10-17","Statement":[{"Effect":"`)
	b.WriteString(effect)
	b.WriteString(`","`)
	b.WriteString(actionKey)
	b.WriteString(`":[`)
	for i, a := range actions {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(a)
		b.WriteByte('"')
	}
	b.WriteString(`],"Resource":[`)
	for i, r := range resources {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(r)
		b.WriteByte('"')
	}
	b.WriteString(`]}]}`)
	return b.String()
}

// stableSuffix derives a deterministic id fragment from a name, so a synthesized
// policy keeps the same PolicyId across restarts.
func stableSuffix(name string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	var h uint32 = 2166136261
	for i := range len(name) {
		h ^= uint32(name[i])
		h *= 16777619
	}
	out := make([]byte, 12)
	for i := range out {
		out[i] = alphabet[h%26]
		h /= 26
		if h == 0 {
			h = 2166136261 ^ uint32(i)
		}
	}
	return string(out)
}

// ManagedARN builds the ARN for an AWS-managed policy name, for callers that
// have a bare name (CloudFormation templates commonly do).
func ManagedARN(name string) string { return managedPrefix + name }

// CustomerARN builds the ARN for a customer-managed policy.
func CustomerARN(path, name string) string {
	return awsident.GlobalARN("iam", "policy"+normPath(path)+name)
}
