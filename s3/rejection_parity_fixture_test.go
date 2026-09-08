package s3

// The state the S3 baselines address, and the preconditions each mutating
// operation needs.

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const (
	bkt        = "audit-bucket"
	objKey     = "audit/object.txt"
	policyDoc  = `{"Version":"2012-10-17","Statement":[{"Sid":"a","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::audit-bucket/*"}]}`
	lockBucket = "audit-locked"
)

// fx is the state the baselines address.
type fx struct {
	uploadID string
	lockKey  string
}

func mk(t *testing.T, ts *httptest.Server, op string, body map[string]any) string {
	t.Helper()
	code, resp := call(t, ts, bindingFor(t, op), body)
	if code < 200 || code > 299 {
		t.Fatalf("fixture %s = %d: %s", op, code, resp)
	}
	return resp
}

// bindingFor reads an operation's binding out of the committed manifest rather
// than restating it — the harness and the service must agree with the model,
// not with each other.
func bindingFor(t *testing.T, op string) *binding {
	t.Helper()
	b, ok := loadRoutes(t)[op]
	if !ok {
		t.Fatalf("no binding for %s", op)
	}
	return b
}

func xmlField(t *testing.T, resp, tag string) string {
	t.Helper()
	m := regexp.MustCompile(`<` + tag + `>([^<]+)</` + tag + `>`).FindStringSubmatch(resp)
	if m == nil {
		t.Fatalf("response has no <%s>: %s", tag, resp)
	}
	return m[1]
}

// listPartETag reads back the ETag S3 gave the uploaded part. Inventing one
// would make CompleteMultipartUpload fail for the ETag rather than for the
// mutation the case is about.
func listPartETag(t *testing.T, ts *httptest.Server, bucket, key, uploadID string) string {
	t.Helper()
	code, resp := call(t, ts, bindingFor(t, "ListParts"), map[string]any{
		"Bucket": bucket, "Key": key, "UploadId": uploadID})
	if code < 200 || code > 299 {
		t.Fatalf("ListParts = %d: %s", code, resp)
	}
	// The ETag comes back XML-escaped and quoted; S3 wants it back as it was
	// given, quotes and all.
	etag := strings.ReplaceAll(xmlField(t, resp, "ETag"), "&#34;", `"`)
	return strings.ReplaceAll(etag, "&quot;", `"`)
}

func setUpFixture(t *testing.T, ts *httptest.Server) fx {
	t.Helper()
	f := fx{lockKey: "locked/object.txt"}
	mk(t, ts, "CreateBucket", map[string]any{"Bucket": bkt})
	mk(t, ts, "PutObject", map[string]any{"Bucket": bkt, "Key": objKey})
	mk(t, ts, "PutBucketTagging", map[string]any{"Bucket": bkt,
		"Tagging": map[string]any{"TagSet": []any{map[string]any{"Key": "env", "Value": "dev"}}}})
	mk(t, ts, "PutObjectTagging", map[string]any{"Bucket": bkt, "Key": objKey,
		"Tagging": map[string]any{"TagSet": []any{map[string]any{"Key": "env", "Value": "dev"}}}})

	// Object Lock needs its own bucket: it can only be enabled at creation, and
	// enabling it on the shared fixture would change what every other object
	// baseline is allowed to do.
	mk(t, ts, "CreateBucket", map[string]any{"Bucket": lockBucket,
		"ObjectLockEnabledForBucket": "true"})
	mk(t, ts, "PutObject", map[string]any{"Bucket": lockBucket, "Key": f.lockKey})

	// Every Get* sub-resource baseline needs its sub-resource to exist, and
	// operations run alphabetically — Get sorts before Put, so nothing else
	// will have configured them.
	mk(t, ts, "PutBucketCors", map[string]any{"Bucket": bkt,
		"CORSConfiguration": map[string]any{"CORSRules": []any{map[string]any{
			"AllowedMethods": []any{"GET"}, "AllowedOrigins": []any{"*"}}}}})
	mk(t, ts, "PutBucketLifecycleConfiguration", map[string]any{"Bucket": bkt,
		"LifecycleConfiguration": map[string]any{"Rules": []any{map[string]any{
			"ID": "audit", "Status": "Enabled",
			"Filter":     map[string]any{"Prefix": "audit/"},
			"Expiration": map[string]any{"Days": 30}}}}})
	// The fixture policy grants to everyone, and a new bucket blocks public
	// policies — as on AWS, the block has to come off before the put.
	mk(t, ts, "PutPublicAccessBlock", map[string]any{"Bucket": bkt,
		"PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls": true, "IgnorePublicAcls": true,
			"BlockPublicPolicy": false, "RestrictPublicBuckets": false}})
	mk(t, ts, "PutBucketPolicy", map[string]any{"Bucket": bkt, "Policy": policyDoc})
	mk(t, ts, "PutBucketOwnershipControls", map[string]any{"Bucket": bkt,
		"OwnershipControls": map[string]any{"Rules": []any{map[string]any{"ObjectOwnership": "BucketOwnerEnforced"}}}})
	mk(t, ts, "PutBucketEncryption", map[string]any{"Bucket": bkt,
		"ServerSideEncryptionConfiguration": map[string]any{"Rules": []any{map[string]any{
			"ApplyServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"}}}}})
	mk(t, ts, "PutBucketWebsite", map[string]any{"Bucket": bkt,
		"WebsiteConfiguration": map[string]any{
			"IndexDocument": map[string]any{"Suffix": "index.html"}}})
	mk(t, ts, "PutObjectRetention", map[string]any{"Bucket": lockBucket, "Key": f.lockKey,
		"Retention": map[string]any{"Mode": "GOVERNANCE", "RetainUntilDate": "2030-01-01T00:00:00Z"}})

	resp := mk(t, ts, "CreateMultipartUpload", map[string]any{"Bucket": bkt, "Key": "multi/part.bin"})
	f.uploadID = xmlField(t, resp, "UploadId")
	return f
}

