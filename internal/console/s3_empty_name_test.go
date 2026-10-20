package console_test

import (
	"net/url"
	"strings"
	"testing"
)

// An empty bucket name is PUT / to the service, which answers MethodNotAllowed
// ("unsupported service-level method PUT") because the service root takes only
// a list. That is the right answer to that request and the wrong one to the
// form: nobody asked to PUT the root, they left the name out. Real clients stop
// it before the call (botocore and the Go SDKs both refuse an empty Bucket), so
// the form says what is missing, in its own words, and sends nothing.
func TestAnEmptyBucketNameIsTheFormsToRefuse(t *testing.T) {
	h := newConsole(t)
	for _, name := range []string{"", "   "} {
		rec := req(t, h, "POST", "/_console/s3/create", url.Values{"name": {name}})
		if rec.Header().Get("HX-Doze-Error") != "1" {
			t.Fatalf("name %q: status %d, no refusal drawn:\n%s", name, rec.Code, clip(rec.Body.String()))
		}
		body := flatText(rec.Body.String())
		if strings.Contains(body, "MethodNotAllowed") || strings.Contains(body, "service-level") {
			t.Errorf("name %q: the page shows the service's answer to PUT /, not the form's: %s", name, body)
		}
		if want := "Refused a bucket needs a name"; body != want {
			t.Errorf("name %q: the page says %q, want %q", name, body, want)
		}
	}
}
