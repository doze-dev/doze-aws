package cloudformation

import (
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Every operation in the stub table says it is one.
//
// A stub answers 400 ValidationError, which is the code for a request whose
// input is wrong, and that is how the model sweep read it: 67 operations
// counted as handled, and nothing to make them move to the agreed refusal. The
// table is the fact, so the table is what this walks: an operation added to it
// is stamped or this fails.
func TestEveryStubbedActionIsStampedAsAStub(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir(), Logf: dozetest.Logf(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	actions := dozetest.Names(stubActions)
	sort.Strings(actions)
	if len(actions) == 0 {
		t.Skip("the stub table is empty: every operation is built or refused as not built")
	}
	for _, action := range actions {
		form := url.Values{"Action": {action}, "Version": {"2010-05-15"}}
		req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Header().Get(awshttp.LegacyStubHeader) == "" {
			t.Errorf("%s is in stubActions and its answer (%d) carries no %s", action, rec.Code, awshttp.LegacyStubHeader)
		}
	}

	// And an operation that is built is not: a refusal of its input is a
	// refusal of its input.
	form := url.Values{"Action": {"CreateStack"}, "Version": {"2010-05-15"}}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 400 || rec.Header().Get(awshttp.LegacyStubHeader) != "" {
		t.Errorf("an empty CreateStack answered %d with %s = %q; want a 400 that is not a stub's",
			rec.Code, awshttp.LegacyStubHeader, rec.Header().Get(awshttp.LegacyStubHeader))
	}
}