func baselines(f fx) map[string]map[string]any {
	b := map[string]any{"Bucket": bkt}
	obj := map[string]any{"Bucket": bkt, "Key": objKey}
	lock := map[string]any{"Bucket": lockBucket, "Key": f.lockKey}
	tagging := map[string]any{"TagSet": []any{map[string]any{"Key": "env", "Value": "dev"}}}
	// No Grants: an empty list is a container auditkit cannot reach into, so
	// the exemplar supplies one when a case needs it.
	acp := map[string]any{"Owner": map[string]any{"ID": "000000000000"}}

	return map[string]map[string]any{
		// Buckets.
		"CreateBucket":         {"Bucket": "made-by-baseline"},
		"DeleteBucket":         {"Bucket": "made-by-baseline"},
		"HeadBucket":           b,
		"ListBuckets":          {},
		"ListDirectoryBuckets": {},

		// Objects.
		"PutObject":    {"Bucket": bkt, "Key": "made-by-baseline"},
		"GetObject":    obj,
		"HeadObject":   obj,
		"DeleteObject": {"Bucket": bkt, "Key": "made-by-baseline"},
		"CopyObject": {"Bucket": bkt, "Key": "made-by-baseline",
			"CopySource": bkt + "/" + objKey},
		"DeleteObjects": {"Bucket": bkt, "Delete": map[string]any{
			"Objects": []any{map[string]any{"Key": "made-by-baseline"}}}},
		"ListObjects":        b,
		"ListObjectsV2":      b,
		"ListObjectVersions": b,

		// Multipart.
		"CreateMultipartUpload": {"Bucket": bkt, "Key": "multi/made-by-baseline"},
		"UploadPart": {"Bucket": bkt, "Key": "multi/part.bin",
			"UploadId": f.uploadID, "PartNumber": 1},
		"UploadPartCopy": {"Bucket": bkt, "Key": "multi/part.bin",
			"UploadId": f.uploadID, "PartNumber": 2, "CopySource": bkt + "/" + objKey},
		"ListParts":            {"Bucket": bkt, "Key": "multi/part.bin", "UploadId": f.uploadID},
		"ListMultipartUploads": b,
		"AbortMultipartUpload": {"Bucket": bkt, "Key": "multi/part.bin",
			"UploadId": "made-by-baseline"},
		"CompleteMultipartUpload": {"Bucket": bkt, "Key": "multi/part.bin",
			"UploadId": "made-by-baseline",
			"MultipartUpload": map[string]any{"Parts": []any{
				map[string]any{"PartNumber": 1, "ETag": "made-by-baseline"}}}},

		// Bucket sub-resources: tagging, cors, lifecycle, policy, and friends.
		"PutBucketTagging":    {"Bucket": bkt, "Tagging": tagging},
		"GetBucketTagging":    b,
		"DeleteBucketTagging": {"Bucket": "made-by-baseline"},
		"PutBucketCors": {"Bucket": bkt, "CORSConfiguration": map[string]any{
			"CORSRules": []any{map[string]any{
				"AllowedMethods": []any{"GET"}, "AllowedOrigins": []any{"*"}}}}},
		"GetBucketCors":    b,
		"DeleteBucketCors": {"Bucket": "made-by-baseline"},
		"PutBucketLifecycleConfiguration": {"Bucket": bkt,
			"LifecycleConfiguration": map[string]any{"Rules": []any{map[string]any{
				"ID": "audit", "Status": "Enabled",
				"Filter":     map[string]any{"Prefix": "audit/"},
				"Expiration": map[string]any{"Days": 30}}}}},
		"GetBucketLifecycleConfiguration": b,
		"DeleteBucketLifecycle":           {"Bucket": "made-by-baseline"},
		"PutBucketPolicy":                 {"Bucket": bkt, "Policy": policyDoc},
		"GetBucketPolicy":                 b,
		"DeleteBucketPolicy":              {"Bucket": "made-by-baseline"},
		"GetBucketPolicyStatus":           b,
		"PutPublicAccessBlock": {"Bucket": bkt, "PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls": true, "IgnorePublicAcls": true, "BlockPublicPolicy": false, "RestrictPublicBuckets": false}},
		"GetPublicAccessBlock":    b,
		"DeletePublicAccessBlock": {"Bucket": "made-by-baseline"},
		"PutBucketOwnershipControls": {"Bucket": bkt, "OwnershipControls": map[string]any{
			"Rules": []any{map[string]any{"ObjectOwnership": "BucketOwnerEnforced"}}}},
		"GetBucketOwnershipControls":    b,
		"DeleteBucketOwnershipControls": {"Bucket": "made-by-baseline"},
		"PutBucketEncryption": {"Bucket": bkt, "ServerSideEncryptionConfiguration": map[string]any{
			"Rules": []any{map[string]any{"ApplyServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"}}}}},
		"GetBucketEncryption":    b,
		"DeleteBucketEncryption": {"Bucket": "made-by-baseline"},
		"PutBucketVersioning": {"Bucket": bkt,
			"VersioningConfiguration": map[string]any{"Status": "Enabled"}},
		"GetBucketVersioning": b,
		"PutBucketWebsite": {"Bucket": bkt, "WebsiteConfiguration": map[string]any{
			"IndexDocument": map[string]any{"Suffix": "index.html"}}},
		"GetBucketWebsite":    b,
		"DeleteBucketWebsite": {"Bucket": "made-by-baseline"},
		"PutBucketAccelerateConfiguration": {"Bucket": bkt,
			"AccelerateConfiguration": map[string]any{"Status": "Suspended"}},
		"GetBucketAccelerateConfiguration": b,
		"PutBucketRequestPayment": {"Bucket": bkt,
			"RequestPaymentConfiguration": map[string]any{"Payer": "BucketOwner"}},
		"GetBucketRequestPayment": b,
		"PutBucketLogging": {"Bucket": bkt,
			"BucketLoggingStatus": map[string]any{}},
		"GetBucketLogging": b,
		"PutBucketReplication": {"Bucket": bkt,
			"ReplicationConfiguration": map[string]any{
				"Role": "arn:aws:iam::000000000000:role/audit",
				"Rules": []any{map[string]any{
					"Status":      "Enabled",
					"Destination": map[string]any{"Bucket": "arn:aws:s3:::audit-dest"}}}}},
		"GetBucketReplication":    b,
		"DeleteBucketReplication": {"Bucket": "made-by-baseline"},
		"PutBucketAcl":            {"Bucket": bkt, "AccessControlPolicy": acp},
		"GetBucketAcl":            b,
		"GetBucketLocation":       b,
		"PutBucketNotificationConfiguration": {"Bucket": bkt,
			"NotificationConfiguration": map[string]any{}},
		"GetBucketNotificationConfiguration": b,

		// Object sub-resources.
		"PutObjectTagging":    {"Bucket": bkt, "Key": objKey, "Tagging": tagging},
		"GetObjectTagging":    obj,
		"DeleteObjectTagging": {"Bucket": bkt, "Key": "made-by-baseline"},
		"PutObjectAcl":        {"Bucket": bkt, "Key": objKey, "AccessControlPolicy": acp},
		"GetObjectAcl":        obj,
		"GetObjectAttributes": {"Bucket": bkt, "Key": objKey,
			"ObjectAttributes": []any{"ObjectSize"}},

		// Object Lock, on its own bucket.
		"PutObjectLockConfiguration": {"Bucket": lockBucket,
			"ObjectLockConfiguration": map[string]any{"ObjectLockEnabled": "Enabled"}},
		"GetObjectLockConfiguration": {"Bucket": lockBucket},
		"PutObjectLegalHold": {"Bucket": lockBucket, "Key": f.lockKey,
			"LegalHold": map[string]any{"Status": "OFF"}},
		"GetObjectLegalHold": lock,
		"PutObjectRetention": {"Bucket": lockBucket, "Key": f.lockKey,
			"Retention": map[string]any{"Mode": "GOVERNANCE", "RetainUntilDate": "2030-01-01T00:00:00Z"}},
		"GetObjectRetention": lock,
	}
}

