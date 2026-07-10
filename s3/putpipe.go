package s3

// The PutObject/UploadPart ingest pipeline: decode whatever body framing the
// client chose, hash while streaming to disk, then verify every checksum the
// client declared. This is where the full dual-SDK upload matrix converges:
//
//   - plain body (Content-MD5 optional)                       aws-sdk-go v1, curl
//   - UNSIGNED-PAYLOAD                                        presigned uploads
//   - STREAMING-AWS4-HMAC-SHA256-PAYLOAD                      signed chunks (both SDKs)
//   - STREAMING-*-TRAILER                                     chunked + trailing checksum (v2 default)
//   - x-amz-checksum-<alg> header                             checksum declared up front
//   - x-amz-sdk-checksum-algorithm + trailer                  checksum arrives in the trailer

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awschunk"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/checksum"
)

// ingested is the outcome of streaming one upload body to disk.
type ingested struct {
	Blob        string
	Size        int64
	MD5Hex      string
	ChecksumAlg string // canonical, "" if none requested
	ChecksumVal string // base64 of the computed digest
}

// ingestBody streams the request body into a blob, computing MD5 plus any
// requested flexible checksum, and verifies declared values (Content-MD5,
// header checksum, trailer checksum). On any verification failure the blob is
// removed and an S3-shaped error returned.
func (s *Server) ingestBody(r *http.Request, bucket string) (*ingested, *awshttp.APIError) {
	body := io.Reader(r.Body)
	var chunked *awschunk.Reader
	if awschunk.IsChunked(r) {
		chunked = awschunk.NewReader(r.Body)
		body = chunked
	}

	// Which checksum algorithm is in play, and (if via header) its declared value.
	alg, declared, aerr := declaredChecksum(r.Header)
	if aerr != nil {
		return nil, aerr
	}

	md5h := md5.New()
	tee := io.TeeReader(body, md5h)
	var sumh hash.Hash
	if alg != "" {
		sumh, _ = checksum.New(alg)
		tee = io.TeeReader(tee, sumh)
	}

	blob, size, err := s.store.WriteBlob(bucket, tee)
	if err != nil {
		if chunked != nil {
			return nil, awshttp.Errf(400, "IncompleteBody", "decoding aws-chunked body: %v", err)
		}
		return nil, awshttp.Errf(400, "IncompleteBody", "reading request body: %v", err)
	}
	drop := func(e *awshttp.APIError) (*ingested, *awshttp.APIError) {
		s.store.DeleteBlob(blob)
		return nil, e
	}

	// Trailer-declared checksum (and algorithm, if only the trailer names it).
	if chunked != nil {
		for name, vals := range chunked.Trailers() {
			lower := strings.ToLower(name)
			if rest, ok := strings.CutPrefix(lower, "x-amz-checksum-"); ok && len(vals) > 0 {
				canonical, ok := checksum.Supported(rest)
				if !ok {
					return drop(awshttp.Errf(400, "InvalidRequest", "unsupported trailer checksum algorithm %q", rest))
				}
				if alg != "" && canonical != alg {
					return drop(awshttp.Errf(400, "InvalidRequest", "trailer checksum %s does not match declared algorithm %s", canonical, alg))
				}
				if alg == "" {
					// Algorithm arrived only in the trailer: recompute from disk
					// (single extra local read; simpler than pre-registering all).
					f, err := s.store.OpenBlob(blob)
					if err != nil {
						return drop(awshttp.Errf(500, "InternalError", "reopen blob: %v", err))
					}
					sumh, _ = checksum.New(canonical)
					_, _ = io.Copy(sumh, f)
					f.Close()
					alg = canonical
				}
				declared = vals[0]
			}
		}
	}

	// Verify Content-MD5 when present.
	md5sum := md5h.Sum(nil)
	if cmd5 := r.Header.Get("Content-MD5"); cmd5 != "" {
		want, err := base64.StdEncoding.DecodeString(cmd5)
		if err != nil || len(want) != len(md5sum) {
			return drop(awshttp.Errf(400, "InvalidDigest", "Content-MD5 is not valid base64 MD5"))
		}
		if string(want) != string(md5sum) {
			return drop(awshttp.Errf(400, "BadDigest", "the Content-MD5 you specified did not match what we received"))
		}
	}

	out := &ingested{
		Blob:   blob,
		Size:   size,
		MD5Hex: hex.EncodeToString(md5sum),
	}
	if alg != "" {
		out.ChecksumAlg = alg
		out.ChecksumVal = checksum.Encode(sumh.Sum(nil))
		if declared != "" && declared != out.ChecksumVal {
			return drop(awshttp.Errf(400, "BadDigest",
				"the %s you specified (%s) did not match what we computed (%s)", alg, declared, out.ChecksumVal))
		}
	}
	return out, nil
}

// declaredChecksum inspects request headers for the flexible-checksum
// algorithm and (if header-borne) its value.
func declaredChecksum(h http.Header) (alg, value string, aerr *awshttp.APIError) {
	for name, vals := range h {
		lower := strings.ToLower(name)
		rest, ok := strings.CutPrefix(lower, "x-amz-checksum-")
		if !ok || len(vals) == 0 {
			continue
		}
		if rest == "algorithm" || rest == "mode" || rest == "type" {
			continue // x-amz-checksum-algorithm etc. are not value headers
		}
		canonical, ok := checksum.Supported(rest)
		if !ok {
			return "", "", awshttp.Errf(400, "InvalidRequest", "unsupported checksum algorithm %q", rest)
		}
		return canonical, vals[0], nil
	}
	if sdkAlg := h.Get("x-amz-sdk-checksum-algorithm"); sdkAlg != "" {
		canonical, ok := checksum.Supported(sdkAlg)
		if !ok {
			return "", "", awshttp.Errf(400, "InvalidRequest", "unsupported checksum algorithm %q", sdkAlg)
		}
		return canonical, "", nil // value arrives in the trailer
	}
	// x-amz-trailer names the checksum header that will arrive in the trailer.
	if trailer := h.Get("x-amz-trailer"); trailer != "" {
		if rest, ok := strings.CutPrefix(strings.ToLower(trailer), "x-amz-checksum-"); ok {
			if canonical, ok := checksum.Supported(rest); ok {
				return canonical, "", nil
			}
		}
	}
	return "", "", nil
}

// collectMeta pulls user metadata (x-amz-meta-*) and the standard passthrough
// headers off an upload request.
func collectMeta(h http.Header) (meta, headers map[string]string) {
	for name, vals := range h {
		lower := strings.ToLower(name)
		if rest, ok := strings.CutPrefix(lower, "x-amz-meta-"); ok && len(vals) > 0 {
			if meta == nil {
				meta = map[string]string{}
			}
			meta[rest] = vals[0]
		}
	}
	for _, name := range []string{"Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "Expires"} {
		if v := h.Get(name); v != "" {
			if name == "Content-Encoding" {
				// aws-chunked is transport framing, not object metadata.
				v = strings.TrimPrefix(v, "aws-chunked")
				v = strings.Trim(strings.TrimPrefix(v, ","), " ")
				if v == "" {
					continue
				}
			}
			if headers == nil {
				headers = map[string]string{}
			}
			headers[name] = v
		}
	}
	return meta, headers
}
