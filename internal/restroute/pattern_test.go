package restroute

import "testing"

func TestPatternEdges(t *testing.T) {
	for _, c := range []struct {
		name   string
		segs   []string
		labels []string
		greedy bool
		want   string
	}{
		{"the root", nil, nil, false, "/"},
		{"a literal path", []string{"a", "b"}, []string{"", ""}, false, "/a/b"},
		{"a trailing greedy label", []string{"tags", ""}, []string{"", "resourceArn"}, true, "/tags/*"},
		{"a greedy label that is not last stays a label", []string{"", "x"}, []string{"id", ""}, true, "/{id}/x"},
		{"a label in the middle", []string{"a", "", "c"}, []string{"", "B", ""}, false, "/a/{B}/c"},
	} {
		if got := Pattern(c.segs, c.labels, c.greedy); got != c.want {
			t.Errorf("%s: Pattern = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTidyTrimsAndMarks(t *testing.T) {
	for in, want := range map[string]string{
		"/":           "/",
		"/a":          "/a",
		"/a/":         "/a",
		"/a//":        "/a",
		"///":         "/",
		"/a//b":       "/a/" + emptySeg + "/b",
		"/a///b/":     "/a/" + emptySeg + "/" + emptySeg + "/b",
		"/a/b/c":      "/a/b/c",
		"/a/b//":      "/a/b",
		"//a":         "/" + emptySeg + "/a",
		"/with space": "/with space",
	} {
		if got := tidy(in); got != want {
			t.Errorf("tidy(%q) = %q, want %q", in, got, want)
		}
	}
}
