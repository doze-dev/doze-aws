package s3

// Object tagging, object lock, GetObjectAttributes, and small shared helpers.

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/checksum"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

func supportedChecksum(alg string) (string, bool) { return checksum.Supported(alg) }

// ingestFromReader is the copy-path variant of ingestBody: stream an
// arbitrary reader to a blob, computing MD5.
func (s *Server) ingestFromReader(bucket string, r io.Reader) (*ingested, *awshttp.APIError) {
	req, _ := http.NewRequest("PUT", "/", io.NopCloser(r))
	return s.ingestBody(req, bucket)
}

// newSection returns a bounded reader over an io.ReaderAt.
func newSection(f io.ReaderAt, start, length int64) io.Reader {
	return io.NewSectionReader(f, start, length)
}

// ---- object tagging ----

func (s *Server) getObjectTagging(w http.ResponseWriter, bucket, key, versionID string) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type result struct {
		XMLName xml.Name `xml:"Tagging"`
		XMLNS   string   `xml:"xmlns,attr"`
		tagSet
	}
	if v.VersionID != "null" {
		w.Header().Set("x-amz-version-id", v.VersionID)
	}
	writeXML(w, 200, result{XMLNS: s3NS, tagSet: tagsToXML(v.Tags)})
	return nil
}

func (s *Server) putObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key, versionID string) *awshttp.APIError {
	var req tagSet
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if err := s.store.UpdateVersion(bucket, v, func(ov *s3store.ObjectVersion) error {
		ov.Tags = xmlToTags(req)
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

func (s *Server) deleteObjectTagging(w http.ResponseWriter, bucket, key, versionID string) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if err := s.store.UpdateVersion(bucket, v, func(ov *s3store.ObjectVersion) error {
		ov.Tags = nil
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- object lock ----

func (s *Server) getObjectRetention(w http.ResponseWriter, bucket, key, versionID string) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if v.RetainMode == "" {
		return awshttp.Errf(404, "NoSuchObjectLockConfiguration", "object %q has no retention configuration", key)
	}
	type result struct {
		XMLName         xml.Name `xml:"Retention"`
		XMLNS           string   `xml:"xmlns,attr"`
		Mode            string   `xml:"Mode"`
		RetainUntilDate string   `xml:"RetainUntilDate"`
	}
	writeXML(w, 200, result{
		XMLNS: s3NS, Mode: v.RetainMode,
		RetainUntilDate: time.Unix(v.RetainUntil, 0).UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Server) putObjectRetention(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) *awshttp.APIError {
	var req struct {
		Mode            string `xml:"Mode"`
		RetainUntilDate string `xml:"RetainUntilDate"`
	}
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	until, err := time.Parse(time.RFC3339, req.RetainUntilDate)
	if err != nil {
		return awshttp.Errf(400, "MalformedXML", "RetainUntilDate must be RFC3339")
	}
	v, gerr := s.store.GetVersion(bucket, key, q.Get("versionId"))
	if gerr != nil {
		return awshttp.AsAPIError(gerr)
	}
	bypass := strings.EqualFold(r.Header.Get("x-amz-bypass-governance-retention"), "true")
	if uerr := s.store.UpdateVersion(bucket, v, func(ov *s3store.ObjectVersion) error {
		// Shortening COMPLIANCE retention (or GOVERNANCE without bypass) is
		// forbidden; extending is always allowed.
		if ov.RetainUntil > until.Unix() {
			if ov.RetainMode == "COMPLIANCE" || (ov.RetainMode == "GOVERNANCE" && !bypass) {
				return awshttp.Errf(403, "AccessDenied", "existing retention cannot be shortened")
			}
		}
		ov.RetainMode = strings.ToUpper(req.Mode)
		ov.RetainUntil = until.Unix()
		return nil
	}); uerr != nil {
		return awshttp.AsAPIError(uerr)
	}
	w.WriteHeader(200)
	return nil
}

func (s *Server) getObjectLegalHold(w http.ResponseWriter, bucket, key, versionID string) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	status := "OFF"
	if v.LegalHold {
		status = "ON"
	}
	type result struct {
		XMLName xml.Name `xml:"LegalHold"`
		XMLNS   string   `xml:"xmlns,attr"`
		Status  string   `xml:"Status"`
	}
	writeXML(w, 200, result{XMLNS: s3NS, Status: status})
	return nil
}

func (s *Server) putObjectLegalHold(w http.ResponseWriter, r *http.Request, bucket, key, versionID string) *awshttp.APIError {
	var req struct {
		Status string `xml:"Status"`
	}
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if err := s.store.UpdateVersion(bucket, v, func(ov *s3store.ObjectVersion) error {
		ov.LegalHold = strings.EqualFold(req.Status, "ON")
		return nil
	}); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(200)
	return nil
}

// ---- GetObjectAttributes ----

func (s *Server) getObjectAttributes(w http.ResponseWriter, r *http.Request, bucket, key, versionID string) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, versionID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if v.DeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
		return s3store.ErrNoSuchKey(key)
	}
	wanted := map[string]bool{}
	for _, part := range strings.Split(r.Header.Get("x-amz-object-attributes"), ",") {
		wanted[strings.TrimSpace(part)] = true
	}
	type checksumEl struct {
		ChecksumCRC32  string `xml:"ChecksumCRC32,omitempty"`
		ChecksumCRC32C string `xml:"ChecksumCRC32C,omitempty"`
		ChecksumCRC64  string `xml:"ChecksumCRC64NVME,omitempty"`
		ChecksumSHA1   string `xml:"ChecksumSHA1,omitempty"`
		ChecksumSHA256 string `xml:"ChecksumSHA256,omitempty"`
		ChecksumType   string `xml:"ChecksumType,omitempty"`
	}
	type result struct {
		XMLName      xml.Name    `xml:"GetObjectAttributesOutput"`
		XMLNS        string      `xml:"xmlns,attr"`
		ETag         string      `xml:"ETag,omitempty"`
		Checksum     *checksumEl `xml:"Checksum,omitempty"`
		ObjectSize   int64       `xml:"ObjectSize,omitempty"`
		StorageClass string      `xml:"StorageClass,omitempty"`
	}
	res := result{XMLNS: s3NS}
	if wanted["ETag"] {
		res.ETag = v.ETag // GetObjectAttributes returns the unquoted form
	}
	if wanted["ObjectSize"] {
		res.ObjectSize = v.Size
	}
	if wanted["StorageClass"] {
		res.StorageClass = orDefault(v.StorageClass, "STANDARD")
	}
	if wanted["Checksum"] && v.ChecksumAlg != "" {
		el := &checksumEl{ChecksumType: orDefault(v.ChecksumType, "FULL_OBJECT")}
		switch v.ChecksumAlg {
		case "CRC32":
			el.ChecksumCRC32 = v.ChecksumVal
		case "CRC32C":
			el.ChecksumCRC32C = v.ChecksumVal
		case "CRC64NVME":
			el.ChecksumCRC64 = v.ChecksumVal
		case "SHA1":
			el.ChecksumSHA1 = v.ChecksumVal
		case "SHA256":
			el.ChecksumSHA256 = v.ChecksumVal
		}
		res.Checksum = el
	}
	w.Header().Set("Last-Modified", time.Unix(v.LastModified, 0).UTC().Format(http.TimeFormat))
	if v.VersionID != "null" {
		w.Header().Set("x-amz-version-id", v.VersionID)
	}
	writeXML(w, 200, res)
	return nil
}
