// Package awschunk decodes the aws-chunked content encoding S3 clients use
// for streaming uploads. Three x-amz-content-sha256 modes produce it:
//
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD           signed chunks (SigV4 clients, aws-sdk-go v1 & v2)
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER   signed chunks + trailing checksum headers
//	STREAMING-UNSIGNED-PAYLOAD-TRAILER           unsigned chunks + trailers (aws-sdk-go-v2's default for plain HTTP)
//
// The frame format is:
//
//	<hex-size>[;chunk-signature=<sig>]\r\n
//	<size bytes of payload>\r\n
//	...
//	0[;chunk-signature=<sig>]\r\n
//	[trailer-header: value\r\n ...]
//	[x-amz-trailer-signature: sig\r\n]
//	\r\n
//
// Chunk signatures are parsed and discarded (doze-aws never verifies
// signatures); trailers are captured and exposed after EOF so the S3 layer
// can check declared checksums.
package awschunk

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// IsChunked reports whether the request body is aws-chunked, by either signal
// clients use (some send both, some only one).
func IsChunked(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		return true
	}
	return strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-")
}

// maxChunkSize guards against absurd chunk headers (the SDKs use ≤1 MiB).
const maxChunkSize = 64 << 20

// maxHeaderLine bounds a chunk-header or trailer line.
const maxHeaderLine = 16 << 10

// Reader streams the decoded payload of an aws-chunked body.
type Reader struct {
	br       *bufio.Reader
	remain   int64 // bytes left in the current chunk
	started  bool
	finished bool
	trailers http.Header
	err      error
}

// NewReader wraps an aws-chunked stream.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r), trailers: http.Header{}}
}

// Read yields decoded payload bytes. After io.EOF, Trailers returns any
// trailing headers.
func (r *Reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	for r.remain == 0 {
		if r.finished {
			return 0, io.EOF
		}
		if err := r.nextChunk(); err != nil {
			r.err = err
			return 0, err
		}
		if r.finished {
			return 0, io.EOF
		}
	}
	if int64(len(p)) > r.remain {
		p = p[:r.remain]
	}
	n, err := io.ReadFull(r.br, p)
	r.remain -= int64(n)
	if err != nil {
		r.err = fmt.Errorf("aws-chunked: truncated chunk payload: %w", err)
		return n, r.err
	}
	if r.remain == 0 {
		// Consume the CRLF that terminates the chunk payload.
		if err := r.expectCRLF(); err != nil {
			r.err = err
			return n, err
		}
	}
	return n, nil
}

// nextChunk parses a chunk header; on the terminal 0-chunk it consumes the
// trailer section and marks the stream finished.
func (r *Reader) nextChunk() error {
	// Tolerate a blank line between chunks (some clients emit one).
	line, err := r.readLine()
	if err != nil {
		return fmt.Errorf("aws-chunked: reading chunk header: %w", err)
	}
	if line == "" && !r.started {
		line, err = r.readLine()
		if err != nil {
			return fmt.Errorf("aws-chunked: reading chunk header: %w", err)
		}
	}
	r.started = true

	sizeHex, _, _ := strings.Cut(line, ";") // discard chunk-signature etc.
	sizeHex = strings.TrimSpace(sizeHex)
	if sizeHex == "" {
		return errors.New("aws-chunked: empty chunk-size line")
	}
	size, err := strconv.ParseInt(sizeHex, 16, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("aws-chunked: malformed chunk size %q", sizeHex)
	}
	if size > maxChunkSize {
		return fmt.Errorf("aws-chunked: chunk size %d exceeds limit", size)
	}
	if size == 0 {
		r.finished = true
		return r.readTrailers()
	}
	r.remain = size
	return nil
}

// readTrailers consumes trailer lines after the terminal chunk.
func (r *Reader) readTrailers() error {
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil // trailing CRLF after the last trailer is optional in the wild
			}
			return fmt.Errorf("aws-chunked: reading trailers: %w", err)
		}
		if line == "" {
			return nil
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("aws-chunked: malformed trailer line %q", line)
		}
		name = strings.TrimSpace(name)
		if strings.EqualFold(name, "x-amz-trailer-signature") {
			continue // parsed, never verified
		}
		r.trailers.Add(name, strings.TrimSpace(value))
	}
}

// Trailers returns the captured trailing headers. Valid after Read has
// returned io.EOF.
func (r *Reader) Trailers() http.Header { return r.trailers }

// readLine reads one CRLF- (or LF-) terminated line, bounded.
func (r *Reader) readLine() (string, error) {
	line, err := r.br.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return "", io.ErrUnexpectedEOF
		}
		return "", err
	}
	if len(line) > maxHeaderLine {
		return "", errors.New("aws-chunked: header line too long")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (r *Reader) expectCRLF() error {
	b := make([]byte, 2)
	if _, err := io.ReadFull(r.br, b); err != nil {
		return fmt.Errorf("aws-chunked: missing chunk terminator: %w", err)
	}
	if b[0] != '\r' || b[1] != '\n' {
		return fmt.Errorf("aws-chunked: bad chunk terminator %q", b)
	}
	return nil
}
