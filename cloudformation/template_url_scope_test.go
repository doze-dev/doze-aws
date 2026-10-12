package cloudformation_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/cloudformation"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/sigparse"
)

// A TemplateURL is fetched from S3 through the gateway, and the request is
// signed so the gateway can tell which service and region it is for. The scope
// named us-east-1 whatever region the instance was. The gateway in front of the
// request records what it was asked, as any router of regions would read it.
func TestATemplateURLIsFetchedInTheInstancesRegion(t *testing.T) {
	var scope sigparse.Scope
	var path string
	gateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, _ = sigparse.Parse(r)
		path = r.URL.Path
		io.WriteString(w, `{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}}}`)
	})
	s, err := cloudformation.New(cloudformation.Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t), Gateway: gateway,
		Identity: awsident.Identity{Region: "eu-west-1", AccountID: "111122223333"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	form := url.Values{"Action": {"ValidateTemplate"}, "Version": {"2010-05-15"},
		"TemplateURL": {"https://s3.eu-west-1.amazonaws.com/staging/app.template.json"}}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ValidateTemplate: %d %s", rec.Code, rec.Body.String())
	}
	if path != "/staging/app.template.json" {
		t.Fatalf("the template was not fetched: the gateway saw %q", path)
	}
	if scope.Service != "s3" {
		t.Errorf("the fetch is scoped to service %q, want s3", scope.Service)
	}
	if scope.Region != "eu-west-1" {
		t.Errorf("the fetch is scoped to region %q, want the instance's, eu-west-1", scope.Region)
	}
}
