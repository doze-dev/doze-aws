package s3

// Sub-resource guarding.
//
// S3 does not route by operation name. A request is identified by method, path
// shape, and a query-string SUB-RESOURCE marker — `?acl`, `?tagging`,
// `?ownershipControls` and so on. That makes an unrecognised marker genuinely
// dangerous, because the natural way to write the dispatcher is a switch with a
// default arm, and the default arm is a *different operation*:
//
//	GET    /bucket?ownershipControls  → falls through → lists the bucket
//	DELETE /object?annotation         → falls through → DELETES THE OBJECT
//
// Both were real behaviour before this file existed. The first returns the
// wrong document; the second destroys data the caller never asked to touch.
//
// So every marker AWS defines is enumerated here. One this build does not
// implement gets a clean NotImplemented naming the operation, and never
// reaches a handler that would do something else entirely.

import (
	"net/url"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// unsupportedBucketSubresources maps a bucket-level query marker to the AWS
// operation it selects, for markers doze-aws does not implement.
var unsupportedBucketSubresources = map[string]string{
	"abac":                    "GetBucketAbac / PutBucketAbac",
	"analytics":               "bucket analytics configuration",
	"intelligent-tiering":     "bucket intelligent-tiering configuration",
	"inventory":               "bucket inventory configuration",
	"metrics":                 "bucket metrics configuration",
	"metadataConfiguration":   "bucket metadata configuration",
	"metadataTable":           "bucket metadata table configuration",
	"metadataAnnotationTable": "bucket metadata annotation table configuration",
	"metadataInventoryTable":  "bucket metadata inventory table configuration",
	"metadataJournalTable":    "bucket metadata journal table configuration",
	"ownershipControls":       "bucket ownership controls",
	"publicAccessBlock":       "bucket public access block",
	"policyStatus":            "GetBucketPolicyStatus",
	"session":                 "CreateSession (directory buckets)",
}

// unsupportedObjectSubresources is the same for object-level markers.
//
// `encryption` appears here but not in the bucket table: bucket-level
// encryption IS implemented, object-level UpdateObjectEncryption is not.
var unsupportedObjectSubresources = map[string]string{
	"annotation":   "object annotations",
	"encryption":   "UpdateObjectEncryption",
	"renameObject": "RenameObject",
	"restore":      "RestoreObject",
	"select":       "SelectObjectContent",
	"select-type":  "SelectObjectContent",
	"torrent":      "GetObjectTorrent",
}

// checkBucketSubresource reports a clean error when a request names a
// bucket-level sub-resource this build does not implement.
func checkBucketSubresource(q url.Values) *awshttp.APIError {
	return checkSubresource(q, unsupportedBucketSubresources)
}

// checkObjectSubresource is the object-level equivalent.
func checkObjectSubresource(q url.Values) *awshttp.APIError {
	return checkSubresource(q, unsupportedObjectSubresources)
}

func checkSubresource(q url.Values, table map[string]string) *awshttp.APIError {
	for marker, op := range table {
		if q.Has(marker) {
			return awshttp.Errf(501, "NotImplemented",
				"doze-aws does not implement %s (?%s); the request was refused rather than "+
					"silently treated as a different operation", op, marker)
		}
	}
	return nil
}
