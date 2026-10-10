package settings

import "testing"

func TestNothingIsBuiltUntilAsked(t *testing.T) {
	calls := 0
	Register("lazy-test", func() []Setting {
		calls++
		return []Setting{{Name: "B"}, {Name: "A", Min: Int64(1)}}
	})
	if calls != 0 || Built("lazy-test") {
		t.Fatalf("the builder ran at registration (calls=%d)", calls)
	}
	got := For("lazy-test")
	if calls != 1 || len(got) != 2 {
		t.Fatalf("calls=%d settings=%v", calls, got)
	}
	if got[0].Name != "A" || got[0].Service != "lazy-test" {
		t.Errorf("not sorted or not stamped with its service: %+v", got)
	}
	For("lazy-test")
	if calls != 1 {
		t.Errorf("built %d times, want once", calls)
	}
	if s, ok := Lookup("lazy-test", "A"); !ok || s.Min == nil || *s.Min != 1 {
		t.Errorf("Lookup = %+v, %v", s, ok)
	}
	if _, ok := Lookup("lazy-test", "Nope"); ok {
		t.Error("found a setting that is not there")
	}
	if For("nobody-registered-this") != nil {
		t.Error("a service with no registration should have no settings")
	}
}
