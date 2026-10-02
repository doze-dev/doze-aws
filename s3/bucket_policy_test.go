package s3

// S3 checks two things before it stores a bucket policy: it is a policy, and
// it is about this bucket. Found by the boto3 conformance suite
// (conformance/tests/test_s3.py) — whatever was sent used to be stored.

import "testing"

func TestBucketPolicyIsAPolicyAboutThisBucket(t *testing.T) {
	stmt := func(resource string) string {
		return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"s3:GetObject","Resource":` + resource + `}]}`
	}
	for _, doc := range []string{
		stmt(`"arn:aws:s3:::mine/*"`),
		stmt(`"arn:aws:s3:::mine"`),
		stmt(`["arn:aws:s3:::mine","arn:aws:s3:::mine/logs/*"]`),
	} {
		if aerr := validBucketPolicy("mine", doc); aerr != nil {
			t.Errorf("refused %s: %v", doc, aerr)
		}
	}
	for what, doc := range map[string]string{
		"not json":          `{nope`,
		"no statements":     `{"Version":"2012-10-17","Statement":[]}`,
		"another bucket":    stmt(`"arn:aws:s3:::theirs/*"`),
		"a longer name":     stmt(`"arn:aws:s3:::mine-too/*"`),
		"everything":        stmt(`"*"`),
		"one of two is not": stmt(`["arn:aws:s3:::mine/*","arn:aws:s3:::theirs/*"]`),
	} {
		if aerr := validBucketPolicy("mine", doc); aerr == nil || aerr.Code != "MalformedPolicy" {
			t.Errorf("%s: got %v, want MalformedPolicy", what, aerr)
		}
	}
}
