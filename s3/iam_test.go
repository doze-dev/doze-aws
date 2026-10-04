package s3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/doze-dev/doze-aws/internal/dozetest"
)

func iamServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Suffix: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The permission an S3 request is authorized as is the one AWS's Service
// Authorization Reference gives its operation — which is not always the
// operation's name, and was often guessed wrong: DeleteBucketCors is
// s3:PutBucketCORS, ListObjectVersions is s3:ListBucketVersions, and a bucket's
// ACL is s3:GetBucketAcl, not s3:GetAcl.
func TestEveryRouteIsAuthorizedAsAWSNamesIt(t *testing.T) {
	s := iamServer(t)
	const b = "arn:aws:s3:::docs"
	const o = "arn:aws:s3:::docs/report.pdf"
	for _, c := range []struct {
		name             string
		method, path     string
		action, resource string
	}{
		{"list buckets", "GET", "/", "s3:ListAllMyBuckets", ""},
		{"list a bucket", "GET", "/docs", "s3:ListBucket", b},
		{"list a bucket, v2", "GET", "/docs?list-type=2", "s3:ListBucket", b},
		{"head a bucket", "HEAD", "/docs", "s3:ListBucket", b},
		{"create a bucket", "PUT", "/docs", "s3:CreateBucket", b},
		{"delete a bucket", "DELETE", "/docs", "s3:DeleteBucket", b},
		{"batch delete", "POST", "/docs?delete=", "s3:DeleteObject", b},
		{"get an object", "GET", "/docs/report.pdf", "s3:GetObject", o},
		{"head an object", "HEAD", "/docs/report.pdf", "s3:GetObject", o},
		{"put an object", "PUT", "/docs/report.pdf", "s3:PutObject", o},
		{"copy an object", "PUT", "/docs/report.pdf", "s3:PutObject", o}, // + the source's read, by the guard
		{"delete an object", "DELETE", "/docs/report.pdf", "s3:DeleteObject", o},

		// A request that names a version is authorized as the version's action.
		{"get a version", "GET", "/docs/report.pdf?versionId=v1", "s3:GetObjectVersion", o},
		{"delete a version", "DELETE", "/docs/report.pdf?versionId=v1", "s3:DeleteObjectVersion", o},
		{"get a version's tags", "GET", "/docs/report.pdf?tagging&versionId=v1", "s3:GetObjectVersionTagging", o},

		// What the path-guessing resolver named wrongly or not at all.
		{"get bucket acl", "GET", "/docs?acl", "s3:GetBucketAcl", b},
		{"put bucket acl", "PUT", "/docs?acl", "s3:PutBucketAcl", b},
		{"get object acl", "GET", "/docs/report.pdf?acl", "s3:GetObjectAcl", o},
		{"delete bucket cors", "DELETE", "/docs?cors", "s3:PutBucketCORS", b},
		{"delete bucket lifecycle", "DELETE", "/docs?lifecycle", "s3:PutLifecycleConfiguration", b},
		{"delete bucket encryption", "DELETE", "/docs?encryption", "s3:PutEncryptionConfiguration", b},
		{"delete bucket tagging", "DELETE", "/docs?tagging", "s3:PutBucketTagging", b},
		{"list object versions", "GET", "/docs?versions", "s3:ListBucketVersions", b},
		{"get object lock configuration", "GET", "/docs?object-lock", "s3:GetBucketObjectLockConfiguration", b},
		{"put object lock configuration", "PUT", "/docs?object-lock", "s3:PutBucketObjectLockConfiguration", b},
		{"get public access block", "GET", "/docs?publicAccessBlock", "s3:GetBucketPublicAccessBlock", b},
		{"put public access block", "PUT", "/docs?publicAccessBlock", "s3:PutBucketPublicAccessBlock", b},
		{"delete public access block", "DELETE", "/docs?publicAccessBlock", "s3:PutBucketPublicAccessBlock", b},
		{"get bucket policy status", "GET", "/docs?policyStatus", "s3:GetBucketPolicyStatus", b},
		{"get ownership controls", "GET", "/docs?ownershipControls", "s3:GetBucketOwnershipControls", b},
		{"get bucket location", "GET", "/docs?location", "s3:GetBucketLocation", b},
		{"get logging", "GET", "/docs?logging", "s3:GetBucketLogging", b},
		{"get accelerate", "GET", "/docs?accelerate", "s3:GetAccelerateConfiguration", b},
		{"get request payment", "GET", "/docs?requestPayment", "s3:GetBucketRequestPayment", b},
		{"get notification", "GET", "/docs?notification", "s3:GetBucketNotification", b},
		{"put notification", "PUT", "/docs?notification", "s3:PutBucketNotification", b},
		{"get retention", "GET", "/docs/report.pdf?retention", "s3:GetObjectRetention", o},
		{"put legal hold", "PUT", "/docs/report.pdf?legal-hold", "s3:PutObjectLegalHold", o},
		{"get attributes", "GET", "/docs/report.pdf?attributes", "s3:GetObject", o},
		{"bucket policy", "PUT", "/docs?policy", "s3:PutBucketPolicy", b},
		{"delete bucket policy", "DELETE", "/docs?policy", "s3:DeleteBucketPolicy", b},
		{"object tagging", "PUT", "/docs/report.pdf?tagging", "s3:PutObjectTagging", o},

		// Multipart, authorized as AWS names it.
		{"list multipart uploads", "GET", "/docs?uploads", "s3:ListBucketMultipartUploads", b},
		{"initiate multipart", "POST", "/docs/report.pdf?uploads", "s3:PutObject", o},
		{"upload a part", "PUT", "/docs/report.pdf?uploadId=x&partNumber=1", "s3:PutObject", o},
		{"complete multipart", "POST", "/docs/report.pdf?uploadId=x", "s3:PutObject", o},
		{"abort multipart", "DELETE", "/docs/report.pdf?uploadId=x", "s3:AbortMultipartUpload", o},
		{"list parts", "GET", "/docs/report.pdf?uploadId=x", "s3:ListMultipartUploadParts", o},

		{"an unrouted method is not evaluated", "PATCH", "/docs/report.pdf", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.path, nil)
			if c.name == "copy an object" {
				r.Header.Set("x-amz-copy-source", "/src/key")
			}
			action, resource := s.resolveIAM(r)
			if action != c.action || resource != c.resource {
				t.Errorf("%s %s = (%q, %q), want (%q, %q)", c.method, c.path, action, resource, c.action, c.resource)
			}
		})
	}
}

