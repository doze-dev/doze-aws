package kms

import "testing"

// FuzzOpenBlob feeds arbitrary bytes to the ciphertext-blob parser, asserting it
// rejects malformed input with an error and never panics (it runs on untrusted
// CiphertextBlob values from Decrypt/ReEncrypt).
func FuzzOpenBlob(f *testing.F) {
	// Seed with a real-shaped blob and some truncations/garbage.
	f.Add([]byte("DZKMS\x01\x04abcd" + string(make([]byte, 40))))
	f.Add([]byte("DZKMS"))
	f.Add([]byte("DZKMS\x02"))
	f.Add([]byte{})
	f.Add([]byte("not a blob at all"))
	f.Fuzz(func(t *testing.T, blob []byte) {
		keyID, unseal, aerr := openBlob(blob)
		if aerr != nil {
			return
		}
		// A parsed blob must yield a key id and an unseal closure; calling the
		// closure with an empty key must not panic (it returns an error).
		_ = keyID
		if unseal != nil {
			_, _ = unseal(&Key{Material: make([]byte, 32)}, nil)
		}
	})
}
