package gateway

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// peekAction reads a Query request's Action out of a form body and puts the
// body back for the service. Its three edges: only a form is read, a body of
// exactly the cap is read, and a body over it is neither read nor cut.
func TestPeekActionReadsOnlyAForm(t *testing.T) {
	for _, tc := range []struct {
		what, ct, want string
	}{
		{"a form", "application/x-www-form-urlencoded", "ListTopics"},
		{"a form with a charset", "application/x-www-form-urlencoded; charset=utf-8", "ListTopics"},
		{"no content type at all", "", "ListTopics"},
		{"plain text", "text/plain", ""},
		{"JSON", "application/x-amz-json-1.0", ""},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader("Action=ListTopics&Version=2010-03-31"))
		if tc.ct != "" {
			r.Header.Set("Content-Type", tc.ct)
		}
		if got, _ := peekAction(r); got != tc.want {
			t.Errorf("%s: Action %q, want %q", tc.what, got, tc.want)
		}
		// Whatever it decided, the body is still there for the service.
		if b, _ := io.ReadAll(r.Body); string(b) != "Action=ListTopics&Version=2010-03-31" {
			t.Errorf("%s: the service would read %q", tc.what, b)
		}
	}
}

func TestPeekActionAtTheBodyCap(t *testing.T) {
	const maxPeek = 10 << 20
	form := func(size int) []byte {
		head := "Action=ListTopics&pad="
		return append([]byte(head), bytes.Repeat([]byte("a"), size-len(head))...)
	}

	// Exactly the cap: still a form doze-aws can route, and still whole.
	at := form(maxPeek)
	r := httptest.NewRequest("POST", "/", bytes.NewReader(at))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got, _ := peekAction(r); got != "ListTopics" {
		t.Errorf("a body of exactly %d bytes: Action %q, want ListTopics", maxPeek, got)
	}
	if b, _ := io.ReadAll(r.Body); !bytes.Equal(b, at) {
		t.Errorf("a body of exactly %d bytes reached the service as %d bytes", maxPeek, len(b))
	}

	// One byte over: not routed by Action, and the service still gets every byte.
	over := form(maxPeek + 1)
	r = httptest.NewRequest("POST", "/", bytes.NewReader(over))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got, _ := peekAction(r); got != "" {
		t.Errorf("a body over the cap was routed by Action %q", got)
	}
	if b, _ := io.ReadAll(r.Body); !bytes.Equal(b, over) {
		t.Errorf("a body of %d bytes reached the service as %d bytes: it was cut", len(over), len(b))
	}
}
