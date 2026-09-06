package lambdaruntime

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Function output, attributed.
//
// A child's stdout and stderr are one byte stream; what a person wants is
// lines, each tied to the invocation that printed it. The Runner knows which
// invocation is in flight (it handed the work over in handleNext), so the
// splitter stamps every line with that request id on its way to two places:
// the ring buffer the Tail header is cut from, and the LogSink the lambda
// package hangs on the Spec, which is how lines reach the logs service and
// the terminal.
//
// Attribution follows AWS's rule: output belongs to the request whose work
// the function last fetched, until it fetches the next one. Output before
// the first fetch is init output and carries no request id.

// LogSink receives every line a function's process writes.
type LogSink interface {
	// Line is one line of output, without its newline. requestID is "" for
	// init output. stream is the log stream the Runner minted for its process.
	Line(stream, requestID string, at time.Time, line []byte)
	// Flush says an invocation just finished — its END and REPORT lines are
	// written — and is the moment a sink ships what it has, so that "Invoke
	// returned" implies "the lines are queryable".
	Flush()
}

// maxLineBytes caps one event the way CloudWatch does (256 KiB); a longer
// line is split, never dropped.
const maxLineBytes = 256 << 10

// lineSplitter is the io.Writer both stdout and stderr of the child point
// at — the same value for both, which is what makes os/exec serialise them.
type lineSplitter struct {
	mu      sync.Mutex
	ring    *ringBuffer
	sink    LogSink
	stream  string
	current func() string // the in-flight request id, "" during init
	partial []byte
}

func newLineSplitter(ring *ringBuffer, sink LogSink, stream string, current func() string) *lineSplitter {
	return &lineSplitter{ring: ring, sink: sink, stream: stream, current: current}
}

// Write splits on newlines; a trailing partial line waits for its end.
func (s *lineSplitter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.ring.Write(p)
	buf := p
	if len(s.partial) > 0 {
		buf = append(s.partial, p...)
		s.partial = nil
	}
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		s.emit(buf[:i])
		buf = buf[i+1:]
	}
	if len(buf) >= maxLineBytes {
		s.emit(buf)
		buf = nil
	}
	if len(buf) > 0 {
		s.partial = append([]byte(nil), buf...)
	}
	return len(p), nil
}

// Line writes a line the Runner itself produces — START, END, REPORT — to
// both destinations, attributed to the request it names.
func (s *lineSplitter) Line(requestID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.ring.Write([]byte(text + "\n"))
	if s.sink != nil {
		s.sink.Line(s.stream, requestID, time.Now(), []byte(text))
	}
}

// flushPartial pushes out a last line with no newline — what a crashing
// process leaves behind.
func (s *lineSplitter) flushPartial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 {
		s.emit(s.partial)
		s.partial = nil
	}
}

// emit runs under s.mu. Carriage returns from Windows-minded loggers are
// trimmed; the request id is read at emit time, which is the attribution.
func (s *lineSplitter) emit(line []byte) {
	if s.sink == nil {
		return
	}
	line = bytes.TrimRight(line, "\r")
	for len(line) > maxLineBytes {
		s.sink.Line(s.stream, s.current(), time.Now(), line[:maxLineBytes])
		line = line[maxLineBytes:]
	}
	s.sink.Line(s.stream, s.current(), time.Now(), line)
}

// streamName mints a log stream name the way Lambda does:
// 2026/09/07/[$LATEST]0123456789abcdef0123456789abcdef — one per execution
// environment, which locally is one per Runner process.
func streamName(version string, now time.Time) string {
	if version == "" {
		version = "$LATEST"
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("2006/01/02") + "/[" + version + "]" + hex.EncodeToString(b[:])
}
