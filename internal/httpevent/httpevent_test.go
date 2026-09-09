package httpevent

// The 2.0 payload is shared by HTTP API routes and Lambda function URLs, and
// the package had no test file. What that left unproven is the binary path:
// a body that is not text must reach the function base64-encoded with
// isBase64Encoded true, and a response that says isBase64Encoded must be
// decoded on the way back. Nothing asserted either, at any level — the string
// "isBase64Encoded" appeared in no test in the repository. A regression there
// does not fail loudly; it silently corrupts every image, PDF and
// octet-stream that goes through an HTTP API, and the JSON cases keep
// passing.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func eventFor(method, path, ct string, body []byte) map[string]any {
	r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	return Event(Request{R: r, Path: path, Body: body, Now: time.Unix(1700000000, 0)})
}

// TestBinaryBodiesAreBase64Encoded: the rule AWS applies, and the flag that
// tells the function which it got.
func TestBinaryBodiesAreBase64Encoded(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0x00}

	t.Run("binary", func(t *testing.T) {
		ev := eventFor(http.MethodPost, "/upload", "image/png", raw)
		if ev["isBase64Encoded"] != true {
			t.Errorf("isBase64Encoded = %v, want true", ev["isBase64Encoded"])
		}
		want := base64.StdEncoding.EncodeToString(raw)
		if ev["body"] != want {
			t.Errorf("body = %q, want the base64 %q", ev["body"], want)
		}
	})

	t.Run("text passes through verbatim", func(t *testing.T) {
		for _, ct := range []string{"text/plain", "application/json", "application/xml",
			"application/x-www-form-urlencoded", "text/javascript", ""} {
			ev := eventFor(http.MethodPost, "/", ct, []byte(`{"a":1}`))
			if ev["isBase64Encoded"] != false {
				t.Errorf("%q: isBase64Encoded = %v, want false", ct, ev["isBase64Encoded"])
			}
			if ev["body"] != `{"a":1}` {
				t.Errorf("%q: body = %q, want it verbatim", ct, ev["body"])
			}
		}
	})

	t.Run("an empty binary body is not flagged", func(t *testing.T) {
		// AWS sends isBase64Encoded false when there is no body at all;
		// flagging an empty string makes a function that decodes it eagerly
		// take the binary branch for a GET.
		ev := eventFor(http.MethodGet, "/", "image/png", nil)
		if ev["isBase64Encoded"] != false {
			t.Errorf("isBase64Encoded = %v, want false for an empty body", ev["isBase64Encoded"])
		}
		if ev["body"] != "" {
			t.Errorf("body = %q, want empty", ev["body"])
		}
	})
}

func TestIsText(t *testing.T) {
	for _, c := range []struct {
		ct   string
		text bool
	}{
		{"", true},
		{"text/plain", true},
		{"text/html; charset=utf-8", true},
		{"application/json", true},
		{"application/vnd.api+json", true},
		{"application/xml", true},
		{"text/xml", true},
		{"application/x-www-form-urlencoded", true},
		{"application/javascript", true},
		{"TEXT/PLAIN", true},
		{"application/octet-stream", false},
		{"image/png", false},
		{"application/pdf", false},
		{"multipart/form-data; boundary=x", false},
	} {
		if got := IsText(c.ct); got != c.text {
			t.Errorf("IsText(%q) = %v, want %v", c.ct, got, c.text)
		}
	}
}

// TestEventShape pins the document a function actually receives.
func TestEventShape(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/items/42?q=a&q=b&sort=asc", nil)
	r.Header.Set("X-Trace", "t-1")
	r.Header.Add("Cookie", "a=1; b=2")
	r.Header.Add("Cookie", "c=3")
	r.Host = "api.example.test"
	r.RemoteAddr = "203.0.113.7:51234"

	ev := Event(Request{
		R: r, Path: "/items/42", RouteKey: "GET /items/{id}", Stage: "prod",
		APIID: "abc123", DomainName: "abc123.execute-api.localhost", AccountID: "000000000000",
		RequestID: "req-1", Now: time.Unix(1700000000, 0),
		PathParameters: map[string]string{"id": "42"},
		StageVariables: map[string]string{"env": "prod"},
		Authorizer:     map[string]any{"lambda": map[string]any{"tier": "gold"}},
	})

	if ev["version"] != "2.0" || ev["routeKey"] != "GET /items/{id}" || ev["rawPath"] != "/items/42" {
		t.Errorf("version/routeKey/rawPath = %v/%v/%v", ev["version"], ev["routeKey"], ev["rawPath"])
	}
	if ev["rawQueryString"] != "q=a&q=b&sort=asc" {
		t.Errorf("rawQueryString = %q", ev["rawQueryString"])
	}

	headers := ev["headers"].(map[string]string)
	if headers["x-trace"] != "t-1" {
		t.Errorf("headers must be lowercased: %+v", headers)
	}
	if headers["host"] != "api.example.test" {
		t.Errorf("host header = %q", headers["host"])
	}
	if _, ok := headers["cookie"]; ok {
		t.Errorf("cookies move to their own member, not headers: %+v", headers)
	}

	cookies := ev["cookies"].([]string)
	if len(cookies) != 3 || cookies[0] != "a=1" || cookies[1] != "b=2" || cookies[2] != "c=3" {
		t.Errorf("cookies = %+v, want each pair split across both header lines", cookies)
	}

	// A repeated query key is joined with a comma, as 2.0 does — it has no
	// multi-value member, unlike 1.0.
	q := ev["queryStringParameters"].(map[string]string)
	if q["q"] != "a,b" || q["sort"] != "asc" {
		t.Errorf("queryStringParameters = %+v", q)
	}

	if ev["pathParameters"].(map[string]string)["id"] != "42" {
		t.Errorf("pathParameters = %+v", ev["pathParameters"])
	}
	if ev["stageVariables"].(map[string]string)["env"] != "prod" {
		t.Errorf("stageVariables = %+v", ev["stageVariables"])
	}

	rc := ev["requestContext"].(map[string]any)
	if rc["apiId"] != "abc123" || rc["stage"] != "prod" || rc["accountId"] != "000000000000" {
		t.Errorf("requestContext = %+v", rc)
	}
	if rc["domainPrefix"] != "abc123" {
		t.Errorf("domainPrefix = %v, want the first label of the domain", rc["domainPrefix"])
	}
	if rc["timeEpoch"] != int64(1700000000000) {
		t.Errorf("timeEpoch = %v", rc["timeEpoch"])
	}
	if rc["time"] != "14/Nov/2023:22:13:20 +0000" {
		t.Errorf("time = %v", rc["time"])
	}
	reqHTTP := rc["http"].(map[string]any)
	if reqHTTP["method"] != "GET" || reqHTTP["path"] != "/items/42" || reqHTTP["sourceIp"] != "203.0.113.7" {
		t.Errorf("requestContext.http = %+v — sourceIp must lose the port", reqHTTP)
	}
	if rc["authorizer"].(map[string]any)["lambda"].(map[string]any)["tier"] != "gold" {
		t.Errorf("authorizer = %+v", rc["authorizer"])
	}
}

