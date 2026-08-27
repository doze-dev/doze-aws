package console

import "testing"

// TestResSlugIsSelectorSafe pins the property the whole out-of-band patch
// depends on: htmx addresses a patch target by concatenating "#" + id, so an id
// containing a dot, a slash or a space silently addresses something else — or
// nothing — and htmx reports neither.
func TestResSlugIsSelectorSafe(t *testing.T) {
	for _, name := range []string{
		"orders.fifo", "a.b", "a-b", "/db/password", "with space",
		"UPPER_and-lower99", "trailing.", "café", "a#b", "a[0]", "",
	} {
		got := resSlug(name)
		for i := 0; i < len(got); i++ {
			c := got[i]
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
				c >= '0' && c <= '9' || c == '-' || c == '_'
			if !ok {
				t.Errorf("resSlug(%q) = %q — byte %q is not selector-safe", name, got, c)
				break
			}
		}
	}
	// The mangling alone is lossy, so the fingerprint has to keep collisions
	// apart. "a.b" and "a-b" both mangle toward "a_b"/"a-b" territory.
	if resSlug("a.b") == resSlug("a_b") {
		t.Errorf("resSlug collapses %q and %q onto the same slot: %q", "a.b", "a_b", resSlug("a.b"))
	}
	if resSlug("plain") != "plain" {
		t.Errorf("resSlug(%q) = %q — an already-safe name should pass through unchanged", "plain", resSlug("plain"))
	}
}

func TestHumanSecs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"345600", "4 days"}, // the SQS retention default, which rendered as "345600 s"
		{"30", "30s"},
		{"0", "0s"},
		{"3600", "1 hour"},
		{"5400", "1 hour 30 mins"},
		{"", "—"},
		{"not-a-number", "not-a-number"}, // pass through rather than invent
	} {
		if got := humanSecs(tc.in); got != tc.want {
			t.Errorf("humanSecs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
