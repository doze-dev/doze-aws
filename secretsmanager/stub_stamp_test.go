package secretsmanager

import (
	"net/http"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// Every operation in the stub table says it is one.
//
// A stub answers 400 InvalidRequestException, the code for a request that is
// wrong, and the model sweep counted the three as handled. The table is the
// fact, so the table is what this walks. The request is the sweep's own, an
// empty one.
func TestEveryStubbedActionIsStampedAsAStub(t *testing.T) {
	ts := smServer(t)
	post := func(action string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("POST", ts.URL+"/", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", "secretsmanager."+action)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	actions := dozetest.Names(stubActions)
	if len(actions) == 0 {
		t.Skip("the stub table is empty: every operation is built or refused as not built")
	}
	for _, action := range actions {
		if resp := post(action); resp.Header.Get(awshttp.LegacyStubHeader) == "" {
			t.Errorf("%s is in stubActions and its answer (%d) carries no %s", action, resp.StatusCode, awshttp.LegacyStubHeader)
		}
	}
	// An operation that is built answers the same code for a real reason, and
	// is not a stub: the header is what tells the two apart.
	if resp := post("DescribeSecret"); resp.StatusCode != 400 || resp.Header.Get(awshttp.LegacyStubHeader) != "" {
		t.Errorf("an empty DescribeSecret answered %d with %s = %q; want a 400 that is not a stub's",
			resp.StatusCode, awshttp.LegacyStubHeader, resp.Header.Get(awshttp.LegacyStubHeader))
	}
}
