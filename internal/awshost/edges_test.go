package awshost

import "testing"

// A service label with nothing after it, and a single-label host, are the edges
// of the two region lookups: each reads the label that follows, so a host that
// ends there must have none, and the label before a service is never a region
// whatever it looks like.
func TestHostsThatEndAtTheServiceLabel(t *testing.T) {
	for _, tc := range []struct {
		host string
		want Info
	}{
		{"mybucket.s3", Info{Service: "s3", Bucket: "mybucket"}},
		{"abc123.execute-api", Info{Service: "apigateway", APIID: "abc123"}},
		{"fnid.lambda-url", Info{Service: "lambda", FunctionURLID: "fnid"}},
		{"sqs", Info{Service: "sqs"}},
		// The label before the service is the bucket, not a region, even when
		// it has the shape of one.
		{"us-east-1.s3", Info{Service: "s3", Bucket: "us-east-1"}},
		{"us-east-1.s3.us-west-2", Info{Service: "s3", Bucket: "us-east-1", Region: "us-west-2"}},
	} {
		if got := Parse(tc.host, ""); got != tc.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.host, got, tc.want)
		}
	}
}

// The suffix is stripped before the labels are read, so a suffix that itself
// looks like a region is not mistaken for the request's.
func TestASuffixThatLooksLikeARegionIsNotTheRegion(t *testing.T) {
	got := Parse("b.s3.zz-top-1.test", "zz-top-1.test")
	if want := (Info{Service: "s3", Bucket: "b"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// And with no suffix configured the same labels are read as they stand.
	got = Parse("b.s3.zz-top-1.test", "")
	if want := (Info{Service: "s3", Bucket: "b", Region: "zz-top-1"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A region is lowercase letters, then a number. The characters at each edge of
// both ranges are the ones a boundary written one off would let through.
func TestRegionShapeEdges(t *testing.T) {
	for s, want := range map[string]bool{
		"us-east-1":     true,
		"us-east-0":     true,
		"us-east-9":     true,
		"us-east-90":    true,
		"aa-zz-1":       true,
		"us-east-/":     false, // '/' is just below '0'
		"us-east-:":     false, // ':' is just above '9'
		"us-east-1a":    false,
		"u`-east-1":     false, // '`' is just below 'a'
		"u{-east-1":     false, // '{' is just above 'z'
		"US-EAST-1":     false,
		"us-east-":      false,
		"us--1":         false,
		"us-east":       false,
		"us-gov-east-1": true,
	} {
		if got := looksLikeRegion(s); got != want {
			t.Errorf("looksLikeRegion(%q) = %v, want %v", s, got, want)
		}
	}
}
