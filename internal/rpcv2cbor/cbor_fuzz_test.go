package rpcv2cbor

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzDecode asserts one thing: the decoder never panics.
//
// Correctness is the table test's job. This is about the other half — bytes
// arriving from anywhere, and a hand-rolled binary parser being the obvious
// place for an index to run off the end of a buffer. Seeded with the real SDK
// body and with the malformed shapes that have historically broken CBOR
// readers: truncated arguments, unterminated containers, mismatched string
// chunks, and a nesting bomb.
func FuzzDecode(f *testing.F) {
	if body, err := os.ReadFile(filepath.Join("testdata", "putmetricdata_cbor.cbor")); err == nil {
		f.Add(body)
	}
	seeds := [][]byte{
		{},
		{0x01},
		{0xa0},
		{0x9f, 0xff},
		{0xbf, 0x61, 'k', 0x01, 0xff},
		{0x7f, 0x62, 'a', 'b', 0xff},
		{0x19, 0x01},             // truncated argument
		{0x63, 'a'},              // truncated text
		{0x9f, 0x01},             // unterminated array
		{0xa1, 0x01, 0x01},       // non-string key
		{0xc1, 0x1a, 0, 0, 0, 0}, // tag 1
		{0xff},                   // bare break
		{0x1c},                   // reserved
		{0x5f, 0x41, 0x01, 0xff}, // indefinite byte string
		{0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // huge argument
		{0x9b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // array claiming a huge count
		{0xbb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // map claiming a huge count
		{0x1f, 0x8b, 0x08, 0x00},                               // gzip magic, truncated stream
	}
	// A nesting bomb, as a seed rather than a special case.
	deep := make([]byte, 0, 512)
	for range 512 {
		deep = append(deep, 0x9f)
	}
	seeds = append(seeds, deep)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		// The contract is "returns, one way or the other". A value with no
		// error must also be self-consistent enough not to trip the caller,
		// which DecodeMap exercises on the same bytes.
		if v, err := Decode(body); err == nil && v != nil {
			_, _ = DecodeMap(body)
		}
	})
}