// A virtual-hosted request names its bucket in the Host, so the path alone says
// "/report.pdf": a path-guessing resolver read it as a bucket called
// report.pdf and evaluated a bucket listing. The router sees one shape.
func TestAVirtualHostedRequestIsAuthorizedAsTheObjectItNames(t *testing.T) {
	s := iamServer(t)
	r := httptest.NewRequest(http.MethodGet, "/report.pdf", nil)
	r.Host = "docs.s3.us-east-1.localhost"
	action, resource := s.resolveIAM(r)
	if action != "s3:GetObject" || resource != "arn:aws:s3:::docs/report.pdf" {
		t.Errorf("vhost GET = (%q, %q), want s3:GetObject on docs/report.pdf", action, resource)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "docs.s3.us-east-1.localhost"
	if action, resource := s.resolveIAM(r); action != "s3:ListBucket" || resource != "arn:aws:s3:::docs" {
		t.Errorf("vhost bucket listing = (%q, %q), want s3:ListBucket on docs", action, resource)
	}
}

// Every operation the router serves has an action, and the committed table is
// what dzaudit regenerates from AWS's reference — the weekly drift check
// refreshes the fixture, and this is what notices when the code is behind it.
func TestTheActionTableIsWhatTheReferenceSays(t *testing.T) {
	for _, rt := range routes {
		if iamActions()[rt.Op] == "" && rt.Op != "ListDirectoryBuckets" {
			t.Errorf("%s has no IAM action", rt.Op)
		}
	}
	raw, err := os.ReadFile("testdata/iam_s3.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Actions   map[string]string
		Versioned map[string]string
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for op, want := range fixture.Actions {
		if got := iamActions()[op]; got != want {
			t.Errorf("%s: the code says %q, the fixture %q — regenerate: dzaudit iam -go s3 s3", op, got, want)
		}
	}
	for action, want := range fixture.Versioned {
		if got := iamVersioned()[action]; got != want {
			t.Errorf("%s: the code says %q for a version, the fixture %q", action, got, want)
		}
	}
}
