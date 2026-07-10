package sigparse

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestParseV4Header(t *testing.T) {
	h := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-date, Signature=fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024"
	s, ok := ParseAuthorization(h)
	if !ok {
		t.Fatalf("ParseAuthorization(%q) not ok", h)
	}
	want := Scope{AccessKeyID: "AKIDEXAMPLE", Date: "20130524", Region: "us-east-1", Service: "s3", Version: 4}
	if s != want {
		t.Errorf("scope = %+v, want %+v", s, want)
	}
}

func TestParseV4HeaderCredentialLast(t *testing.T) {
	// Order of the comma-separated components is not guaranteed.
	h := "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=abc, Credential=AKID/20260101/eu-west-2/events/aws4_request"
	s, ok := ParseAuthorization(h)
	if !ok || s.Service != "events" || s.Region != "eu-west-2" {
		t.Errorf("scope = %+v ok=%v, want events/eu-west-2", s, ok)
	}
}

func TestParseV2Header(t *testing.T) {
	s, ok := ParseAuthorization("AWS AKIDEXAMPLE:frJIUN8DYpKDtOLCwo//yllqDzg=")
	if !ok {
		t.Fatal("V2 header not recognized")
	}
	if s.AccessKeyID != "AKIDEXAMPLE" || s.Version != 2 || s.Service != "" {
		t.Errorf("scope = %+v", s)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, h := range []string{
		"",
		"Bearer abc123",
		"AWS ",                           // no key
		"AWS :sig",                       // empty key
		"AWS AKID",                       // no signature separator
		"AWS AKID:",                      // empty signature
		"AWS4-HMAC-SHA256 Signature=abc", // no credential
		"AWS4-HMAC-SHA256 Credential=AKID/20130524/us-east-1/s3/aws4_extra", // bad terminator
		"AWS4-HMAC-SHA256 Credential=/20130524/us-east-1/s3/aws4_request",   // empty key
		"AWS4-HMAC-SHA256 Credential=AKID/20130524/us-east-1/aws4_request",  // 4 parts
	} {
		if s, ok := ParseAuthorization(h); ok {
			t.Errorf("ParseAuthorization(%q) = %+v, want not-ok", h, s)
		}
	}
}

func TestParsePresignedV4(t *testing.T) {
	q := url.Values{
		"X-Amz-Algorithm":  {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential": {"AKIDEXAMPLE/20130524/us-east-1/s3/aws4_request"},
		"X-Amz-Date":       {"20130524T000000Z"},
		"X-Amz-Expires":    {"86400"},
	}
	s, ok := ParsePresigned(q)
	if !ok || !s.Presigned || s.Version != 4 || s.Service != "s3" {
		t.Errorf("scope = %+v ok=%v", s, ok)
	}
}

func TestParsePresignedV2(t *testing.T) {
	q := url.Values{
		"AWSAccessKeyId": {"AKIDEXAMPLE"},
		"Signature":      {"vjbyPxybdZaNmGa%2ByT272YEAiv4%3D"},
		"Expires":        {"1141889120"},
	}
	s, ok := ParsePresigned(q)
	if !ok || !s.Presigned || s.Version != 2 || s.AccessKeyID != "AKIDEXAMPLE" {
		t.Errorf("scope = %+v ok=%v", s, ok)
	}
}

func TestParseFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.com/bucket/key?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKID%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=300", nil)
	s, ok := Parse(r)
	if !ok || s.Service != "s3" || !s.Presigned {
		t.Errorf("scope = %+v ok=%v", s, ok)
	}

	r = httptest.NewRequest("POST", "https://example.com/", nil)
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/sqs/aws4_request, Signature=x")
	s, ok = Parse(r)
	if !ok || s.Service != "sqs" {
		t.Errorf("scope = %+v ok=%v", s, ok)
	}

	r = httptest.NewRequest("GET", "https://example.com/", nil)
	if s, ok := Parse(r); ok {
		t.Errorf("anonymous request parsed as %+v", s)
	}
}

func TestPresignedExpiry(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name             string
		q                url.Values
		present, expired bool
	}{
		{"no presign params", url.Values{}, false, false},
		{"v4 live", url.Values{
			"X-Amz-Date": {"20260710T115000Z"}, "X-Amz-Expires": {"3600"},
		}, true, false},
		{"v4 expired", url.Values{
			"X-Amz-Date": {"20260710T100000Z"}, "X-Amz-Expires": {"300"},
		}, true, true},
		{"v4 bad date", url.Values{
			"X-Amz-Date": {"not-a-date"}, "X-Amz-Expires": {"300"},
		}, true, true},
		{"v4 bad expires", url.Values{
			"X-Amz-Date": {"20260710T115000Z"}, "X-Amz-Expires": {"-1"},
		}, true, true},
		{"v2 live", url.Values{
			"AWSAccessKeyId": {"AKID"}, "Signature": {"x"}, "Expires": {"1800000000"}, // 2027-01-15, after `now`
		}, true, false},
		{"v2 expired", url.Values{
			"AWSAccessKeyId": {"AKID"}, "Signature": {"x"}, "Expires": {"1141889120"}, // 2006
		}, true, true},
		{"v2 bad expires", url.Values{
			"AWSAccessKeyId": {"AKID"}, "Signature": {"x"}, "Expires": {"soon"},
		}, true, true},
		{"v2 expires without signature is not presigned", url.Values{
			"Expires": {"1141889120"},
		}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			present, expired := PresignedExpiry(tc.q, now)
			if present != tc.present || expired != tc.expired {
				t.Errorf("PresignedExpiry = (present=%v, expired=%v), want (%v, %v)", present, expired, tc.present, tc.expired)
			}
		})
	}
}

// FuzzParse asserts the parsers never panic and, when they do accept input,
// produce a structurally sane scope.
func FuzzParse(f *testing.F) {
	f.Add("AWS4-HMAC-SHA256 Credential=AKID/20130524/us-east-1/s3/aws4_request, Signature=x", "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=A%2Fb%2Fc%2Fd%2Faws4_request")
	f.Add("AWS AKID:sig", "AWSAccessKeyId=AKID&Signature=s&Expires=99")
	f.Add("", "")
	f.Add("AWS4-HMAC-SHA256", "X-Amz-Date=zzz&X-Amz-Expires=1e9")
	f.Fuzz(func(t *testing.T, header, rawQuery string) {
		if s, ok := ParseAuthorization(header); ok {
			if s.AccessKeyID == "" || (s.Version != 2 && s.Version != 4) {
				t.Errorf("accepted malformed scope %+v from %q", s, header)
			}
		}
		q, err := url.ParseQuery(rawQuery)
		if err != nil {
			return
		}
		if s, ok := ParsePresigned(q); ok {
			if s.AccessKeyID == "" || !s.Presigned {
				t.Errorf("accepted malformed presigned scope %+v from %q", s, rawQuery)
			}
		}
		PresignedExpiry(q, time.Unix(1e9, 0))
	})
}
