package s3

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

func (s *Server) listBuckets(w http.ResponseWriter) *awshttp.APIError {
	buckets, err := s.store.ListBuckets()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type bucketEl struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	}
	type result struct {
		XMLName xml.Name   `xml:"ListAllMyBucketsResult"`
		XMLNS   string     `xml:"xmlns,attr"`
		Owner   owner      `xml:"Owner"`
		Buckets []bucketEl `xml:"Buckets>Bucket"`
	}
	res := result{XMLNS: s3NS, Owner: localOwner(), Buckets: []bucketEl{}}
	for _, b := range buckets {
		res.Buckets = append(res.Buckets, bucketEl{Name: b.Name, CreationDate: iso8601(b.Created)})
	}
	writeXML(w, 200, res)
	return nil
}

func (s *Server) createBucket(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	lock := r.Header.Get("x-amz-bucket-object-lock-enabled") == "true"
	if err := s.store.CreateBucket(bucket, lock); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(200)
	return nil
}

func (s *Server) headBucket(w http.ResponseWriter, bucket string) *awshttp.APIError {
	if _, err := s.store.GetBucket(bucket); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.Header().Set("x-amz-bucket-region", awsident.Region)
	w.WriteHeader(200)
	return nil
}

func (s *Server) deleteBucket(w http.ResponseWriter, bucket string) *awshttp.APIError {
	if err := s.store.DeleteBucket(bucket); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) getBucketLocation(w http.ResponseWriter, bucket string) *awshttp.APIError {
	if _, err := s.store.GetBucket(bucket); err != nil {
		return awshttp.AsAPIError(err)
	}
	// us-east-1 renders as an empty LocationConstraint, the historical quirk
	// SDKs expect.
	type result struct {
		XMLName xml.Name `xml:"LocationConstraint"`
		XMLNS   string   `xml:"xmlns,attr"`
		Value   string   `xml:",chardata"`
	}
	writeXML(w, 200, result{XMLNS: s3NS})
	return nil
}

// ---- versioning ----

func (s *Server) getBucketVersioning(w http.ResponseWriter, bucket string) *awshttp.APIError {
	bk, err := s.store.GetBucket(bucket)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type result struct {
		XMLName xml.Name `xml:"VersioningConfiguration"`
		XMLNS   string   `xml:"xmlns,attr"`
		Status  string   `xml:"Status,omitempty"`
	}
	writeXML(w, 200, result{XMLNS: s3NS, Status: bk.Versioning})
	return nil
}

