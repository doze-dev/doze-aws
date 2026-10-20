package awschunk

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The two limits are about the largest value allowed, so the tests stand on
// both sides of each: the number itself is fine, one more is not. (A boundary
// that moves by one is a change a person would not notice and a client would.)
func TestAChunkOfExactlyTheLimitIsNotRefusedForItsSize(t *testing.T) {
	// The body ends after the header, so a decoder that accepts the size fails
	// later, reading the payload; one that refuses it fails on the size.
	_, err := io.ReadAll(NewReader(strings.NewReader(fmt.Sprintf("%x\r\n", maxChunkSize))))
	if err == nil || strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("a chunk of %d bytes is at the limit and is allowed: %v", maxChunkSize, err)
	}
	_, err = io.ReadAll(NewReader(strings.NewReader(fmt.Sprintf("%x\r\n", maxChunkSize+1))))
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("a chunk of %d bytes is over the limit and is refused for its size: %v", maxChunkSize+1, err)
	}
}

func TestAHeaderLineOfExactlyTheLimitIsAccepted(t *testing.T) {
	// "<hex>;chunk-signature=<padding>" padded to the line limit, newline included.
	pad := func(total int) string {
		line := "0;chunk-signature="
		return line + strings.Repeat("a", total-len(line)-2) + "\r\n"
	}
	ok := pad(maxHeaderLine)
	if len(ok) != maxHeaderLine {
		t.Fatalf("the fixture is %d bytes, want %d", len(ok), maxHeaderLine)
	}
	if _, err := io.ReadAll(NewReader(strings.NewReader(ok + "\r\n"))); err != nil {
		t.Errorf("a header line of %d bytes is at the limit and is allowed: %v", maxHeaderLine, err)
	}
	_, err := io.ReadAll(NewReader(strings.NewReader(pad(maxHeaderLine+1) + "\r\n")))
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("a header line of %d bytes is over the limit: %v", maxHeaderLine+1, err)
	}
}

// A body that ends in the middle of a header line is truncated, which is not
// the same as a body that ended cleanly: a client whose upload was cut must not
// be told it finished.
func TestABodyCutInsideAHeaderLineIsUnexpectedEOF(t *testing.T) {
	_, err := io.ReadAll(NewReader(strings.NewReader("d;chunk-sig")))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("got %v, want io.ErrUnexpectedEOF", err)
	}
	// And an empty body is a clean end, with nothing started.
	_, err = io.ReadAll(NewReader(strings.NewReader("")))
	if errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("an empty body is not a truncated one: %v", err)
	}
}
