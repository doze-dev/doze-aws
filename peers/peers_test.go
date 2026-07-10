package peers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoneNeverResolves(t *testing.T) {
	if _, ok := None().Endpoint("sqs"); ok {
		t.Fatal("None resolved a service")
	}
}

func TestInProcessDispatchesToHandler(t *testing.T) {
	hit := ""
	dir := InProcess(func(service string) http.Handler {
		if service != "sqs" {
			return nil
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = r.URL.Path
			io.WriteString(w, "ok")
		})
	})
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		t.Fatal("sqs not resolved")
	}
	resp, err := ep.Client.Post(ep.URL("/create"), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if hit != "/create" || string(b) != "ok" {
		t.Fatalf("hit=%q body=%q", hit, b)
	}
	if _, ok := dir.Endpoint("s3"); ok {
		t.Fatal("unmapped service resolved")
	}
}

func TestUnixSockets(t *testing.T) {
	dir := UnixSockets(map[string]string{"sqs": "/run/sqs.sock"})
	ep, ok := dir.Endpoint("sqs")
	if !ok || ep.Client == nil || !strings.Contains(ep.BaseURL, "sqs") {
		t.Fatalf("ep = %+v ok=%v", ep, ok)
	}
	if _, ok := dir.Endpoint("s3"); ok {
		t.Fatal("unmapped resolved")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("DOZE_SQS_SOCKET", "/run/x.sock")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://s3.local:9000")
	dir := FromEnv()
	if ep, ok := dir.Endpoint("sqs"); !ok || ep.Client == nil {
		t.Fatalf("sqs socket not resolved: %+v", ep)
	}
	if ep, ok := dir.Endpoint("s3"); !ok || ep.BaseURL != "http://s3.local:9000" {
		t.Fatalf("s3 url = %+v", ep)
	}
	if _, ok := dir.Endpoint("kms"); ok {
		t.Fatal("unset service resolved")
	}
}

func TestStaticAndURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	dir := Static{"sqs": {Client: srv.Client(), BaseURL: srv.URL}}
	ep, ok := dir.Endpoint("sqs")
	if !ok {
		t.Fatal("static not resolved")
	}
	if got := ep.URL("/foo"); got != srv.URL+"/foo" {
		t.Fatalf("URL = %q", got)
	}
	if _, ok := dir.Endpoint("nope"); ok {
		t.Fatal("missing static resolved")
	}
}
