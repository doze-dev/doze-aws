package console_test

// The IAM management flow, driven through the console the way a browser does:
// create a principal and a policy, attach, confirm the attachment changed what
// IAM would decide, then detach and delete. Asserting through the simulator
// matters — an attach that lists correctly but does not change a verdict has
// not actually granted anything.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postForm(t *testing.T, c http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_console"+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	return rec
}

func getBody(t *testing.T, c http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_console"+path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestIAMCreateAttachDetachFlow(t *testing.T) {
	c := newConsole(t)

	// Create a role and a policy.
	if rec := postForm(t, c, "/iam/create", url.Values{
		"kind": {"role"}, "name": {"flow-role"},
		"trust": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
	}); rec.Code >= 400 {
		t.Fatalf("create role: %d %s", rec.Code, rec.Body)
	}
	if rec := postForm(t, c, "/iam/create", url.Values{
		"kind": {"policy"}, "name": {"flow-policy"},
		"document": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["sqs:SendMessage"],"Resource":"*"}]}`},
	}); rec.Code >= 400 {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}
	if body := getBody(t, c, "/iam/role/flow-role"); !strings.Contains(body, "flow-role") {
		t.Fatal("role page does not show the role")
	}

	arn := "arn:aws:iam::000000000000:policy/flow-policy"
	sim := func() string {
		return postForm(t, c, "/iam/simulate", url.Values{
			"principal": {"arn:aws:iam::000000000000:role/flow-role"},
			"actions":   {"sqs:SendMessage"},
		}).Body.String()
	}

	// Before attaching, the action is not permitted.
	if strings.Contains(sim(), ">allowed<") {
		t.Fatal("action allowed before the policy was attached")
	}

	postForm(t, c, "/iam/role/flow-role/attach", url.Values{"arn": {arn}})
	if body := getBody(t, c, "/iam/role/flow-role"); !strings.Contains(body, "flow-policy") {
		t.Error("attached policy is not listed on the principal")
	}
	// The attachment has to change the decision, not just the listing.
	if !strings.Contains(sim(), ">allowed<") {
		t.Error("attaching the policy did not change what IAM decides")
	}

	postForm(t, c, "/iam/role/flow-role/detach", url.Values{"arn": {arn}})
	if strings.Contains(sim(), ">allowed<") {
		t.Error("detaching the policy did not revoke the grant")
	}

	// Cleanup is part of the flow: a console that creates but cannot remove is
	// only half a management surface.
	if rec := postForm(t, c, "/iam/role/flow-role/delete", nil); rec.Code >= 400 {
		t.Errorf("delete role: %d", rec.Code)
	}
	if rec := postForm(t, c, "/iam/policy/delete", url.Values{"arn": {arn}}); rec.Code >= 400 {
		t.Errorf("delete policy: %d", rec.Code)
	}
	if body := getBody(t, c, "/iam"); strings.Contains(body, "flow-role") {
		t.Error("deleted role still listed")
	}
}

// A user's access key is shown once. The listing must reflect the key's
// existence afterwards even though the secret is gone.
func TestIAMAccessKeyLifecycle(t *testing.T) {
	c := newConsole(t)
	postForm(t, c, "/iam/create", url.Values{"kind": {"user"}, "name": {"key-user"}})

	rec := postForm(t, c, "/iam/user/key-user/keys", nil)
	flash := rec.Header().Get("Location") + rec.Header().Get("HX-Redirect")
	if !strings.Contains(flash, "AKIA") {
		t.Fatalf("the created key was not surfaced: %q", flash)
	}
	body := getBody(t, c, "/iam/user/key-user")
	if !strings.Contains(body, "AKIA") {
		t.Error("key not listed on the user page")
	}
}

// A redirect after a mutation has to land on a real page. Asserting the 303
// alone passes while the Location drops the console's path prefix and every
// mutation dead-ends on a 404 — which is exactly what happened.
func TestMutationRedirectsLandSomewhere(t *testing.T) {
	c := newConsole(t)
	postForm(t, c, "/iam/create", url.Values{"kind": {"user"}, "name": {"redir-user"}})
	postForm(t, c, "/kinesis/create", url.Values{"name": {"redir-stream"}, "shards": {"1"}})

	cases := []struct {
		path string
		form url.Values
	}{
		{"/iam/create", url.Values{"kind": {"user"}, "name": {"redir-user-2"}}},
		{"/iam/user/redir-user/attach", url.Values{"arn": {"arn:aws:iam::aws:policy/ReadOnlyAccess"}}},
		{"/iam/user/redir-user/inline", url.Values{
			"policy":   {"p"},
			"document": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
		}},
		{"/kinesis/redir-stream/retention", url.Values{"hours": {"48"}}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := postForm(t, c, tc.path, tc.form)
			loc := rec.Header().Get("Location")
			if loc == "" {
				t.Fatalf("no redirect (status %d)", rec.Code)
			}
			if !strings.HasPrefix(loc, "/_console/") {
				t.Fatalf("redirect leaves the console: %s", loc)
			}
			// Follow it: the destination must actually render.
			follow := httptest.NewRecorder()
			c.ServeHTTP(follow, httptest.NewRequest(http.MethodGet, loc, nil))
			if follow.Code != http.StatusOK {
				t.Fatalf("redirect to %s = %d", loc, follow.Code)
			}
		})
	}
}
