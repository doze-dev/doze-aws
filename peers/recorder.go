package peers

import (
	"bytes"
	"io"
	"net/http"
)

// recorder is a minimal in-memory http.ResponseWriter used by the in-process
// transport. (net/http/httptest has one, but importing a testing-flavored
// package from production code muddies the dependency story.)
type recorder struct {
	status      int
	wroteHeader bool
	header      http.Header
	body        bytes.Buffer
}

func newRecorder() *recorder {
	return &recorder{status: http.StatusOK, header: http.Header{}}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
}

func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

func (r *recorder) result(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode:    r.status,
		Status:        http.StatusText(r.status),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        r.header,
		Body:          io.NopCloser(bytes.NewReader(r.body.Bytes())),
		ContentLength: int64(r.body.Len()),
		Request:       req,
	}
}
