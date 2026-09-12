package s3_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/doze-dev/doze-aws/s3"
)

// Removing --s3-host is the one change in the addressing cleanup that can fail
// QUIETLY, so it does not get to.
//
// Its default was "localhost", and *.localhost really does resolve to loopback
// on macOS and on Linux under systemd-resolved — measured, not assumed. So an
// SDK pointed at http://localhost:4566 with the default UsePathStyle:false
// addressed buckets virtual-hosted and worked. Afterwards the same request is
// read path-style: GET photos.localhost/receipts/jan.pdf becomes bucket
// "receipts", key "jan.pdf" — a NoSuchBucket for a bucket nobody named, or a
// successful read from the wrong one.
//
// The behaviour change is deliberate. Being silent about it would not be.
func TestALostVHostRequestSaysSo(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}

	srv, err := s3.New(s3.Options{DataDir: t.TempDir(), Suffix: "aws.harbour.doze", Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	get := func(host string) {
		req, _ := http.NewRequest("GET", ts.URL+"/some/key", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	get("photos.localhost")
	mu.Lock()
	first := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(first, "path-style") {
		t.Fatalf("a lost vhost request was not reported:\n%s", first)
	}
	// The message has to carry both ways out, or it is a complaint rather than
	// a remedy.
	for _, want := range []string{"UsePathStyle", "--suffix", "s3."} {
		if !strings.Contains(first, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, first)
		}
	}

	// Once per shape. A client addresses EVERY request this way, and a line per
	// request is a line nobody reads.
	before := count(lines, "path-style")
	get("photos.localhost")
	get("receipts.localhost")
	mu.Lock()
	after := count(lines, "path-style")
	all := strings.Join(lines, "\n")
	mu.Unlock()
	if after != before {
		t.Errorf("the warning repeated for the same base host (%d -> %d):\n%s", before, after, all)
	}

	// And it stays quiet for hosts that are NOT a lost vhost request: the
	// instance's own shapes, and a plain address.
	for _, host := range []string{
		"aws.harbour.doze",
		"photos.s3.us-east-1.aws.harbour.doze",
		"s3.us-east-1.aws.harbour.doze",
		"127.0.0.1:4566",
		"localhost:4566",
	} {
		mu.Lock()
		lines = nil
		mu.Unlock()
		get(host)
		mu.Lock()
		got := strings.Join(lines, "\n")
		mu.Unlock()
		if strings.Contains(got, "path-style") {
			t.Errorf("warned about %q, which is not a lost vhost request:\n%s", host, got)
		}
	}
}

func count(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}
