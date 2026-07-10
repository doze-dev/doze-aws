package s3

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

// putObject handles PUT /bucket/key.
func (s *Server) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	in, aerr := s.ingestBody(r, bucket)
	if aerr != nil {
		return aerr
	}
	meta, headers := collectMeta(r.Header)
	v := s3store.ObjectVersion{
		Key:          key,
		Blob:         in.Blob,
		Size:         in.Size,
		ETag:         in.MD5Hex,
		ChecksumAlg:  in.ChecksumAlg,
		ChecksumVal:  in.ChecksumVal,
		ChecksumType: "FULL_OBJECT",
		ContentType:  r.Header.Get("Content-Type"),
		Meta:         meta,
		Headers:      headers,
		StorageClass: r.Header.Get("x-amz-storage-class"),
		Tags:         parseTaggingHeader(r.Header.Get("x-amz-tagging")),
	}
	if in.ChecksumAlg == "" {
		v.ChecksumType = ""
	}
	if aerr := applyLockHeaders(&v, r.Header, s.now()); aerr != nil {
		s.store.DeleteBlob(in.Blob)
		return aerr
	}
	stored, err := s.store.PutVersion(bucket, v, r.Header.Get("If-None-Match"), r.Header.Get("If-Match"))
	if err != nil {
		s.store.DeleteBlob(in.Blob)
		return awshttp.AsAPIError(err)
	}
	w.Header().Set("ETag", quoteETag(stored.ETag))
	if stored.VersionID != "null" {
		w.Header().Set("x-amz-version-id", stored.VersionID)
	}
	if stored.ChecksumAlg != "" {
		w.Header().Set("x-amz-checksum-"+strings.ToLower(stored.ChecksumAlg), stored.ChecksumVal)
		w.Header().Set("x-amz-checksum-type", stored.ChecksumType)
	}
	s.notify(bucket, key, "s3:ObjectCreated:Put", stored)
	w.WriteHeader(200)
	return nil
}

// applyLockHeaders folds object-lock request headers into a version.
func applyLockHeaders(v *s3store.ObjectVersion, h http.Header, now time.Time) *awshttp.APIError {
	if mode := h.Get("x-amz-object-lock-mode"); mode != "" {
		until := h.Get("x-amz-object-lock-retain-until-date")
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return awshttp.Errf(400, "InvalidArgument", "x-amz-object-lock-retain-until-date must be RFC3339, got %q", until)
		}
		if !t.After(now) {
			return awshttp.Errf(400, "InvalidArgument", "retain-until date must be in the future")
		}
		v.RetainMode = strings.ToUpper(mode)
		v.RetainUntil = t.Unix()
	}
	if lh := h.Get("x-amz-object-lock-legal-hold"); lh != "" {
		v.LegalHold = strings.EqualFold(lh, "ON")
	}
	return nil
}

