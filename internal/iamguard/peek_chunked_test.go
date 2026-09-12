package iamguard

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A body the middleware only PEEKS at must reach the handler whole.
//
// peekBody's guard is on ContentLength, and a chunked request has none —
// net/http reports -1. So `r.ContentLength > maxPeek` was false, the body was
// read to maxPeek, and then r.Body was REPLACED by exactly those bytes. The
// remainder was dropped on the floor.
//
// IAM mode is soft by DEFAULT, so this middleware is always on. A perfectly
// well-formed 4 MB BatchWriteItem sent with Transfer-Encoding: chunked reached
// its handler as 1 MiB of truncated JSON and came back as
// SerializationException — for a request that was never malformed.
//
// S3 and Lambda were spared only because resolve branches to resolveS3 and
// resolveLambda before any peek, which is why the obvious large-payload path
// never showed it.
func TestAnOversizedChunkedBodyIsNotTruncated(t *testing.T) {
	// Distinctive at both ends, so a truncation is unmistakable.
	payload := append(bytes.Repeat([]byte("a"), maxPeek+4096), []byte("TAIL")...)

	r, _ := http.NewRequest("POST", "/", io.NopCloser(bytes.NewReader(payload)))
	// What net/http hands a handler for a chunked request: unknown length.
	r.ContentLength = -1

	body, ok := peekBody(r)
	if ok || body != nil {
		t.Errorf("a body over maxPeek must not be peeked at: ok=%v len=%d", ok, len(body))
	}

	// The handler's view: every byte, in order.
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("handler saw %d bytes, want %d — truncated by %d", len(got), len(payload), len(payload)-len(got))
	}
	if !bytes.Equal(got, payload) {
		t.Error("handler saw different bytes than were sent")
	}
	if !bytes.HasSuffix(got, []byte("TAIL")) {
		t.Error("the end of the body did not survive")
	}
}

// The ordinary case still works: a small body is peeked at AND replayed.
func TestASmallBodyIsPeekedAndReplayed(t *testing.T) {
	const want = `{"TableName":"orders"}`
	r, _ := http.NewRequest("POST", "/", io.NopCloser(strings.NewReader(want)))
	r.ContentLength = int64(len(want))

	body, ok := peekBody(r)
	if !ok || string(body) != want {
		t.Fatalf("peek = %q, %v", body, ok)
	}
	got, _ := io.ReadAll(r.Body)
	if string(got) != want {
		t.Errorf("handler saw %q, want %q", got, want)
	}
}

// A body exactly AT the limit is still peekable — the extra byte read is only
// how "too big" is detected, not a reduction of the limit.
func TestABodyExactlyAtTheLimitIsStillPeeked(t *testing.T) {
	payload := bytes.Repeat([]byte("b"), maxPeek)
	r, _ := http.NewRequest("POST", "/", io.NopCloser(bytes.NewReader(payload)))
	r.ContentLength = -1

	body, ok := peekBody(r)
	if !ok || len(body) != maxPeek {
		t.Fatalf("a body of exactly maxPeek should peek: ok=%v len=%d", ok, len(body))
	}
	got, _ := io.ReadAll(r.Body)
	if len(got) != maxPeek {
		t.Errorf("handler saw %d bytes, want %d", len(got), maxPeek)
	}
}

// A declared Content-Length over the limit is skipped before anything is read,
// which is the path that always worked and must keep working.
func TestADeclaredOversizeBodyIsLeftUntouched(t *testing.T) {
	payload := bytes.Repeat([]byte("c"), 64)
	r, _ := http.NewRequest("POST", "/", io.NopCloser(bytes.NewReader(payload)))
	r.ContentLength = maxPeek + 1

	if body, ok := peekBody(r); ok || body != nil {
		t.Errorf("a declared-oversize body must not be peeked: ok=%v", ok)
	}
	got, _ := io.ReadAll(r.Body)
	if len(got) != len(payload) {
		t.Errorf("handler saw %d bytes, want %d", len(got), len(payload))
	}
}
