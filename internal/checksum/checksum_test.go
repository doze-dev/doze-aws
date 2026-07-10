package checksum

import (
	"testing"
)

// TestKnownVectors pins each algorithm to an externally-verified digest of
// "123456789" (the standard CRC check string) and "Hello, world!".
func TestKnownVectors(t *testing.T) {
	cases := []struct {
		alg   string
		input string
		want  string // base64
	}{
		// crc32("123456789") = 0xCBF43926
		{"CRC32", "123456789", "y/Q5Jg=="},
		// crc32c("123456789") = 0xE3069283
		{"CRC32C", "123456789", "4waSgw=="},
		// crc64/nvme check value for "123456789" = 0xAE8B14860A799888
		{"CRC64NVME", "123456789", "rosUhgp5mIg="},
		// sha1("Hello, world!")
		{"SHA1", "Hello, world!", "lDpwLQbzRZmu4fjajvn3KWAx1pk="},
		// crc32("Hello, world!") = 0xEBE6C6E6 (zlib.crc32-verified)
		{"CRC32", "Hello, world!", "6+bG5g=="},
	}
	for _, tc := range cases {
		t.Run(tc.alg+"/"+tc.input, func(t *testing.T) {
			h, err := New(tc.alg)
			if err != nil {
				t.Fatal(err)
			}
			h.Write([]byte(tc.input))
			if got := Encode(h.Sum(nil)); got != tc.want {
				t.Errorf("%s(%q) = %s, want %s", tc.alg, tc.input, got, tc.want)
			}
		})
	}
}

func TestSupported(t *testing.T) {
	for _, alg := range []string{"crc32", "CRC32C", "Sha256", "CRC64NVME", "sha1"} {
		if _, ok := Supported(alg); !ok {
			t.Errorf("Supported(%q) = false", alg)
		}
	}
	if _, ok := Supported("MD5"); ok {
		t.Error("MD5 accepted as a flexible checksum")
	}
}

func TestCompositeShape(t *testing.T) {
	h1, _ := New("CRC32")
	h1.Write([]byte("part one"))
	h2, _ := New("CRC32")
	h2.Write([]byte("part two"))
	got, err := Composite("CRC32", [][]byte{h1.Sum(nil), h2.Sum(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || got[len(got)-2:] != "-2" {
		t.Errorf("composite = %q, want -2 suffix", got)
	}
}

func TestIncrementalMatchesOneShot(t *testing.T) {
	for _, alg := range Algorithms {
		a, _ := New(alg)
		a.Write([]byte("hello "))
		a.Write([]byte("world"))
		b, _ := New(alg)
		b.Write([]byte("hello world"))
		if Encode(a.Sum(nil)) != Encode(b.Sum(nil)) {
			t.Errorf("%s: incremental != one-shot", alg)
		}
	}
}