// parseTaggingHeader parses the x-amz-tagging query-encoded tag set.
func parseTaggingHeader(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range vals {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// getObject handles GET/HEAD /bucket/key with conditional and range semantics.
func (s *Server) getObject(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values, headOnly bool) *awshttp.APIError {
	v, err := s.store.GetVersion(bucket, key, q.Get("versionId"))
	if err != nil {
		if aerr := awshttp.AsAPIError(err); aerr.Code == "NoSuchKey" && !headOnly {
			// Directory-style requests serve the website index document...
			if strings.HasSuffix(key, "/") {
				if handled := s.serveWebsite(w, r, bucket, key, false); handled {
					return nil
				}
			}
			// ...and other misses serve the website error document.
			if handled := s.serveWebsite(w, r, bucket, key, true); handled {
				return nil
			}
		}
		return awshttp.AsAPIError(err)
	}
	if v.DeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
		if q.Get("versionId") != "" {
			return awshttp.Errf(405, "MethodNotAllowed", "the specified version is a delete marker")
		}
		return s3store.ErrNoSuchKey(key)
	}

	// Conditional requests, in S3's evaluation order.
	lastMod := time.Unix(v.LastModified, 0).UTC()
	if im := r.Header.Get("If-Match"); im != "" && trimQuotes(im) != v.ETag {
		return awshttp.Errf(412, "PreconditionFailed", "If-Match failed")
	}
	if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil && lastMod.After(t) {
			return awshttp.Errf(412, "PreconditionFailed", "If-Unmodified-Since failed")
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && trimQuotes(inm) == v.ETag {
		writeCommonHeaders(w, v)
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && !lastMod.After(t) {
			writeCommonHeaders(w, v)
			w.WriteHeader(http.StatusNotModified)
			return nil
		}
	}

	writeCommonHeaders(w, v)
	if strings.EqualFold(r.Header.Get("x-amz-checksum-mode"), "ENABLED") && v.ChecksumAlg != "" {
		w.Header().Set("x-amz-checksum-"+strings.ToLower(v.ChecksumAlg), v.ChecksumVal)
		w.Header().Set("x-amz-checksum-type", orDefault(v.ChecksumType, "FULL_OBJECT"))
	}
	if len(v.Tags) > 0 {
		w.Header().Set("x-amz-tagging-count", strconv.Itoa(len(v.Tags)))
	}

	// Range handling: one range honored; multiple ranges fall back to 200
	// (documented limitation, common among emulators).
	start, length, isRange, aerr := parseRange(r.Header.Get("Range"), v.Size)
	if aerr != nil {
		return aerr
	}
	if headOnly {
		if isRange {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, v.Size))
			w.Header().Set("Content-Length", fmtInt(length))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", fmtInt(v.Size))
			w.WriteHeader(200)
		}
		return nil
	}

	f, ferr := s.store.OpenBlob(v.Blob)
	if ferr != nil {
		return awshttp.Errf(500, "InternalError", "open blob: %v", ferr)
	}
	defer f.Close()
	if isRange {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return awshttp.Errf(500, "InternalError", "seek: %v", err)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, v.Size))
		w.Header().Set("Content-Length", fmtInt(length))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.CopyN(w, f, length)
		return nil
	}
	w.Header().Set("Content-Length", fmtInt(v.Size))
	w.WriteHeader(200)
	_, _ = io.Copy(w, f)
	return nil
}

// writeCommonHeaders emits the metadata headers GET and HEAD share.
func writeCommonHeaders(w http.ResponseWriter, v *s3store.ObjectVersion) {
	h := w.Header()
	h.Set("ETag", quoteETag(v.ETag))
	h.Set("Last-Modified", time.Unix(v.LastModified, 0).UTC().Format(http.TimeFormat))
	h.Set("Accept-Ranges", "bytes")
	if v.ContentType != "" {
		h.Set("Content-Type", v.ContentType)
	} else {
		h.Set("Content-Type", "application/octet-stream")
	}
	if v.VersionID != "null" {
		h.Set("x-amz-version-id", v.VersionID)
	}
	for name, val := range v.Headers {
		h.Set(name, val)
	}
	for name, val := range v.Meta {
		h.Set("x-amz-meta-"+name, val)
	}
	if v.RetainMode != "" {
		h.Set("x-amz-object-lock-mode", v.RetainMode)
		h.Set("x-amz-object-lock-retain-until-date", time.Unix(v.RetainUntil, 0).UTC().Format(time.RFC3339))
	}
	if v.LegalHold {
		h.Set("x-amz-object-lock-legal-hold", "ON")
	}
	if v.StorageClass != "" && v.StorageClass != "STANDARD" {
		h.Set("x-amz-storage-class", v.StorageClass)
	}
}

