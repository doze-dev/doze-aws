package modelcheck

import (
	"encoding/base64"
	"strings"
	"testing"
)

// A blob's length is of its decoded bytes. KMS's Plaintext may be 4096 bytes,
// which is 5464 base64 characters; measured as text it was refused above 3072.
func TestBlobLengthIsMeasuredInBytes(t *testing.T) {
	table := []Constraint{{Path: "Plaintext", Kind: KindLength, Blob: true, Min: 1, Max: 4096}}
	plain := []Constraint{{Path: "Plaintext", Kind: KindLength, Min: 1, Max: 4096}}
	enc := func(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }

	for _, tc := range []struct {
		bytes  int
		refuse bool
	}{
		{1, false}, {3072, false}, {3073, false}, {4095, false}, {4096, false}, {4097, true}, {6000, true},
	} {
		err := ValidateMap(map[string]any{"Plaintext": enc(tc.bytes)}, table)
		if (err != nil) != tc.refuse {
			t.Errorf("%d bytes (%d characters): refused=%v, want %v (%v)", tc.bytes, len(enc(tc.bytes)), err != nil, tc.refuse, err)
		}
	}

	// The same value in a table that does not say Blob is still measured as
	// text, which is right for a member that is a string.
	if ValidateMap(map[string]any{"Plaintext": enc(3073)}, plain) == nil {
		t.Error("a non-blob member of 4,100 characters should exceed 4096")
	}

	// The minimum is in bytes too: the empty string is the only empty blob.
	if ValidateMap(map[string]any{"Plaintext": ""}, table) == nil {
		t.Error("an empty blob should be shorter than the minimum of 1")
	}
	if err := ValidateMap(map[string]any{"Plaintext": "AA=="}, table); err != nil {
		t.Errorf("one byte of padded base64 was refused: %v", err)
	}
	// Unpadded input, which some clients send, counts the same.
	if err := ValidateMap(map[string]any{"Plaintext": strings.TrimRight(enc(4096), "=")}, table); err != nil {
		t.Errorf("4096 unpadded bytes were refused: %v", err)
	}
}
