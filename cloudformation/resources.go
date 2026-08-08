package cloudformation

// The resource type registry: how each AWS::* type maps onto the stackfile IR.
//
// Three outcomes are possible for a resource, and the distinction is the whole
// point of this file:
//
//	mapped    doze-aws models it — it becomes stackfile IR and is provisioned.
//	ignored   doze-aws has no analogue but the template is still valid without
//	          it (IAM roles, log groups, permissions). Accepted and REPORTED.
//	rejected  the type belongs to a service doze-aws does not serve. The
//	          template fails rather than deploying half of itself.
//
// The "ignored" tier is a deliberate exception to the project's no-silent-no-op
// rule. Real templates are full of AWS::IAM::Role; refusing them would fail
// essentially every template. So they are accepted — and every one of them
// appears in the report, so nobody discovers the gap in production instead.

import (
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// ignoredTypes are accepted and skipped, with the reason shown in the report.
var ignoredTypes = map[string]string{
	"AWS::IAM::Role":                           "no IAM evaluation during apply; create the role via the iam service if you need it",
	"AWS::IAM::Policy":                         "no IAM evaluation during apply",
	"AWS::IAM::ManagedPolicy":                  "no IAM evaluation during apply",
	"AWS::IAM::InstanceProfile":                "no EC2 locally",
	"AWS::IAM::User":                           "no IAM evaluation during apply",
	"AWS::IAM::Group":                          "no IAM evaluation during apply",
	"AWS::IAM::ServiceLinkedRole":              "no IAM evaluation during apply",
	"AWS::Logs::LogGroup":                      "there is no CloudWatch Logs locally",
	"AWS::Logs::LogStream":                     "there is no CloudWatch Logs locally",
	"AWS::Logs::SubscriptionFilter":            "there is no CloudWatch Logs locally",
	"AWS::CloudWatch::Alarm":                   "there is no CloudWatch locally",
	"AWS::CloudWatch::Dashboard":               "there is no CloudWatch locally",
	"AWS::CDK::Metadata":                       "CDK bookkeeping, no resource behind it",
	"AWS::ECR::Repository":                     "there is no container registry locally (CDK's bootstrap declares one)",
	"AWS::CloudFormation::WaitCondition":       "nothing to wait for locally",
	"AWS::CloudFormation::WaitConditionHandle": "nothing to wait for locally",
	"AWS::SSM::Parameter::Value":               "a parameter reference, not a resource",
}

// Kind classifies what happened to one resource.
type Kind int

const (
	// Mapped means the resource became stackfile IR.
	Mapped Kind = iota
	// Ignored means it was accepted with no local effect.
	Ignored
	// Rejected means doze-aws refused it.
	Rejected
)

func (k Kind) String() string {
	switch k {
	case Mapped:
		return "mapped"
	case Ignored:
		return "ignored"
	default:
		return "rejected"
	}
}

// Entry is one line of the transpile report.
type Entry struct {
	LogicalID string
	Type      string
	Kind      Kind
	// Name is the stack-file name a mapped resource took.
	Name string
	// Reason explains an ignored or rejected resource.
	Reason string
}

// nameProperty names the property that carries an explicit physical name for
// each mapped type. When a template sets it, that name is used; otherwise the
// logical ID is.
var nameProperty = map[string]string{
	"AWS::SQS::Queue":                 "QueueName",
	"AWS::SNS::Topic":                 "TopicName",
	"AWS::S3::Bucket":                 "BucketName",
	"AWS::DynamoDB::Table":            "TableName",
	"AWS::DynamoDB::GlobalTable":      "TableName",
	"AWS::Lambda::Function":           "FunctionName",
	"AWS::Serverless::Function":       "FunctionName",
	"AWS::Events::Rule":               "Name",
	"AWS::Events::EventBus":           "Name",
	"AWS::KMS::Key":                   "", // keys are addressed by alias
	"AWS::KMS::Alias":                 "AliasName",
	"AWS::SecretsManager::Secret":     "Name",
	"AWS::SSM::Parameter":             "Name",
	"AWS::Kinesis::Stream":            "Name",
	"AWS::Serverless::SimpleTable":    "TableName",
	"AWS::SNS::Subscription":          "",
	"AWS::Lambda::EventSourceMapping": "",
	"AWS::Lambda::Permission":         "",
	"AWS::S3::BucketPolicy":           "",
	"AWS::SQS::QueuePolicy":           "",
	"AWS::SNS::TopicPolicy":           "",
	"AWS::Lambda::LayerVersion":       "LayerName",
	"AWS::Lambda::Alias":              "Name",
	"AWS::Lambda::Version":            "",
	"AWS::Lambda::Url":                "",
	"AWS::Serverless::Api":            "Name",
	"AWS::Serverless::HttpApi":        "Name",
	"AWS::ApiGateway::RestApi":        "Name",
	"AWS::ApiGatewayV2::Api":          "Name",
	"AWS::ApiGateway::Deployment":     "",
	"AWS::ApiGateway::Stage":          "StageName",
	"AWS::ApiGateway::Resource":       "",
	"AWS::ApiGateway::Method":         "",
	"AWS::ApiGateway::Account":        "",
}

// IsMappable reports whether doze-aws models a resource type.
func IsMappable(typ string) bool {
	_, ok := nameProperty[typ]
	return ok
}

// physicalName decides the name a resource takes in the stack file. An
// explicit name property wins; otherwise the logical ID is used verbatim.
//
// CloudFormation would generate something like `stack-Logical-1A2B3C`. Locally
// that is actively unhelpful — you want to `aws sqs receive-message --queue-url
// .../MyQueue`, not chase a random suffix — so the logical ID is the name.
func physicalName(r *Resource, props map[string]any) string {
	if prop, ok := nameProperty[r.Type]; ok && prop != "" {
		if v, ok := props[prop]; ok {
			if s := fmt.Sprint(v); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return r.LogicalID
}

// refValue is what `!Ref` on a resource of this type yields, following AWS's
// per-type rules — Ref on a queue is its URL, on a topic its ARN, on a bucket
// its name.
func refValue(typ, name string) string {
	switch typ {
	case "AWS::SQS::Queue":
		return queueURL(name)
	case "AWS::SNS::Topic":
		return awsident.ARN("sns", name)
	case "AWS::KMS::Key":
		return name
	case "AWS::Lambda::LayerVersion":
		return awsident.ARN("lambda", "layer:"+name+":1")
	case "AWS::SecretsManager::Secret":
		return awsident.ARN("secretsmanager", "secret:"+name)
	case "AWS::Kinesis::Stream":
		return name
	}
	// Buckets, tables, functions, rules and parameters all Ref to their name.
	return name
}

// attributes are the Fn::GetAtt values doze-aws can answer for a resource.
// Anything absent here produces an explicit error naming what IS available,
// rather than an empty string that silently corrupts a property.
func attributes(typ, name string) map[string]string {
	switch typ {
	case "AWS::SQS::Queue":
		return map[string]string{
			"Arn":       awsident.ARN("sqs", name),
			"QueueName": name,
			"QueueUrl":  queueURL(name),
		}
	case "AWS::SNS::Topic":
		return map[string]string{
			"TopicArn":  awsident.ARN("sns", name),
			"TopicName": name,
			"Arn":       awsident.ARN("sns", name),
		}
	case "AWS::S3::Bucket":
		return map[string]string{
			"Arn":                s3ARN(name),
			"DomainName":         name + ".s3.localhost",
			"RegionalDomainName": name + ".s3." + awsident.Region + ".localhost",
			"WebsiteURL":         "http://" + name + ".s3-website.localhost",
		}
	case "AWS::DynamoDB::Table", "AWS::DynamoDB::GlobalTable", "AWS::Serverless::SimpleTable":
		return map[string]string{
			"Arn":       awsident.ARN("dynamodb", "table/"+name),
			"StreamArn": awsident.ARN("dynamodb", "table/"+name+"/stream/local"),
		}
	case "AWS::Lambda::Function", "AWS::Serverless::Function":
		return map[string]string{
			"Arn": awsident.ARN("lambda", "function:"+name),
		}
	case "AWS::Events::Rule":
		return map[string]string{"Arn": awsident.ARN("events", "rule/"+name)}
	case "AWS::ApiGateway::RestApi", "AWS::Serverless::Api", "AWS::ApiGatewayV2::Api":
		return map[string]string{
			"RootResourceId": "root",
			"Arn":            awsident.ARN("apigateway", "/restapis/"+name),
			"ApiId":          name,
		}
	case "AWS::Events::EventBus":
		return map[string]string{
			"Arn":  awsident.ARN("events", "event-bus/"+name),
			"Name": name,
		}
	case "AWS::KMS::Key":
		return map[string]string{
			"Arn":   awsident.ARN("kms", "key/"+name),
			"KeyId": name,
		}
	case "AWS::SecretsManager::Secret":
		return map[string]string{"Id": awsident.ARN("secretsmanager", "secret:"+name)}
	case "AWS::SSM::Parameter":
		return map[string]string{"Type": "String", "Value": name}
	case "AWS::Kinesis::Stream":
		return map[string]string{
			"Arn":  awsident.ARN("kinesis", "stream/"+name),
			"Name": name,
		}
	case "AWS::Lambda::LayerVersion":
		return map[string]string{"LayerVersionArn": awsident.ARN("lambda", "layer:"+name+":1")}
	}
	return map[string]string{}
}

// s3ARN builds a bucket ARN, which has no region or account segment.
func s3ARN(bucket string) string { return "arn:aws:s3:::" + bucket }

// queueURL matches the URL shape the SQS service hands out.
func queueURL(name string) string {
	return "http://127.0.0.1/" + awsident.AccountID + "/" + name
}

// classify decides what will happen to a resource type before any mapping is
// attempted, so the report can be produced even when a template is rejected.
func classify(typ string) (Kind, string) {
	if IsMappable(typ) {
		return Mapped, ""
	}
	if reason, ok := ignoredTypes[typ]; ok {
		return Ignored, reason
	}
	// Serverless types get a reason naming the missing service specifically.
	if reason, ok := samReason(typ); ok {
		return Rejected, reason
	}
	// A whole-service refusal is more useful than "unknown type".
	if service, ok := serviceOf(typ); ok {
		return Rejected, "doze-aws does not serve " + service
	}
	return Rejected, "unsupported resource type"
}

// serviceOf extracts the service name from an AWS::Service::Type identifier.
func serviceOf(typ string) (string, bool) {
	parts := strings.Split(typ, "::")
	if len(parts) < 2 {
		return "", false
	}
	return parts[1], true
}

// ---- identities for ignored resources ----
//
// An ignored resource has no local behaviour, but it still has an IDENTITY,
// and templates lean on that constantly: `Role: !GetAtt ExecutionRole.Arn` is
// in almost every function, and CDK's bootstrap outputs `!Ref
// ContainerAssetsRepository`. Dropping an ignored resource from the reference
// scope would make those templates fail on a resource nobody needed.
//
// So ignored resources get a synthesized name and a plausible ARN. Nothing
// consumes them — they exist so a reference resolves rather than exploding.

// ghostName is the physical name an ignored resource takes: an explicit name
// property when the template gives one, else the logical ID.
func ghostName(r *Resource, props map[string]any) string {
	for _, field := range []string{"RoleName", "GroupName", "UserName", "PolicyName",
		"LogGroupName", "RepositoryName", "AlarmName", "ManagedPolicyName", "InstanceProfileName"} {
		if v, ok := props[field]; ok {
			if s := fmt.Sprint(v); s != "" && s != "<nil>" && !strings.Contains(s, "map[") {
				return s
			}
		}
	}
	return r.LogicalID
}

// ghostIdentity returns the Ref value and attributes for an ignored resource.
func ghostIdentity(typ, name string) (string, map[string]string) {
	service, kind := serviceAndKind(typ)
	arn := ghostARN(service, kind, name)
	atts := map[string]string{"Arn": arn, "Name": name}

	switch typ {
	case "AWS::IAM::Role":
		atts["RoleId"] = "AROA" + strings.ToUpper(name)
		atts["RoleName"] = name
	case "AWS::IAM::User":
		atts["UserName"] = name
	case "AWS::IAM::Group":
		atts["GroupName"] = name
	case "AWS::IAM::InstanceProfile":
		// Ref on an instance profile is its name, but GetAtt Arn is common.
	case "AWS::Logs::LogGroup":
		atts["LogGroupName"] = name
	case "AWS::ECR::Repository":
		atts["RepositoryName"] = name
		atts["RepositoryUri"] = awsident.AccountID + ".dkr.ecr." + awsident.Region + ".localhost/" + name
	}

	// Ref semantics differ by type: an IAM role Refs to its name, a managed
	// policy to its ARN.
	ref := name
	if typ == "AWS::IAM::ManagedPolicy" || typ == "AWS::IAM::Policy" {
		ref = arn
	}
	return ref, atts
}

// serviceAndKind splits AWS::Service::Kind.
func serviceAndKind(typ string) (string, string) {
	parts := strings.Split(typ, "::")
	if len(parts) < 3 {
		return "", ""
	}
	return strings.ToLower(parts[1]), strings.ToLower(parts[2])
}

// ghostARN builds a plausible ARN for a resource doze-aws does not model. IAM
// is global, so it has no region segment.
func ghostARN(service, kind, name string) string {
	if service == "iam" {
		return awsident.GlobalARN("iam", kind+"/"+name)
	}
	if service == "" {
		return name
	}
	return awsident.ARN(service, kind+"/"+name)
}

// derivedName sanitises a name taken from a logical ID so it satisfies the
// naming rules of the service it belongs to.
//
// This only applies to DERIVED names. An explicit name in the template is
// passed through untouched, so a template that would be rejected by real
// CloudFormation is rejected here too rather than being quietly rewritten.
//
// S3 is the strict case: bucket names must be lowercase, and a logical ID like
// `ServerlessDeploymentBucket` is not one.
func derivedName(typ, logicalID string) string {
	switch typ {
	case "AWS::S3::Bucket":
		return s3SafeName(logicalID)
	}
	return logicalID
}

// s3SafeName lowercases and filters a name into the S3 bucket charset.
func s3SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	for len(out) < 3 {
		out += "0"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-.")
	}
	return out
}