// parseRange interprets a single-range header. Multi-range and unparseable
// specs fall back to a full response, matching common emulator behavior;
// unsatisfiable ranges error like S3.
func parseRange(spec string, size int64) (start, length int64, ok bool, aerr *awshttp.APIError) {
	if spec == "" || !strings.HasPrefix(spec, "bytes=") || strings.Contains(spec, ",") {
		return 0, 0, false, nil
	}
	rng := strings.TrimPrefix(spec, "bytes=")
	fromS, toS, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false, nil
	}
	if fromS == "" {
		// suffix range: last N bytes
		n, err := strconv.ParseInt(toS, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, nil
		}
		if n > size {
			n = size
		}
		return size - n, n, true, nil
	}
	from, err := strconv.ParseInt(fromS, 10, 64)
	if err != nil || from < 0 {
		return 0, 0, false, nil
	}
	if from >= size {
		return 0, 0, false, awshttp.Errf(416, "InvalidRange", "the requested range is not satisfiable")
	}
	to := size - 1
	if toS != "" {
		t, err := strconv.ParseInt(toS, 10, 64)
		if err != nil {
			return 0, 0, false, nil
		}
		if t < size {
			to = t
		}
	}
	if to < from {
		return 0, 0, false, awshttp.Errf(416, "InvalidRange", "the requested range is not satisfiable")
	}
	return from, to - from + 1, true, nil
}

