package awschunk

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsChunked(t *testing.T) {
	r := httptest.NewRequest("PUT", "/b/k", nil)
	if IsChunked(r) {
		t.Error("plain request detected as chunked")
	}
	r.Header.Set("Content-Encoding", "aws-chunked")
	if !IsChunked(r) {
		t.Error("Content-Encoding aws-chunked not detected")
	}

	r = httptest.NewRequest("PUT", "/b/k", nil)
	r.Header.Set("X-Amz-Content-Sha256", "STREAMING-UNSIGNED-PAYLOAD-TRAILER")
	if !IsChunked(r) {
		t.Error("STREAMING sha256 not detected")
	}
}

func TestDecodeUnsignedWithTrailer(t *testing.T) {
	// The exact shape aws-sdk-go-v2 produces for a small PutObject with the
	// default CRC32 checksum over plain HTTP.
	body := "d\r\nHello, world!\r\n" +
		"0\r\n" +
		"x-amz-checksum-crc32: 6taSKQ==\r\n" +
		"\r\n"
	r := NewReader(strings.NewReader(body))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Hello, world!" {
		t.Errorf("payload = %q", got)
	}
	if tr := r.Trailers().Get("x-amz-checksum-crc32"); tr != "6taSKQ==" {
		t.Errorf("trailer = %q", tr)
	}
}

func TestDecodeSignedChunks(t *testing.T) {
	// Signed chunks: signature parameters are parsed and discarded.
	body := "5;chunk-signature=deadbeef\r\nhello\r\n" +
		"6;chunk-signature=deadbeef\r\n world\r\n" +
		"0;chunk-signature=deadbeef\r\n" +
		"\r\n"
	r := NewReader(strings.NewReader(body))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Errorf("payload = %q", got)
	}
}

func TestDecodeSignedWithTrailerSignature(t *testing.T) {
	body := "3;chunk-signature=aa\r\nabc\r\n" +
		"0;chunk-signature=aa\r\n" +
		"x-amz-checksum-sha256: qwe=\r\n" +
		"x-amz-trailer-signature: bb\r\n" +
		"\r\n"
	r := NewReader(strings.NewReader(body))
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "abc" {
		t.Fatalf("payload = %q, err = %v", got, err)
	}
	if r.Trailers().Get("x-amz-checksum-sha256") != "qwe=" {
		t.Errorf("trailers = %v", r.Trailers())
	}
	if r.Trailers().Get("x-amz-trailer-signature") != "" {
		t.Error("trailer signature leaked into trailers")
	}
}

func TestDecodeEmptyPayload(t *testing.T) {
	// Zero-byte object: just the terminal chunk.
	r := NewReader(strings.NewReader("0\r\n\r\n"))
	got, err := io.ReadAll(r)
	if err != nil || len(got) != 0 {
		t.Fatalf("payload = %q, err = %v", got, err)
	}
}

func TestDecodeLargeMultiChunk(t *testing.T) {
	// Two 64 KiB chunks — exercises partial reads across chunk boundaries.
	chunk := strings.Repeat("x", 64<<10)
	body := "10000\r\n" + chunk + "\r\n10000\r\n" + chunk + "\r\n0\r\n\r\n"
	r := NewReader(strings.NewReader(body))
	got, err := io.ReadAll(r)
	if err != nil || len(got) != 128<<10 {
		t.Fatalf("len = %d, err = %v", len(got), err)
	}
}

func TestDecodeMalformed(t *testing.T) {
	cases := map[string]string{
		"garbage size":       "zz\r\nhello\r\n0\r\n\r\n",
		"negative size":      "-5\r\nhello\r\n0\r\n\r\n",
		"truncated payload":  "a\r\nhi",
		"missing terminator": "5\r\nhelloXX0\r\n\r\n",
		"empty size line":    ";chunk-signature=x\r\n0\r\n\r\n",
		"huge chunk":         "fffffffff\r\n\r\n",
		"bad trailer line":   "1\r\nx\r\n0\r\nnot-a-header\r\n\r\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := io.ReadAll(NewReader(strings.NewReader(body)))
			if err == nil {
				t.Errorf("malformed stream decoded without error")
			}
		})
	}
}

// FuzzDecode asserts the decoder never panics and never claims more payload
// than the input could contain.
func FuzzDecode(f *testing.F) {
	f.Add([]byte("d\r\nHello, world!\r\n0\r\nx-amz-checksum-crc32: 6taSKQ==\r\n\r\n"))
	f.Add([]byte("5;chunk-signature=x\r\nhello\r\n0\r\n\r\n"))
	f.Add([]byte("0\r\n\r\n"))
	f.Add([]byte("zz\r\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(strings.NewReader(string(data)))
		got, _ := io.ReadAll(io.LimitReader(r, 1<<20))
		if len(got) > len(data) {
			t.Errorf("decoded %d bytes from %d input bytes", len(got), len(data))
		}
		r.Trailers()
	})
}