func (s *Server) putBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	var req struct {
		Status string `xml:"Status"`
	}
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	if req.Status != "Enabled" && req.Status != "Suspended" {
		return awshttp.Errf(400, "MalformedXML", "Status must be Enabled or Suspended, got %q", req.Status)
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		if bk.ObjectLock != "" && req.Status == "Suspended" {
			return awshttp.Errf(409, "InvalidBucketState", "versioning cannot be suspended on an object-lock bucket")
		}
		bk.Versioning = req.Status
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

// ---- tagging ----

type tagSet struct {
	Tags []tagEl `xml:"TagSet>Tag"`
}

type tagEl struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func tagsToXML(tags map[string]string) tagSet {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ts := tagSet{Tags: []tagEl{}}
	for _, k := range keys {
		ts.Tags = append(ts.Tags, tagEl{Key: k, Value: tags[k]})
	}
	return ts
}

func xmlToTags(ts tagSet) map[string]string {
	out := map[string]string{}
	for _, t := range ts.Tags {
		out[t.Key] = t.Value
	}
	return out
}

func (s *Server) getBucketTagging(w http.ResponseWriter, bucket string) *awshttp.APIError {
	bk, err := s.store.GetBucket(bucket)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if len(bk.Tags) == 0 {
		return awshttp.Errf(404, "NoSuchTagSet", "the bucket %s has no tags", bucket)
	}
	type result struct {
		XMLName xml.Name `xml:"Tagging"`
		XMLNS   string   `xml:"xmlns,attr"`
		tagSet
	}
	writeXML(w, 200, result{XMLNS: s3NS, tagSet: tagsToXML(bk.Tags)})
	return nil
}

func (s *Server) putBucketTagging(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	var req tagSet
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		bk.Tags = xmlToTags(req)
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(204)
	return nil
}

// ---- generic stored config documents ----

// bucketDocField maps a config name to its Bucket field accessor.
func bucketDocField(bk *s3store.Bucket, name string) *string {
	switch name {
	case "cors":
		return &bk.CORS
	case "lifecycle":
		return &bk.Lifecycle
	case "website":
		return &bk.Website
	case "object-lock":
		return &bk.ObjectLock
	case "encryption":
		return &bk.Encryption
	case "notification":
		return &bk.Notification
	case "replication":
		return &bk.Replication
	case "logging":
		return &bk.Logging
	case "accelerate":
		return &bk.Accelerate
	case "requestPayment":
		return &bk.RequestPays
	case "policy":
		return &bk.Policy
	}
	return nil
}

// docMissingCode names the error S3 uses when a config document is absent.
var docMissingCode = map[string]string{
	"cors":      "NoSuchCORSConfiguration",
	"lifecycle": "NoSuchLifecycleConfiguration",
	"website":   "NoSuchWebsiteConfiguration",
	"policy":    "NoSuchBucketPolicy",
}

func (s *Server) getBucketDoc(w http.ResponseWriter, bucket, name string) *awshttp.APIError {
	bk, err := s.store.GetBucket(bucket)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	doc := *bucketDocField(bk, name)
	if doc == "" {
		if code, ok := docMissingCode[name]; ok {
			return awshttp.Errf(404, code, "the bucket %s has no %s configuration", bucket, name)
		}
		// Configs that always answer (notification, accelerate, ...) return
		// an empty document of the right root.
		doc = emptyDocFor(name)
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(doc))
	return nil
}

// emptyDocFor renders the empty configuration document for configs that never 404.
func emptyDocFor(name string) string {
	switch name {
	case "notification":
		return xml.Header + `<NotificationConfiguration xmlns="` + s3NS + `"/>`
	case "accelerate":
		return xml.Header + `<AccelerateConfiguration xmlns="` + s3NS + `"/>`
	case "requestPayment":
		return xml.Header + `<RequestPaymentConfiguration xmlns="` + s3NS + `"><Payer>BucketOwner</Payer></RequestPaymentConfiguration>`
	case "encryption":
		return xml.Header + `<ServerSideEncryptionConfiguration xmlns="` + s3NS + `"><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`
	case "logging":
		return xml.Header + `<BucketLoggingStatus xmlns="` + s3NS + `"/>`
	case "object-lock":
		return xml.Header + `<ObjectLockConfiguration xmlns="` + s3NS + `"/>`
	case "replication":
		return xml.Header + `<ReplicationConfiguration xmlns="` + s3NS + `"/>`
	}
	return xml.Header + "<Configuration/>"
}

func (s *Server) putBucketDoc(w http.ResponseWriter, r *http.Request, bucket, name string) *awshttp.APIError {
	doc, aerr := readBodyString(r)
	if aerr != nil {
		return aerr
	}
	// Functional documents must at least parse as XML.
	if name == "cors" || name == "lifecycle" || name == "website" || name == "object-lock" {
		var probe any
		if err := xml.Unmarshal([]byte(doc), &probe); err != nil {
			return awshttp.Errf(400, "MalformedXML", "%s configuration is not well-formed XML", name)
		}
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		*bucketDocField(bk, name) = doc
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

func (s *Server) deleteBucketDoc(w http.ResponseWriter, bucket, name string) *awshttp.APIError {
	field := name
	if name == "tagging" {
		if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
			bk.Tags = nil
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		*bucketDocField(bk, field) = ""
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(204)
	return nil
}

// ---- policy & ACL ----

func (s *Server) getBucketPolicy(w http.ResponseWriter, bucket string) *awshttp.APIError {
	bk, err := s.store.GetBucket(bucket)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if bk.Policy == "" {
		return awshttp.Errf(404, "NoSuchBucketPolicy", "the bucket %s has no policy", bucket)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(bk.Policy))
	return nil
}

func (s *Server) putBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	doc, aerr := readBodyString(r)
	if aerr != nil {
		return aerr
	}
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		bk.Policy = doc
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(204)
	return nil
}

// aclDoc renders the canned full-control ACL every local resource reports.
type aclDoc struct {
	XMLName xml.Name `xml:"AccessControlPolicy"`
	XMLNS   string   `xml:"xmlns,attr"`
	Owner   owner    `xml:"Owner"`
	Grants  []grant  `xml:"AccessControlList>Grant"`
}

type grant struct {
	Grantee    grantee `xml:"Grantee"`
	Permission string  `xml:"Permission"`
}

type grantee struct {
	XMLNSXSI    string `xml:"xmlns:xsi,attr"`
	Type        string `xml:"xsi:type,attr"`
	ID          string `xml:"ID,omitempty"`
	DisplayName string `xml:"DisplayName,omitempty"`
}

func cannedACL() aclDoc {
	o := localOwner()
	return aclDoc{
		XMLNS: s3NS,
		Owner: o,
		Grants: []grant{{
			Grantee:    grantee{XMLNSXSI: "http://www.w3.org/2001/XMLSchema-instance", Type: "CanonicalUser", ID: o.ID, DisplayName: o.DisplayName},
			Permission: "FULL_CONTROL",
		}},
	}
}

func (s *Server) getBucketACL(w http.ResponseWriter, bucket string) *awshttp.APIError {
	if _, err := s.store.GetBucket(bucket); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeXML(w, 200, cannedACL())
	return nil
}

func (s *Server) putBucketACL(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	// Tier C: the canned ACL name is stored; grants have no meaning locally.
	acl := r.Header.Get("x-amz-acl")
	if err := s.store.UpdateBucket(bucket, func(bk *s3store.Bucket) error {
		bk.ACL = acl
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

func (s *Server) getObjectACL(w http.ResponseWriter, bucket, key string) *awshttp.APIError {
	if _, err := s.store.GetVersion(bucket, key, ""); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeXML(w, 200, cannedACL())
	return nil
}

func (s *Server) putObjectACL(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	if _, err := s.store.GetVersion(bucket, key, ""); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200) // accepted, no local meaning
	return nil
}

// ---- listings ----

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request, bucket string, q url.Values) *awshttp.APIError {
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	maxKeys := intOr(q.Get("max-keys"), 1000)
	v2 := q.Get("list-type") == "2"

	after := q.Get("marker") // V1
	token := q.Get("continuation-token")
	if v2 {
		after = q.Get("start-after")
		if token != "" {
			after = token // our continuation token IS the last-delivered key
		}
	}
	res, err := s.store.ListObjects(bucket, prefix, delimiter, after, maxKeys)
	if err != nil {
		return awshttp.AsAPIError(err)
	}

	type object struct {
		Key          string  `xml:"Key"`
		LastModified string  `xml:"LastModified"`
		ETag         string  `xml:"ETag"`
		Size         int64   `xml:"Size"`
		StorageClass string  `xml:"StorageClass"`
		Owner        *owner  `xml:"Owner,omitempty"`
		ChecksumAlg  *string `xml:"ChecksumAlgorithm,omitempty"`
	}
	type prefixEl struct {
		Prefix string `xml:"Prefix"`
	}
	toObjects := func() []object {
		out := []object{}
		fetchOwner := !v2 || q.Get("fetch-owner") == "true"
		for _, e := range res.Entries {
			o := object{
				Key: e.Key, LastModified: iso8601(e.LastModified),
				ETag: quoteETag(e.ETag), Size: e.Size, StorageClass: e.StorageClass,
			}
			if fetchOwner {
				ow := localOwner()
				o.Owner = &ow
			}
			if e.ChecksumAlg != "" {
				alg := e.ChecksumAlg
				o.ChecksumAlg = &alg
			}
			out = append(out, o)
		}
		return out
	}
	prefixes := []prefixEl{}
	for _, p := range res.CommonPrefixes {
		prefixes = append(prefixes, prefixEl{Prefix: p})
	}

	if v2 {
		type result struct {
			XMLName        xml.Name   `xml:"ListBucketResult"`
			XMLNS          string     `xml:"xmlns,attr"`
			Name           string     `xml:"Name"`
			Prefix         string     `xml:"Prefix"`
			Delimiter      string     `xml:"Delimiter,omitempty"`
			StartAfter     string     `xml:"StartAfter,omitempty"`
			MaxKeys        int        `xml:"MaxKeys"`
			KeyCount       int        `xml:"KeyCount"`
			IsTruncated    bool       `xml:"IsTruncated"`
			Token          string     `xml:"ContinuationToken,omitempty"`
			NextToken      string     `xml:"NextContinuationToken,omitempty"`
			Contents       []object   `xml:"Contents"`
			CommonPrefixes []prefixEl `xml:"CommonPrefixes,omitempty"`
		}
		objs := toObjects()
		writeXML(w, 200, result{
			XMLNS: s3NS, Name: bucket, Prefix: prefix, Delimiter: delimiter,
			StartAfter: q.Get("start-after"), MaxKeys: maxKeys,
			KeyCount: len(objs) + len(prefixes), IsTruncated: res.IsTruncated,
			Token: token, NextToken: res.NextToken,
			Contents: objs, CommonPrefixes: prefixes,
		})
		return nil
	}

	type result struct {
		XMLName        xml.Name   `xml:"ListBucketResult"`
		XMLNS          string     `xml:"xmlns,attr"`
		Name           string     `xml:"Name"`
		Prefix         string     `xml:"Prefix"`
		Marker         string     `xml:"Marker"`
		NextMarker     string     `xml:"NextMarker,omitempty"`
		Delimiter      string     `xml:"Delimiter,omitempty"`
		MaxKeys        int        `xml:"MaxKeys"`
		IsTruncated    bool       `xml:"IsTruncated"`
		Contents       []object   `xml:"Contents"`
		CommonPrefixes []prefixEl `xml:"CommonPrefixes,omitempty"`
	}
	writeXML(w, 200, result{
		XMLNS: s3NS, Name: bucket, Prefix: prefix, Marker: q.Get("marker"),
		NextMarker: res.NextToken, Delimiter: delimiter, MaxKeys: maxKeys,
		IsTruncated: res.IsTruncated, Contents: toObjects(), CommonPrefixes: prefixes,
	})
	return nil
}

func (s *Server) listObjectVersions(w http.ResponseWriter, bucket string, q url.Values) *awshttp.APIError {
	res, err := s.store.ListVersions(bucket, q.Get("prefix"), q.Get("delimiter"),
		q.Get("key-marker"), q.Get("version-id-marker"), intOr(q.Get("max-keys"), 1000))
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type versionEl struct {
		Key          string `xml:"Key"`
		VersionID    string `xml:"VersionId"`
		IsLatest     bool   `xml:"IsLatest"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
		Owner        owner  `xml:"Owner"`
	}
	type markerEl struct {
		Key          string `xml:"Key"`
		VersionID    string `xml:"VersionId"`
		IsLatest     bool   `xml:"IsLatest"`
		LastModified string `xml:"LastModified"`
		Owner        owner  `xml:"Owner"`
	}
	type prefixEl struct {
		Prefix string `xml:"Prefix"`
	}
	type result struct {
		XMLName         xml.Name    `xml:"ListVersionsResult"`
		XMLNS           string      `xml:"xmlns,attr"`
		Name            string      `xml:"Name"`
		Prefix          string      `xml:"Prefix"`
		KeyMarker       string      `xml:"KeyMarker"`
		VersionIDMarker string      `xml:"VersionIdMarker"`
		NextKeyMarker   string      `xml:"NextKeyMarker,omitempty"`
		NextVIDMarker   string      `xml:"NextVersionIdMarker,omitempty"`
		MaxKeys         int         `xml:"MaxKeys"`
		IsTruncated     bool        `xml:"IsTruncated"`
		Versions        []versionEl `xml:"Version"`
		DeleteMarkers   []markerEl  `xml:"DeleteMarker"`
		CommonPrefixes  []prefixEl  `xml:"CommonPrefixes,omitempty"`
	}
	out := result{
		XMLNS: s3NS, Name: bucket, Prefix: q.Get("prefix"),
		KeyMarker: q.Get("key-marker"), VersionIDMarker: q.Get("version-id-marker"),
		NextKeyMarker: res.NextKeyMarker, NextVIDMarker: res.NextVersionMark,
		MaxKeys: intOr(q.Get("max-keys"), 1000), IsTruncated: res.IsTruncated,
		Versions: []versionEl{}, DeleteMarkers: []markerEl{},
	}
	for _, v := range res.Versions {
		if v.DeleteMarker {
			out.DeleteMarkers = append(out.DeleteMarkers, markerEl{
				Key: v.Key, VersionID: v.VersionID, IsLatest: v.IsLatest,
				LastModified: iso8601(v.LastModified), Owner: localOwner(),
			})
			continue
		}
		out.Versions = append(out.Versions, versionEl{
			Key: v.Key, VersionID: v.VersionID, IsLatest: v.IsLatest,
			LastModified: iso8601(v.LastModified), ETag: quoteETag(v.ETag),
			Size: v.Size, StorageClass: orDefault(v.StorageClass, "STANDARD"),
			Owner: localOwner(),
		})
	}
	for _, p := range res.CommonPrefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, prefixEl{Prefix: p})
	}
	writeXML(w, 200, out)
	return nil
}

func intOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}