// exemplars stand in for containers a baseline does not carry.
func exemplars() map[string]any {
	return map[string]any{
		"Tagging.TagSet":                                             []any{map[string]any{"Key": "env", "Value": "dev"}},
		"Delete.Objects":                                             []any{map[string]any{"Key": "audit/object.txt"}},
		"ObjectAttributes":                                           []any{"ObjectSize"},
		"OptionalObjectAttributes":                                   []any{"RestoreStatus"},
		"AccessControlPolicy":                                        map[string]any{"Owner": map[string]any{"ID": "000000000000"}, "Grants": []any{}},
		"AccessControlPolicy.Grants":                                 []any{map[string]any{"Permission": "READ", "Grantee": map[string]any{"Type": "CanonicalUser", "ID": "000000000000"}}},
		"CreateBucketConfiguration":                                  map[string]any{"LocationConstraint": "us-west-2"},
		"CORSConfiguration.CORSRules":                                []any{map[string]any{"AllowedMethods": []any{"GET"}, "AllowedOrigins": []any{"*"}}},
		"LifecycleConfiguration.Rules":                               []any{map[string]any{"ID": "audit", "Status": "Enabled", "Filter": map[string]any{"Prefix": "audit/"}, "Expiration": map[string]any{"Days": 30}}},
		"BucketLoggingStatus.LoggingEnabled":                         map[string]any{"TargetBucket": "audit-logs", "TargetPrefix": "log/"},
		"NotificationConfiguration.QueueConfigurations":              []any{map[string]any{"QueueArn": "arn:aws:sqs:us-east-1:000000000000:audit-q", "Events": []any{"s3:ObjectCreated:*"}}},
		"NotificationConfiguration.TopicConfigurations":              []any{map[string]any{"TopicArn": "arn:aws:sns:us-east-1:000000000000:audit-t", "Events": []any{"s3:ObjectCreated:*"}}},
		"NotificationConfiguration.LambdaFunctionConfigurations":     []any{map[string]any{"LambdaFunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:audit", "Events": []any{"s3:ObjectCreated:*"}}},
		"ReplicationConfiguration.Rules":                             []any{map[string]any{"Status": "Enabled", "Destination": map[string]any{"Bucket": "arn:aws:s3:::audit-dest"}}},
		"WebsiteConfiguration.RoutingRules":                          []any{map[string]any{"Redirect": map[string]any{"HostName": "example.invalid"}}},
		"WebsiteConfiguration.ErrorDocument":                         map[string]any{"Key": "error.html"},
		"WebsiteConfiguration.IndexDocument":                         map[string]any{"Suffix": "index.html"},
		"WebsiteConfiguration.RedirectAllRequestsTo":                 map[string]any{"HostName": "example.invalid"},
		"ObjectLockConfiguration.Rule":                               map[string]any{"DefaultRetention": map[string]any{"Mode": "GOVERNANCE", "Days": 1}},
		"CreateBucketConfiguration.Bucket":                           map[string]any{"Type": "Directory", "DataRedundancy": "SingleAvailabilityZone"},
		"CreateBucketConfiguration.Location":                         map[string]any{"Type": "AvailabilityZone", "Name": "usw2-az1"},
		"CreateBucketConfiguration.Tags[]":                           []any{map[string]any{"Key": "env", "Value": "dev"}},
		"NotificationConfiguration.QueueConfigurations[]":            []any{map[string]any{"QueueArn": "arn:aws:sqs:us-east-1:000000000000:audit-q", "Events": []any{"s3:ObjectCreated:*"}}},
		"NotificationConfiguration.TopicConfigurations[]":            []any{map[string]any{"TopicArn": "arn:aws:sns:us-east-1:000000000000:audit-t", "Events": []any{"s3:ObjectCreated:*"}}},
		"NotificationConfiguration.LambdaFunctionConfigurations[]":   []any{map[string]any{"LambdaFunctionArn": "arn:aws:lambda:us-east-1:000000000000:function:audit", "Events": []any{"s3:ObjectCreated:*"}}},
		"ReplicationConfiguration.Rules[].ExistingObjectReplication": map[string]any{"Status": "Enabled"},
		"WebsiteConfiguration.RoutingRules[]":                        []any{map[string]any{"Redirect": map[string]any{"HostName": "example.invalid"}}},
		"AccessControlPolicy.Grants[]":                               []any{map[string]any{"Permission": "READ", "Grantee": map[string]any{"Type": "CanonicalUser", "ID": "000000000000"}}},
	}
}

