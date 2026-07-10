package s3store

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
)

// multipartETag computes the "<md5-of-concatenated-part-md5s>-<N>" form.
func multipartETag(partMD5s [][]byte) string {
	h := md5.New()
	for _, m := range partMD5s {
		h.Write(m)
	}
	return fmt.Sprintf("%x-%d", h.Sum(nil), len(partMD5s))
}

func hexDecode(s string) ([]byte, error) {
	// Multipart part ETags are plain hex md5 (no -N suffix at the part level).
	return hex.DecodeString(s)
}

func copyAll(h hash.Hash, r io.Reader) {
	_, _ = io.Copy(h, r)
}

// hexSum is a test-support helper: hex md5 of a string.
func hexSum(s string) (string, error) {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil)), nil
}
