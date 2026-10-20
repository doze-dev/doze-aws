package httpevent

import (
	"net/http/httptest"
	"testing"
)

// requestContext.http.sourceIp is the caller's address without the port.
func TestSourceIPDropsThePort(t *testing.T) {
	for remote, want := range map[string]string{
		"203.0.113.9:5555": "203.0.113.9",
		"[::1]:8080":       "[::1]",
		":8080":            "", // a port with no address in front of it is still only a port
		"no-port":          "no-port",
		"":                 "",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		ev := Event(Request{R: r, Path: "/"})
		got := ev["requestContext"].(map[string]any)["http"].(map[string]any)["sourceIp"]
		if got != want {
			t.Errorf("RemoteAddr %q: sourceIp %q, want %q", remote, got, want)
		}
	}
}
