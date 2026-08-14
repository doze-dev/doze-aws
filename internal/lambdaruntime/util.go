package lambdaruntime

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// ringBuffer is a fixed-size, concurrency-safe tail of process output.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{size: size}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.size {
		r.buf = r.buf[len(r.buf)-r.size:]
	}
	return len(p), nil
}

func (r *ringBuffer) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...)
}

// newID returns a request ID in the shape AWS uses: a v4 UUID, 8-4-4-4-12.
//
// The previous version was not one. It took 8 bytes for the first group
// instead of 4, and padded the last group by re-encoding b[:6] — so every ID
// repeated its own opening bytes at the end ("1448d770410c…-…-1448d770410c")
// and ran 60 characters instead of 36. Anything parsing X-Amzn-RequestId as a
// UUID rejected it, and the repetition made two IDs look related when they
// were not.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; a zero ID is still better
		// than a panic inside an invocation.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
