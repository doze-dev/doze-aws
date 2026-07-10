package s3

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/s3store"
)

func (s *Server) createMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) *awshttp.APIError {
	meta, headers := collectMeta(r.Header)
	alg := ""
	if a := r.Header.Get("x-amz-checksum-algorithm"); a != "" {
		canonical, ok := supportedChecksum(a)
		if !ok {
			return awshttp.Errf(400, "InvalidRequest", "unsupported checksum algorithm %q", a)
		}
		alg = canonical
	}
	ctype := strings.ToUpper(r.Header.Get("x-amz-checksum-type"))
	if ctype == "" && alg != "" {
		ctype = "COMPOSITE"
	}
	up, err := s.store.CreateUpload(bucket, s3store.Upload{
		Key:          key,
		ContentType:  r.Header.Get("Content-Type"),
		Meta:         meta,
		Headers:      headers,
		Tags:         parseTaggingHeader(r.Header.Get("x-amz-tagging")),
		ChecksumAlg:  alg,
		ChecksumType: ctype,
	})
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type result struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		XMLNS    string   `xml:"xmlns,attr"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}
	if alg != "" {
		w.Header().Set("x-amz-checksum-algorithm", alg)
		w.Header().Set("x-amz-checksum-type", ctype)
	}
	writeXML(w, 200, result{XMLNS: s3NS, Bucket: bucket, Key: key, UploadID: up.ID})
	return nil
}

func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) *awshttp.APIError {
	partNum, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || partNum < 1 || partNum > 10000 {
		return awshttp.Errf(400, "InvalidArgument", "partNumber must be 1-10000")
	}
	uploadID := q.Get("uploadId")
	if _, err := s.store.GetUpload(bucket, uploadID); err != nil {
		return awshttp.AsAPIError(err)
	}
	in, aerr := s.ingestBody(r, bucket)
	if aerr != nil {
		return aerr
	}
	if err := s.store.PutPart(bucket, uploadID, s3store.Part{
		Number: partNum, Blob: in.Blob, Size: in.Size,
		ETag: in.MD5Hex, ChecksumVal: in.ChecksumVal,
	}); err != nil {
		s.store.DeleteBlob(in.Blob)
		return awshttp.AsAPIError(err)
	}
	w.Header().Set("ETag", quoteETag(in.MD5Hex))
	if in.ChecksumAlg != "" {
		w.Header().Set("x-amz-checksum-"+strings.ToLower(in.ChecksumAlg), in.ChecksumVal)
	}
	w.WriteHeader(200)
	return nil
}

func (s *Server) uploadPartCopy(w http.ResponseWriter, r *http.Request, bucket, key string, q url.Values) *awshttp.APIError {
	partNum, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || partNum < 1 {
		return awshttp.Errf(400, "InvalidArgument", "partNumber must be positive")
	}
	uploadID := q.Get("uploadId")
	if _, err := s.store.GetUpload(bucket, uploadID); err != nil {
		return awshttp.AsAPIError(err)
	}
	srcBucket, srcKey, srcVersion, aerr := parseCopySource(r.Header.Get("x-amz-copy-source"))
	if aerr != nil {
		return aerr
	}
	src, err := s.store.GetVersion(srcBucket, srcKey, srcVersion)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	f, ferr := s.store.OpenBlob(src.Blob)
	if ferr != nil {
		return awshttp.Errf(500, "InternalError", "open source blob: %v", ferr)
	}
	defer f.Close()

	// Optional x-amz-copy-source-range: bytes=start-end.
	var reader = newSection(f, 0, src.Size)
	if rng := r.Header.Get("x-amz-copy-source-range"); rng != "" {
		start, length, isRange, aerr := parseRange(rng, src.Size)
		if aerr != nil {
			return aerr
		}
		if isRange {
			reader = newSection(f, start, length)
		}
	}
	in, ingestErr := s.ingestFromReader(bucket, reader)
	if ingestErr != nil {
		return ingestErr
	}
	if err := s.store.PutPart(bucket, uploadID, s3store.Part{
		Number: partNum, Blob: in.Blob, Size: in.Size, ETag: in.MD5Hex,
	}); err != nil {
		s.store.DeleteBlob(in.Blob)
		return awshttp.AsAPIError(err)
	}
	type result struct {
		XMLName      xml.Name `xml:"CopyPartResult"`
		XMLNS        string   `xml:"xmlns,attr"`
		ETag         string   `xml:"ETag"`
		LastModified string   `xml:"LastModified"`
	}
	writeXML(w, 200, result{XMLNS: s3NS, ETag: quoteETag(in.MD5Hex), LastModified: iso8601(s.now().Unix())})
	return nil
}

func (s *Server) completeMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) *awshttp.APIError {
	var req struct {
		Parts []struct {
			PartNumber     int    `xml:"PartNumber"`
			ETag           string `xml:"ETag"`
			ChecksumCRC32  string `xml:"ChecksumCRC32"`
			ChecksumCRC32C string `xml:"ChecksumCRC32C"`
			ChecksumCRC64  string `xml:"ChecksumCRC64NVME"`
			ChecksumSHA1   string `xml:"ChecksumSHA1"`
			ChecksumSHA256 string `xml:"ChecksumSHA256"`
		} `xml:"Part"`
	}
	if aerr := readBodyXML(r, &req); aerr != nil {
		return aerr
	}
	var declared []s3store.CompletedPart
	for _, p := range req.Parts {
		sum := p.ChecksumCRC32 + p.ChecksumCRC32C + p.ChecksumCRC64 + p.ChecksumSHA1 + p.ChecksumSHA256
		declared = append(declared, s3store.CompletedPart{Number: p.PartNumber, ETag: p.ETag, ChecksumVal: sum})
	}
	v, err := s.store.CompleteUpload(bucket, uploadID, declared, r.Header.Get("If-None-Match"), r.Header.Get("If-Match"))
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type result struct {
		XMLName        xml.Name `xml:"CompleteMultipartUploadResult"`
		XMLNS          string   `xml:"xmlns,attr"`
		Location       string   `xml:"Location"`
		Bucket         string   `xml:"Bucket"`
		Key            string   `xml:"Key"`
		ETag           string   `xml:"ETag"`
		ChecksumCRC32  string   `xml:"ChecksumCRC32,omitempty"`
		ChecksumCRC32C string   `xml:"ChecksumCRC32C,omitempty"`
		ChecksumCRC64  string   `xml:"ChecksumCRC64NVME,omitempty"`
		ChecksumSHA1   string   `xml:"ChecksumSHA1,omitempty"`
		ChecksumSHA256 string   `xml:"ChecksumSHA256,omitempty"`
		ChecksumType   string   `xml:"ChecksumType,omitempty"`
	}
	res := result{
		XMLNS:    s3NS,
		Location: "http://" + r.Host + "/" + bucket + "/" + key,
		Bucket:   bucket, Key: key, ETag: quoteETag(v.ETag),
		ChecksumType: v.ChecksumType,
	}
	switch v.ChecksumAlg {
	case "CRC32":
		res.ChecksumCRC32 = v.ChecksumVal
	case "CRC32C":
		res.ChecksumCRC32C = v.ChecksumVal
	case "CRC64NVME":
		res.ChecksumCRC64 = v.ChecksumVal
	case "SHA1":
		res.ChecksumSHA1 = v.ChecksumVal
	case "SHA256":
		res.ChecksumSHA256 = v.ChecksumVal
	}
	if v.VersionID != "null" {
		w.Header().Set("x-amz-version-id", v.VersionID)
	}
	writeXML(w, 200, res)
	return nil
}

func (s *Server) abortMultipartUpload(w http.ResponseWriter, bucket, key, uploadID string) *awshttp.APIError {
	if err := s.store.AbortUpload(bucket, uploadID); err != nil {
		return awshttp.AsAPIError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listParts(w http.ResponseWriter, bucket, key, uploadID string) *awshttp.APIError {
	parts, err := s.store.Parts(bucket, uploadID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type partEl struct {
		PartNumber   int    `xml:"PartNumber"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	}
	type result struct {
		XMLName  xml.Name `xml:"ListPartsResult"`
		XMLNS    string   `xml:"xmlns,attr"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
		Owner    owner    `xml:"Owner"`
		Parts    []partEl `xml:"Part"`
	}
	res := result{XMLNS: s3NS, Bucket: bucket, Key: key, UploadID: uploadID, Owner: localOwner(), Parts: []partEl{}}
	for _, p := range parts {
		res.Parts = append(res.Parts, partEl{
			PartNumber: p.Number, LastModified: iso8601(p.LastModified),
			ETag: quoteETag(p.ETag), Size: p.Size,
		})
	}
	writeXML(w, 200, res)
	return nil
}

func (s *Server) listMultipartUploads(w http.ResponseWriter, bucket string, q url.Values) *awshttp.APIError {
	ups, err := s.store.Uploads(bucket, q.Get("prefix"))
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	type uploadEl struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
		Owner     owner  `xml:"Owner"`
	}
	type result struct {
		XMLName xml.Name   `xml:"ListMultipartUploadsResult"`
		XMLNS   string     `xml:"xmlns,attr"`
		Bucket  string     `xml:"Bucket"`
		Uploads []uploadEl `xml:"Upload"`
	}
	res := result{XMLNS: s3NS, Bucket: bucket, Uploads: []uploadEl{}}
	for _, u := range ups {
		res.Uploads = append(res.Uploads, uploadEl{
			Key: u.Key, UploadID: u.ID, Initiated: iso8601(u.Initiated), Owner: localOwner(),
		})
	}
	writeXML(w, 200, res)
	return nil
}
