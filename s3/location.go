package s3

// nullLocationRegion is the one Region GetBucketLocation does not name. The
// model's words for LocationConstraint: "Buckets in Region us-east-1 have a
// LocationConstraint of null." It is a fact about the API and not this
// instance's region, which is why it is written here and nowhere else (see
// notIdentity in identityliterals_test.go).
const nullLocationRegion = "us-east-1"

// locationConstraint is what GetBucketLocation answers for a bucket in region.
// A bucket lives in the region of the instance that holds it, the same value
// HeadBucket reports as x-amz-bucket-region.
func locationConstraint(region string) string {
	if region == nullLocationRegion {
		return ""
	}
	return region
}