// prepare gives each mutating case its own thing to act on: a bucket a
// DeleteBucket case removes is one the next case cannot remove, and an upload
// id is consumed by the operation that completes or aborts it.
func prepare(t *testing.T, ts *httptest.Server, f fx, op, mutating string, body map[string]any, n int) {
	t.Helper()
	set := func(k string, v any) {
		if mutating == k || strings.HasPrefix(mutating, k+".") ||
			strings.HasPrefix(mutating, k+"[") || strings.HasPrefix(mutating, k+"{") {
			return
		}
		body[k] = v
	}
	newBucket := func() string {
		name := fmt.Sprintf("audit-b-%d", n)
		mk(t, ts, "CreateBucket", map[string]any{"Bucket": name})
		return name
	}
	newObject := func() string {
		key := fmt.Sprintf("audit/o-%d.txt", n)
		mk(t, ts, "PutObject", map[string]any{"Bucket": bkt, "Key": key})
		return key
	}
	newUpload := func() string {
		resp := mk(t, ts, "CreateMultipartUpload", map[string]any{
			"Bucket": bkt, "Key": fmt.Sprintf("multi/u-%d.bin", n)})
		return xmlField(t, resp, "UploadId")
	}

	switch op {
	case "CreateBucket":
		set("Bucket", fmt.Sprintf("audit-created-%d", n))
	case "DeleteBucket":
		set("Bucket", newBucket())
	case "DeleteBucketCors", "DeleteBucketLifecycle", "DeleteBucketPolicy",
		"DeleteBucketReplication", "DeleteBucketTagging", "DeleteBucketWebsite",
		"DeleteBucketEncryption", "DeleteBucketOwnershipControls", "DeletePublicAccessBlock":
		// Deleting a sub-resource that is not there is a no-op in S3, so a
		// fresh bucket is enough and the fixture's stays configured.
		set("Bucket", newBucket())

	case "PutObject", "CopyObject":
		set("Key", fmt.Sprintf("audit/put-%d.txt", n))
	case "DeleteObject", "DeleteObjectTagging":
		set("Key", newObject())
	case "DeleteObjects":
		set("Delete", map[string]any{"Objects": []any{map[string]any{"Key": newObject()}}})

	case "CreateMultipartUpload":
		set("Key", fmt.Sprintf("multi/created-%d.bin", n))
	case "AbortMultipartUpload":
		set("Key", fmt.Sprintf("multi/u-%d.bin", n))
		set("UploadId", newUpload())
	case "CompleteMultipartUpload":
		// Completing needs a part that was really uploaded, and its ETag.
		key := fmt.Sprintf("multi/u-%d.bin", n)
		id := newUpload()
		resp := mk(t, ts, "UploadPart", map[string]any{
			"Bucket": bkt, "Key": key, "UploadId": id, "PartNumber": 1})
		_ = resp
		etag := listPartETag(t, ts, bkt, key, id)
		set("Key", key)
		set("UploadId", id)
		set("MultipartUpload", map[string]any{"Parts": []any{
			map[string]any{"PartNumber": 1, "ETag": etag}}})
	}
}
