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

func newID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:12]) + "-" + hex.EncodeToString(b[12:14]) + "-" + hex.EncodeToString(b[14:16]) + hex.EncodeToString(b[:6])
}