// TestEventDefaults: a function URL supplies none of the route metadata and
// must still get AWS's defaults rather than empty strings.
func TestEventDefaults(t *testing.T) {
	ev := Event(Request{R: httptest.NewRequest(http.MethodGet, "/", nil), Path: "/"})
	rc := ev["requestContext"].(map[string]any)
	if ev["routeKey"] != "$default" || rc["stage"] != "$default" || rc["accountId"] != "anonymous" {
		t.Errorf("defaults = routeKey %v stage %v account %v", ev["routeKey"], rc["stage"], rc["accountId"])
	}
	for _, absent := range []string{"queryStringParameters", "cookies", "pathParameters", "stageVariables"} {
		if _, ok := ev[absent]; ok {
			t.Errorf("%s must be omitted when empty, not sent as an empty object", absent)
		}
	}
	if _, ok := rc["authorizer"]; ok {
		t.Errorf("authorizer must be omitted when there is none")
	}
}

// TestEventIsJSONSerialisable: the event is marshalled before it reaches the
// runtime, so a map value that json cannot encode is a 500 at invoke time,
// not here.
func TestEventIsJSONSerialisable(t *testing.T) {
	ev := eventFor(http.MethodPost, "/x", "image/png", []byte{0x00, 0xff})
	if _, err := json.Marshal(ev); err != nil {
		t.Fatalf("the event must marshal: %v", err)
	}
}

// TestWriteResponse is the other half: what the function answers with.
func TestWriteResponse(t *testing.T) {
	t.Run("a structured response", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteResponse(w, []byte(`{"statusCode":201,"headers":{"X-Made":"here","Content-Type":"application/json"},`+
			`"cookies":["s=1","t=2"],"body":"{\"ok\":true}"}`))
		if w.Code != 201 {
			t.Errorf("code = %d", w.Code)
		}
		if w.Header().Get("X-Made") != "here" || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("headers = %+v", w.Header())
		}
		if got := w.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "s=1" || got[1] != "t=2" {
			t.Errorf("cookies become separate Set-Cookie lines, got %+v", got)
		}
		if w.Body.String() != `{"ok":true}` {
			t.Errorf("body = %q", w.Body.String())
		}
	})

	t.Run("base64 bodies are decoded", func(t *testing.T) {
		raw := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
		w := httptest.NewRecorder()
		WriteResponse(w, []byte(`{"statusCode":200,"isBase64Encoded":true,"body":"`+
			base64.StdEncoding.EncodeToString(raw)+`"}`))
		if w.Body.String() != string(raw) {
			t.Errorf("body = %x, want the decoded %x", w.Body.Bytes(), raw)
		}
	})

	t.Run("an undecodable base64 body is written verbatim", func(t *testing.T) {
		// Dropping it would turn a function's bug into an empty 200, which is
		// harder to diagnose than the wrong bytes.
		w := httptest.NewRecorder()
		WriteResponse(w, []byte(`{"statusCode":200,"isBase64Encoded":true,"body":"not!base64!"}`))
		if w.Body.String() != "not!base64!" {
			t.Errorf("body = %q", w.Body.String())
		}
	})

	t.Run("a bare value is the body of a 200", func(t *testing.T) {
		for _, out := range []string{`"hello"`, `[1,2,3]`, `42`, `{"ok":true}`} {
			w := httptest.NewRecorder()
			WriteResponse(w, []byte(out))
			if w.Code != 200 || w.Body.String() != out {
				t.Errorf("%s: code %d body %q", out, w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Errorf("%s: content type = %q", out, w.Header().Get("Content-Type"))
			}
		}
	})

	t.Run("an object without statusCode is a body, not a response", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteResponse(w, []byte(`{"headers":{"X-No":"1"},"body":"nope"}`))
		if w.Code != 200 || w.Body.String() != `{"headers":{"X-No":"1"},"body":"nope"}` {
			t.Errorf("code %d body %q", w.Code, w.Body.String())
		}
		if w.Header().Get("X-No") != "" {
			t.Errorf("its headers must not be applied: %+v", w.Header())
		}
	})

	t.Run("a default content type is set when the function names none", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteResponse(w, []byte(`{"statusCode":204,"body":""}`))
		if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("content type = %q", w.Header().Get("Content-Type"))
		}
	})
}
