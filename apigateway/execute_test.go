package apigateway

import (
	"testing"
)

// TestMatchResourcePrecedence covers API Gateway's routing order, which is not
// "first match wins":
//
//  1. a literal segment beats a path parameter
//  2. a path parameter beats a greedy proxy
//  3. the deepest greedy proxy wins
//
// Getting this wrong sends /users/me to the /users/{id} handler, which is the
// kind of bug that only shows up against real traffic.
func TestMatchResourcePrecedence(t *testing.T) {
	api := &RestAPI{Resources: map[string]*Resource{}}
	add := func(id, path string) {
		api.Resources[id] = &Resource{ID: id, Path: path}
	}
	add("root", "/")
	add("users", "/users")
	add("userId", "/users/{id}")
	add("userMe", "/users/me")
	add("userPosts", "/users/{id}/posts")
	add("proxy", "/{proxy+}")
	add("apiProxy", "/api/{proxy+}")

	cases := []struct {
		path       string
		wantID     string
		wantParams map[string]string
	}{
		{"/", "root", nil},
		{"/users", "users", nil},
		// A literal beats a parameter at the same depth.
		{"/users/me", "userMe", nil},
		{"/users/42", "userId", map[string]string{"id": "42"}},
		{"/users/42/posts", "userPosts", map[string]string{"id": "42"}},
		// A parameter beats the greedy proxy.
		{"/other", "proxy", map[string]string{"proxy": "other"}},
		// The deepest proxy wins over a root one.
		{"/api/v1/thing", "apiProxy", map[string]string{"proxy": "v1/thing"}},
		// A greedy proxy also matches the empty remainder.
		{"/api", "apiProxy", map[string]string{"proxy": ""}},
	}
	for _, c := range cases {
		res, params, ok := matchResource(api, c.path)
		if !ok {
			t.Errorf("%s: no match", c.path)
			continue
		}
		if res.ID != c.wantID {
			t.Errorf("%s matched %s, want %s", c.path, res.ID, c.wantID)
			continue
		}
		for k, want := range c.wantParams {
			if params[k] != want {
				t.Errorf("%s: param %s = %q, want %q", c.path, k, params[k], want)
			}
		}
	}
}

func TestMatchResourceNoMatch(t *testing.T) {
	api := &RestAPI{Resources: map[string]*Resource{
		"root":  {ID: "root", Path: "/"},
		"users": {ID: "users", Path: "/users"},
	}}
	// Without a proxy resource, an unknown path must not match anything.
	if res, _, ok := matchResource(api, "/nope/deeper"); ok {
		t.Fatalf("/nope/deeper matched %s, want no match", res.ID)
	}
}

func TestLambdaFromURI(t *testing.T) {
	cases := []struct{ uri, want string }{
		{
			"arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
				"arn:aws:lambda:us-east-1:000000000000:function:my-fn/invocations",
			"my-fn",
		},
		{
			// A qualified ARN drops the qualifier.
			"arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" +
				"arn:aws:lambda:us-east-1:000000000000:function:my-fn:live/invocations",
			"my-fn",
		},
		{"http://example.com/not-a-lambda", ""},
	}
	for _, c := range cases {
		if got := lambdaFromURI(c.uri); got != c.want {
			t.Errorf("lambdaFromURI(%q) = %q, want %q", c.uri, got, c.want)
		}
	}
}

func TestRebuildPaths(t *testing.T) {
	api := &RestAPI{Resources: map[string]*Resource{
		"root": {ID: "root", Path: "/"},
		"a":    {ID: "a", ParentID: "root", PathPart: "users"},
		"b":    {ID: "b", ParentID: "a", PathPart: "{id}"},
		"c":    {ID: "c", ParentID: "b", PathPart: "posts"},
	}}
	rebuildPaths(api)
	want := map[string]string{
		"root": "/", "a": "/users", "b": "/users/{id}", "c": "/users/{id}/posts",
	}
	for id, path := range want {
		if api.Resources[id].Path != path {
			t.Errorf("%s path = %q, want %q", id, api.Resources[id].Path, path)
		}
	}
}
