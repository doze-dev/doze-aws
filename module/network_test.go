package aws

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/enginetest"

	"github.com/doze-dev/doze-aws/module/awslocal"
)

// The whole local AWS answers at its instance's name and everything under it
// (sqs.<region>.<name>, <bucket>.s3.<region>.<name>), and at sync-<name>, where
// every SDK sends Step Functions' StartSyncExecution and TestState.
func TestTheAWSNetwork(t *testing.T) {
	d := New()
	inst := engine.Instance{Name: "aws", Type: "aws"}
	enginetest.CheckNetwork(t, d, inst)
	n := d.Network(inst)
	if len(n.Listeners) != 1 {
		t.Fatalf("listeners = %+v", n.Listeners)
	}
	l := n.Listeners[0]
	if l.Name != engine.MainListener || l.Protocol != engine.ProtocolHTTP || !slices.Equal(l.Routes, []string{"@", "sync-@"}) {
		t.Errorf("main = %+v", l)
	}
}

// With a name, the SDK endpoint is the name without a port; without one, the
// address, as before.
func TestTheEndpointComesFromTheGrant(t *testing.T) {
	d := New()
	named := engine.Instance{Grant: engine.Grant{Host: "aws.shop.doze", Listeners: map[string]engine.GrantedListener{
		engine.MainListener: {Addr: "aws.shop.doze:4566", URL: "http://aws.shop.doze", Hosts: []string{"aws.shop.doze", "sync-aws.shop.doze"}}}}}
	if v, u := d.ConnString(named, engine.Endpoint{TCPAddr: "aws.shop.doze:4566"}); v != "AWS_ENDPOINT_URL" || u != "http://aws.shop.doze" {
		t.Errorf("named = %s %s", v, u)
	}
	if _, u := d.ConnString(engine.Instance{}, engine.Endpoint{TCPAddr: "127.0.0.1:4566"}); u != "http://127.0.0.1:4566" {
		t.Errorf("unnamed = %s", u)
	}
}

// The backend is told its name, so the stack recognises and mints hostnames
// under it: an object written by virtual host lands in its bucket.
func TestTheStackAnswersUnderItsName(t *testing.T) {
	h, closer, err := serveFactory(awslocal.ServeOptions{DataDir: t.TempDir(), Endpoint: "http://aws.shop.doze", Suffix: "aws.shop.doze"})
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	do := func(method, host, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+path, strings.NewReader(body))
		req.Host = host
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=doze/20260101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=0")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if r := do(http.MethodPut, "aws.shop.doze", "/uploads", ""); r.Code != http.StatusOK {
		t.Fatalf("create bucket: %d %s", r.Code, r.Body)
	}
	if r := do(http.MethodPut, "uploads.s3.us-east-1.aws.shop.doze", "/hello.txt", "hi"); r.Code != http.StatusOK {
		t.Fatalf("put by virtual host: %d %s", r.Code, r.Body)
	}
	r := do(http.MethodGet, "aws.shop.doze", "/uploads/hello.txt", "")
	if b, _ := io.ReadAll(r.Body); r.Code != http.StatusOK || string(b) != "hi" {
		t.Fatalf("get by path: %d %q: the virtual-host write did not land in the bucket", r.Code, b)
	}
}