// deleteObject handles DELETE /bucket/key.
func (s *Server) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) *awshttp.APIError {
	bypass := strings.EqualFold(r.Header.Get("x-amz-bypass-governance-retention"), "true")
	marker, affected, err := s.store.DeleteObject(bucket, key, q.Get("versionId"), bypass)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if marker {
		w.Header().Set("x-amz-delete-marker", "true")
	}
	if affected != "" {
		w.Header().Set("x-amz-version-id", affected)
	}
	s.notify(bucket, key, "s3:ObjectRemoved:Delete", nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deleteObjects handles POST /bucket?delete (batch delete).
func (s *Server) deleteObjects(w http.ResponseWriter, r *http.Request, bucket string) *awshttp.APIError {
	var req struct {
		Quiet   bool `xml:"Quiet"`
		Objects []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"Object"`
	}
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	bypass := strings.EqualFold(r.Header.Get("x-amz-bypass-governance-retention"), "true")

	type deleted struct {
		Key                   string `xml:"Key"`
		VersionID             string `xml:"VersionId,omitempty"`
		DeleteMarker          bool   `xml:"DeleteMarker,omitempty"`
		DeleteMarkerVersionID string `xml:"DeleteMarkerVersionId,omitempty"`
	}
	type failed struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	type result struct {
		XMLName xml.Name  `xml:"DeleteResult"`
		XMLNS   string    `xml:"xmlns,attr"`
		Deleted []deleted `xml:"Deleted"`
		Errors  []failed  `xml:"Error"`
	}
	res := result{XMLNS: s3NS}
	for _, o := range req.Objects {
		marker, affected, err := s.store.DeleteObject(bucket, o.Key, o.VersionID, bypass)
		if err != nil {
			ae := awshttp.AsAPIError(err)
			res.Errors = append(res.Errors, failed{Key: o.Key, Code: ae.Code, Message: ae.Message})
			continue
		}
		if !req.Quiet {
			d := deleted{Key: o.Key, VersionID: o.VersionID}
			if marker {
				d.DeleteMarker, d.DeleteMarkerVersionID = true, affected
			}
			res.Deleted = append(res.Deleted, d)
		}
	}
	writeXML(w, 200, res)
	return nil
}

// copyObject handles PUT /bucket/key with x-amz-copy-source.
func (s *Server) copyObject(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	srcBucket, srcKey, srcVersion, aerr := parseCopySource(r.Header.Get("x-amz-copy-source"))
	if aerr != nil {
		return aerr
	}
	src, err := s.store.GetVersion(srcBucket, srcKey, srcVersion)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if src.DeleteMarker {
		return s3store.ErrNoSuchKey(srcKey)
	}

	// Copy-source conditionals.
	srcMod := time.Unix(src.LastModified, 0).UTC()
	if v := r.Header.Get("x-amz-copy-source-if-match"); v != "" && trimQuotes(v) != src.ETag {
		return awshttp.Errf(412, "PreconditionFailed", "copy-source If-Match failed")
	}
	if v := r.Header.Get("x-amz-copy-source-if-none-match"); v != "" && trimQuotes(v) == src.ETag {
		return awshttp.Errf(412, "PreconditionFailed", "copy-source If-None-Match failed")
	}
	if v := r.Header.Get("x-amz-copy-source-if-unmodified-since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && srcMod.After(t) {
			return awshttp.Errf(412, "PreconditionFailed", "copy-source If-Unmodified-Since failed")
		}
	}
	if v := r.Header.Get("x-amz-copy-source-if-modified-since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && !srcMod.After(t) {
			return awshttp.Errf(412, "PreconditionFailed", "copy-source If-Modified-Since failed")
		}
	}

	f, ferr := s.store.OpenBlob(src.Blob)
	if ferr != nil {
		return awshttp.Errf(500, "InternalError", "open source blob: %v", ferr)
	}
	blob, size, err := s.store.WriteBlob(bucket, f)
	f.Close()
	if err != nil {
		return awshttp.AsAPIError(err)
	}

	v := s3store.ObjectVersion{
		Key: key, Blob: blob, Size: size,
		ETag:         src.ETag,
		ChecksumAlg:  src.ChecksumAlg,
		ChecksumVal:  src.ChecksumVal,
		ChecksumType: src.ChecksumType,
		ContentType:  src.ContentType,
		Meta:         src.Meta,
		Headers:      src.Headers,
		Tags:         src.Tags,
	}
	if strings.EqualFold(r.Header.Get("x-amz-metadata-directive"), "REPLACE") {
		meta, headers := collectMeta(r.Header)
		v.Meta, v.Headers = meta, headers
		if ct := r.Header.Get("Content-Type"); ct != "" {
			v.ContentType = ct
		}
	}
	if strings.EqualFold(r.Header.Get("x-amz-tagging-directive"), "REPLACE") {
		v.Tags = parseTaggingHeader(r.Header.Get("x-amz-tagging"))
	}
	if aerr := applyLockHeaders(&v, r.Header, s.now()); aerr != nil {
		s.store.DeleteBlob(blob)
		return aerr
	}
	stored, err := s.store.PutVersion(bucket, v, "", "")
	if err != nil {
		s.store.DeleteBlob(blob)
		return awshttp.AsAPIError(err)
	}
	if stored.VersionID != "null" {
		w.Header().Set("x-amz-version-id", stored.VersionID)
	}
	if srcVersion != "" {
		w.Header().Set("x-amz-copy-source-version-id", srcVersion)
	}
	type copyResult struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		XMLNS        string   `xml:"xmlns,attr"`
		ETag         string   `xml:"ETag"`
		LastModified string   `xml:"LastModified"`
	}
	s.notify(bucket, key, "s3:ObjectCreated:Copy", stored)
	writeXML(w, 200, copyResult{XMLNS: s3NS, ETag: quoteETag(stored.ETag), LastModified: iso8601(stored.LastModified)})
	return nil
}

// parseCopySource decodes "/bucket/key?versionId=v" (leading slash optional).
func parseCopySource(raw string) (bucket, key, versionID string, aerr *awshttp.APIError) {
	if raw == "" {
		return "", "", "", awshttp.Errf(400, "InvalidArgument", "x-amz-copy-source is required")
	}
	if i := strings.Index(raw, "?"); i >= 0 {
		q, _ := url.ParseQuery(raw[i+1:])
		versionID = q.Get("versionId")
		raw = raw[:i]
	}
	raw = strings.TrimPrefix(raw, "/")
	unescaped, err := url.PathUnescape(raw)
	if err != nil {
		return "", "", "", awshttp.Errf(400, "InvalidArgument", "x-amz-copy-source is not valid")
	}
	b, k, ok := strings.Cut(unescaped, "/")
	if !ok || b == "" || k == "" {
		return "", "", "", awshttp.Errf(400, "InvalidArgument", "x-amz-copy-source must be /bucket/key")
	}
	return b, k, versionID, nil
}

func trimQuotes(s string) string {
	return strings.Trim(s, `"`)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
