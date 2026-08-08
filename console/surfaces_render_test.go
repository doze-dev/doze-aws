package console_test

// Every service surface must render. A template referencing a field the
// handler never sets fails at execution time rather than at build time, so a
// console that compiles proves nothing about whether its pages work — and a
// template error is written into the response body with a 200, so it has to be
// looked for rather than inferred from the status.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEverySurfaceRenders(t *testing.T) {
	c := newConsole(t)
	paths := []string{
		"/", "/traffic",
		"/s3", "/ddb", "/sqs", "/sns", "/eb", "/kinesis",
		"/lambda", "/cfn", "/apigw", "/iam", "/kms", "/sm", "/ssm",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console"+p, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d", p, rec.Code)
			}
			if body := rec.Body.String(); strings.Contains(body, "template:") {
				i := strings.Index(body, "template:")
				t.Fatalf("GET %s rendered a template error: %s", p, body[i:min(i+220, len(body))])
			}
		})
	}
}

// The rail lists every surface, so a page that exists but is unreachable from
// the nav is only half-delivered.
func TestRailLinksEverySurface(t *testing.T) {
	c := newConsole(t)
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console/", nil))
	body := rec.Body.String()
	for _, href := range []string{"/_console/cfn", "/_console/apigw", "/_console/iam"} {
		if !strings.Contains(body, `href="`+href+`"`) {
			t.Errorf("rail has no link to %s", href)
		}
	}
}
