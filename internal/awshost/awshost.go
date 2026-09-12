// Package awshost reads what a request's Host header says about it: which
// service, which region, and which resource the hostname names.
//
// # Why this exists
//
// AWS gives every service its own hostname, and puts the region in it:
//
//	sqs.ap-south-1.amazonaws.com
//	harbour-receipts.s3.ap-south-1.amazonaws.com
//	x70an6eshc.execute-api.ap-south-1.amazonaws.com
//	abc123.lambda-url.ap-south-1.on.aws
//
// doze-aws serves the same shapes with its own suffix in place of AWS's, so a
// URL it hands back differs from the real one by that suffix alone.
//
// Before this package there were FIVE hand-rolled Host parsers — in s3,
// apigateway, lambda, the gateway (twice) and the console — none sharing code,
// each with its own idea of how to strip a port and where to cut. That is the
// shape of drift this tree has been bitten by repeatedly, and none of them knew
// about regions at all.
//
// # What it does not do
//
// It does not decide anything. It reports what the hostname claims; the caller
// decides what to do about it. In particular a Host that names no service is
// not an error — path-style addressing is still how most requests arrive, and
// is how every request arrives when no DNS is set up.
package awshost

import "strings"

// Info is what a hostname claims about a request. Every field is optional: a
// plain 127.0.0.1 yields the zero value, which is correct rather than a
// failure.
type Info struct {
	// Service is the AWS service the hostname names, in doze-aws's own
	// spelling ("eventbridge", not "events"). Empty when the host names none.
	Service string
	// Region is the region label, empty when the host carries none.
	Region string
	// Bucket is set for virtual-hosted-style S3: <bucket>.s3.<region>.<suffix>.
	Bucket string
	// APIID is set for API Gateway: <id>.execute-api.<region>.<suffix>.
	APIID string
	// FunctionURLID is set for a Lambda function URL:
	// <id>.lambda-url.<region>.<suffix>.
	FunctionURLID string
}

// Named reports whether the hostname named a service at all.
func (i Info) Named() bool { return i.Service != "" }

// infix names the service a middle label belongs to. These are AWS's own
// spellings, which differ from doze-aws's service names in one place —
// EventBridge signs and addresses as "events".
var infix = map[string]string{
	"execute-api": "apigateway",
	"lambda-url":  "lambda",
	"s3":          "s3",
}

// service maps a leading label to a doze-aws service name, for the plain
// <service>.<region>.<suffix> shape.
var service = map[string]string{
	"sqs": "sqs", "sns": "sns", "s3": "s3", "dynamodb": "dynamodb",
	"lambda": "lambda", "kinesis": "kinesis", "kms": "kms", "ssm": "ssm",
	"secretsmanager": "secretsmanager", "iam": "iam", "sts": "sts",
	"cloudformation": "cloudformation", "apigateway": "apigateway",
	"logs": "logs", "monitoring": "cloudwatch", "cloudwatch": "cloudwatch",
	"states": "stepfunctions", "stepfunctions": "stepfunctions",
	"events": "eventbridge", "eventbridge": "eventbridge",
	"execute-api": "apigateway",
}

// Parse reads a Host header against the instance's suffix.
//
// suffix is what stands in for "amazonaws.com" — "aws.harbour.doze", say.
// An empty suffix still parses the conventional shapes, because the infix
// labels (.s3., .execute-api., .lambda-url.) identify a request on their own
// and did so before any of this was configurable.
func Parse(host, suffix string) Info {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	// A Host header may carry a port, and an IPv6 literal carries colons of
	// its own — so cut at the LAST colon only when it follows a ']' or there
	// is exactly one.
	if h, ok := stripPort(host); ok {
		host = h
	}
	if suffix != "" {
		host = strings.TrimSuffix(host, "."+strings.ToLower(strings.TrimSuffix(suffix, ".")))
	}
	labels := strings.Split(host, ".")

	// <something>.<infix>.<region>...  — the three shapes with a middle label.
	for i, l := range labels {
		svc, ok := infix[l]
		if !ok || i == 0 {
			continue
		}
		info := Info{Service: svc}
		if i+1 < len(labels) && looksLikeRegion(labels[i+1]) {
			info.Region = labels[i+1]
		}
		head := strings.Join(labels[:i], ".")
		switch svc {
		case "s3":
			info.Bucket = head
		case "apigateway":
			info.APIID = head
		case "lambda":
			info.FunctionURLID = head
		}
		return info
	}

	// <service>.<region>...
	if svc, ok := service[labels[0]]; ok {
		info := Info{Service: svc}
		if len(labels) > 1 && looksLikeRegion(labels[1]) {
			info.Region = labels[1]
		}
		return info
	}
	return Info{}
}

// stripPort removes a trailing :port. It reports false when there is nothing
// to strip, so a bare IPv6 literal is left alone rather than truncated.
func stripPort(host string) (string, bool) {
	if i := strings.LastIndex(host, "]"); i >= 0 {
		if j := strings.Index(host[i:], ":"); j >= 0 {
			return host[:i+j], true
		}
		return host, false
	}
	if strings.Count(host, ":") == 1 {
		i := strings.Index(host, ":")
		return host[:i], true
	}
	return host, false
}

// looksLikeRegion reports whether a label has the shape of an AWS region code:
// <area>-<direction>-<number>, as in us-east-1 or ap-southeast-3.
//
// Shape rather than a list, deliberately. AWS adds regions, and doze-aws
// creates whichever one a request names — so a fixed list would reject a real
// region the day it shipped, which is precisely the failure this emulator
// exists to avoid.
func looksLikeRegion(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return false
	}
	last := parts[len(parts)-1]
	if last == "" {
		return false
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}
