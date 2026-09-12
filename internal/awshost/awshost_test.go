package awshost

import "testing"

// The table is every shape doze-aws hands out or accepts, with the real AWS
// form beside it where one exists — because the whole point is that they
// differ by the suffix alone.
func TestParse(t *testing.T) {
	const suffix = "aws.harbour.doze"

	for _, tc := range []struct {
		what string
		host string
		want Info
	}{
		{"a plain service endpoint",
			"sqs.ap-south-1." + suffix,
			Info{Service: "sqs", Region: "ap-south-1"}},
		{"EventBridge signs as events",
			"events.eu-west-1." + suffix,
			Info{Service: "eventbridge", Region: "eu-west-1"}},
		{"CloudWatch's metrics endpoint is called monitoring",
			"monitoring.us-east-1." + suffix,
			Info{Service: "cloudwatch", Region: "us-east-1"}},
		{"Step Functions signs as states",
			"states.us-east-1." + suffix,
			Info{Service: "stepfunctions", Region: "us-east-1"}},

		{"virtual-hosted S3",
			"harbour-receipts.s3.ap-south-1." + suffix,
			Info{Service: "s3", Region: "ap-south-1", Bucket: "harbour-receipts"}},
		{"a bucket name with dots in it",
			"my.dotted.bucket.s3.us-east-1." + suffix,
			Info{Service: "s3", Region: "us-east-1", Bucket: "my.dotted.bucket"}},
		{"an API Gateway invoke host",
			"x70an6eshc.execute-api.ap-south-1." + suffix,
			Info{Service: "apigateway", Region: "ap-south-1", APIID: "x70an6eshc"}},
		{"a Lambda function URL",
			"abc123.lambda-url.eu-west-1." + suffix,
			Info{Service: "lambda", Region: "eu-west-1", FunctionURLID: "abc123"}},

		{"a port is not part of the name",
			"sqs.ap-south-1." + suffix + ":4566",
			Info{Service: "sqs", Region: "ap-south-1"}},
		{"case is not significant",
			"SQS.AP-SOUTH-1." + suffix,
			Info{Service: "sqs", Region: "ap-south-1"}},
		{"a trailing dot is legal in a hostname",
			"sqs.ap-south-1." + suffix + ".",
			Info{Service: "sqs", Region: "ap-south-1"}},

		// The conventional shapes still parse with no suffix configured, which
		// is how they worked before any of this and how AWS's own hosts read.
		{"AWS's own S3 host",
			"harbour-receipts.s3.ap-south-1.amazonaws.com",
			Info{Service: "s3", Region: "ap-south-1", Bucket: "harbour-receipts"}},
		{"AWS's own execute-api host",
			"x70an6eshc.execute-api.ap-south-1.amazonaws.com",
			Info{Service: "apigateway", Region: "ap-south-1", APIID: "x70an6eshc"}},

		// A host that names nothing is not an error: path-style addressing is
		// how every request arrives when no DNS is set up.
		{"a bare loopback address", "127.0.0.1:4566", Info{}},
		{"a bare name", "localhost", Info{}},
		{"the instance suffix alone", suffix, Info{}},
		{"an unrelated host", "example.com", Info{}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if got := Parse(tc.host, suffix); got != tc.want {
				t.Errorf("Parse(%q)\n got %+v\nwant %+v", tc.host, got, tc.want)
			}
		})
	}
}

// A region label is recognised by SHAPE, not from a list — AWS adds regions,
// and doze-aws creates whichever one a request names, so a list would reject a
// real region the day it shipped.
func TestRegionIsRecognisedByShape(t *testing.T) {
	for _, r := range []string{
		"us-east-1", "ap-southeast-3", "eu-central-2", "me-south-1",
		"ap-south-1", "il-central-1",
		"xx-nowhere-9", // not a real region, and that is the point
	} {
		if !looksLikeRegion(r) {
			t.Errorf("looksLikeRegion(%q) = false", r)
		}
	}
	for _, r := range []string{
		"", "s3", "execute-api", "amazonaws", "us-east", "us-east-x",
		"us--1", "US-EAST-1", "harbour", "1-2-3",
	} {
		if looksLikeRegion(r) {
			t.Errorf("looksLikeRegion(%q) = true", r)
		}
	}
}

// A bucket called "sqs" must not be read as the SQS service, and a hostname
// whose FIRST label is an infix must not be mistaken for the resource form —
// s3.us-east-1.<suffix> is the service, not a bucket named "".
func TestAmbiguousShapes(t *testing.T) {
	const suffix = "aws.harbour.doze"
	for _, tc := range []struct {
		host string
		want Info
	}{
		{"s3.us-east-1." + suffix, Info{Service: "s3", Region: "us-east-1"}},
		{"sqs.s3.us-east-1." + suffix, Info{Service: "s3", Region: "us-east-1", Bucket: "sqs"}},
		// An infix label in FIRST position names no resource, so it is not the
		// resource form. "lambda-url" is the case that proves it: unlike "s3"
		// and "execute-api" it is not also a service name, so without the
		// leading-label guard this would report the lambda service for a
		// hostname that names nothing.
		//
		// Worth stating why this case exists: the first sabotage of that guard
		// PASSED, because every other shape yields an empty resource name,
		// which is indistinguishable from having none.
		{"lambda-url.us-east-1." + suffix, Info{}},
	} {
		if got := Parse(tc.host, suffix); got != tc.want {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", tc.host, got, tc.want)
		}
	}
}
