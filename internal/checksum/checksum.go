// Package checksum implements the flexible-checksum algorithms S3 accepts:
// CRC32, CRC32C, SHA1, SHA256 from the standard library, and CRC64NVME
// in-house (Go has no stdlib implementation; it is a ~30-line table-driven
// CRC with the NVMe polynomial). Values travel base64-encoded on the wire.
//
// Composite reproduces the multipart "checksum-of-checksums" form the SDKs
// validate: the algorithm applied over the concatenated per-part digests,
// suffixed with "-<parts>".
package checksum

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash"
	"hash/crc32"
	"strings"
)

// Algorithms lists the supported x-amz-checksum-* suffixes in canonical form.
var Algorithms = []string{"CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"}

// Supported reports whether alg (any case) is a supported algorithm, and
// returns its canonical spelling.
func Supported(alg string) (string, bool) {
	up := strings.ToUpper(alg)
	for _, a := range Algorithms {
		if a == up {
			return a, true
		}
	}
	return "", false
}

// New returns a fresh hash for a canonical algorithm name.
func New(alg string) (hash.Hash, error) {
	switch alg {
	case "CRC32":
		return crc32.NewIEEE(), nil
	case "CRC32C":
		return crc32.New(crc32.MakeTable(crc32.Castagnoli)), nil
	case "CRC64NVME":
		return newCRC64NVME(), nil
	case "SHA1":
		return sha1.New(), nil
	case "SHA256":
		return sha256.New(), nil
	}
	return nil, fmt.Errorf("unsupported checksum algorithm %q", alg)
}

// Encode renders a digest the way it travels on the wire.
func Encode(sum []byte) string { return base64.StdEncoding.EncodeToString(sum) }

// Composite computes the multipart composite checksum: alg over the
// concatenated raw per-part digests, base64, suffixed with the part count.
func Composite(alg string, partSums [][]byte) (string, error) {
	h, err := New(alg)
	if err != nil {
		return "", err
	}
	for _, sum := range partSums {
		h.Write(sum)
	}
	return Encode(h.Sum(nil)) + fmt.Sprintf("-%d", len(partSums)), nil
}

// crc64NVME implements CRC-64/NVME (as used by S3's CRC64NVME): polynomial
// 0xad93d23594c935a9 reflected, init and xorout all-ones.
type crc64NVME struct {
	crc uint64
}

// nvmeTable is the reflected lookup table for the NVMe polynomial.
var nvmeTable = func() [256]uint64 {
	// Reflected form of 0xad93d23594c935a9.
	const poly = 0x9a6c9329ac4bc9b5
	var t [256]uint64
	for i := range t {
		crc := uint64(i)
		for range 8 {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ poly
			} else {
				crc >>= 1
			}
		}
		t[i] = crc
	}
	return t
}()

func newCRC64NVME() hash.Hash { return &crc64NVME{crc: ^uint64(0)} }

func (c *crc64NVME) Write(p []byte) (int, error) {
	crc := c.crc
	for _, b := range p {
		crc = nvmeTable[byte(crc)^b] ^ (crc >> 8)
	}
	c.crc = crc
	return len(p), nil
}

func (c *crc64NVME) Sum(b []byte) []byte {
	v := ^c.crc
	return append(b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (c *crc64NVME) Reset()         { c.crc = ^uint64(0) }
func (c *crc64NVME) Size() int      { return 8 }
func (c *crc64NVME) BlockSize() int { return 1 }
