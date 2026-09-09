package s3

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

// Public access block, ownership controls and policy status.
//
// A bucket's public access block is the one S3 access control that has a
// local effect worth having: BlockPublicPolicy refuses a bucket policy that
// would grant to everyone, which is the mistake it exists to catch, and a
// bucket is born with all four blocks on, as on AWS. The ACL blocks are
// stored and reported — ACLs are canned here, so there is nothing for them
// to evaluate. Ownership controls are stored and reported for the same
// reason. GetBucketPolicyStatus is computed from the stored policy.

// publicAccessBlock is the parsed configuration.
type publicAccessBlock struct {
	XMLName               xml.Name `xml:"PublicAccessBlockConfiguration"`
	BlockPublicAcls       bool     `xml:"BlockPublicAcls"`
	IgnorePublicAcls      bool     `xml:"IgnorePublicAcls"`
	BlockPublicPolicy     bool     `xml:"BlockPublicPolicy"`
	RestrictPublicBuckets bool     `xml:"RestrictPublicBuckets"`
}

// parsePublicAccessBlock reads a PutPublicAccessBlock body. A missing
// element is false, as on AWS.
func parsePublicAccessBlock(doc string) (publicAccessBlock, *awshttp.APIError) {
	var pab publicAccessBlock
	if err := xml.Unmarshal([]byte(doc), &pab); err != nil || pab.XMLName.Local != "PublicAccessBlockConfiguration" {
		return pab, awshttp.Errf(400, "MalformedXML", "the public access block configuration is not well-formed")
	}
	return pab, nil
}

// blockOf is the stored configuration; the default when none is stored.
func blockOf(bk *s3store.Bucket) publicAccessBlock {
	doc := bk.PublicAccessBlock
	if doc == "" {
		return publicAccessBlock{}
	}
	pab, _ := parsePublicAccessBlock(doc)
	return pab
}

// putPublicAccessBlock stores the configuration after checking its shape.
func (s *Server) putPublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	doc, aerr := readBodyString(r)
	if aerr != nil {
		return aerr
	}
	if _, aerr := parsePublicAccessBlock(doc); aerr != nil {
		return aerr
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		bk.PublicAccessBlock = doc
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

// ownershipValues are the settings ObjectOwnership accepts.
var ownershipValues = map[string]bool{"BucketOwnerPreferred": true, "ObjectWriter": true, "BucketOwnerEnforced": true}

// putBucketOwnershipControls stores the rule after checking the setting.
func (s *Server) putBucketOwnershipControls(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	doc, aerr := readBodyString(r)
	if aerr != nil {
		return aerr
	}
	var oc struct {
		XMLName xml.Name `xml:"OwnershipControls"`
		Rules   []struct {
			ObjectOwnership string `xml:"ObjectOwnership"`
		} `xml:"Rule"`
	}
	if err := xml.Unmarshal([]byte(doc), &oc); err != nil || oc.XMLName.Local != "OwnershipControls" || len(oc.Rules) != 1 {
		return awshttp.Errf(400, "MalformedXML", "the ownership controls document needs exactly one Rule")
	}
	if !ownershipValues[oc.Rules[0].ObjectOwnership] {
		return awshttp.Errf(400, "MalformedXML", "ObjectOwnership must be BucketOwnerPreferred, ObjectWriter or BucketOwnerEnforced")
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		bk.OwnershipControls = doc
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

// getBucketPolicyStatus answers whether the stored policy makes the bucket
// public; a bucket with no policy has no status, as on AWS.
func (s *Server) getBucketPolicyStatus(w http.ResponseWriter, bucket string) *awshttp.APIError {
	bk, err := s.store.GetBucket(bucket)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if bk.Policy == "" {
		return awshttp.Errf(404, "NoSuchBucketPolicy", "the bucket %s has no policy", bucket)
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	isPublic := "false"
	if policyIsPublic(bk.Policy) {
		isPublic = "true"
	}
	_, _ = w.Write([]byte(xml.Header + `<PolicyStatus xmlns="` + s3NS + `"><IsPublic>` + isPublic + `</IsPublic></PolicyStatus>`))
	return nil
}

// policyIsPublic applies S3's rule in its simplest honest form: an Allow
// statement whose Principal is everyone, with no Condition narrowing it,
// makes the bucket public. A policy that does not parse is not public.
func policyIsPublic(policy string) bool {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if json.Unmarshal([]byte(policy), &doc) != nil {
		return false
	}
	var statements []map[string]json.RawMessage
	if err := json.Unmarshal(doc.Statement, &statements); err != nil {
		var one map[string]json.RawMessage
		if json.Unmarshal(doc.Statement, &one) != nil {
			return false
		}
		statements = []map[string]json.RawMessage{one}
	}
	for _, st := range statements {
		var effect string
		json.Unmarshal(st["Effect"], &effect)
		if effect != "Allow" {
			continue
		}
		// A NotPrincipal Allow grants everyone it does not name: public.
		if len(st["NotPrincipal"]) > 0 {
			return true
		}
		if !principalIsEveryone(st["Principal"]) {
			continue
		}
		if !conditionNarrows(st["Condition"]) {
			return true
		}
	}
	return false
}

// conditionNarrows reports whether a "*" statement's Condition makes it
// non-public by AWS's rule: only a condition on one of a fixed set of keys
// (the caller's network, source, account or organization) counts. Any
// other condition — aws:SecureTransport, a date, s3:prefix — leaves the
// statement public.
func conditionNarrows(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var cond map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &cond) != nil {
		return false
	}
	for op, keys := range cond {
		// A negated operator widens rather than narrows.
		if strings.HasPrefix(strings.ToLower(op), "stringnot") || strings.HasPrefix(strings.ToLower(op), "arnnot") ||
			strings.HasPrefix(strings.ToLower(op), "notipaddress") {
			continue
		}
		for key := range keys {
			switch strings.ToLower(key) {
			case "aws:sourceip", "aws:sourcevpc", "aws:sourcevpce", "aws:sourcearn", "aws:sourceaccount", "aws:sourceowner",
				"aws:principalarn", "aws:principalaccount", "aws:principalorgid", "aws:principalorgpaths", "aws:userid",
				"s3:dataaccesspointarn", "s3:dataaccesspointaccount", "s3:accesspointnetworkorigin", "aws:vpcsourceip":
				return true
			}
		}
	}
	return false
}

// principalIsEveryone recognises "*", {"AWS":"*"} and {"AWS":["*"]}.
func principalIsEveryone(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == `"*"` {
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	aws, ok := obj["AWS"]
	if !ok {
		return false
	}
	var one string
	if json.Unmarshal(aws, &one) == nil {
		return one == "*"
	}
	var many []string
	if json.Unmarshal(aws, &many) == nil {
		for _, p := range many {
			if p == "*" {
				return true
			}
		}
	}
	return false
}

// blockedByPublicAccess says whether a policy may not be put on the bucket:
// BlockPublicPolicy is on and the policy would make the bucket public.
func blockedByPublicAccess(bk *s3store.Bucket, policy string) bool {
	return blockOf(bk).BlockPublicPolicy && policyIsPublic(policy)
}
