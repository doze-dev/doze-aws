package awspage

import (
	"errors"
	"slices"
	"testing"
)

func ident(s string) string { return s }

func walk(t *testing.T, items []string, limit int) (seen []string, pages int) {
	t.Helper()
	tok := ""
	for {
		pages++
		if pages > len(items)+2 {
			t.Fatalf("paging did not terminate: %d pages for %d items", pages, len(items))
		}
		pg, next, err := Slice(items, ident, tok, limit)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, pg...)
		if next == "" {
			return seen, pages
		}
		tok = next
	}
}

func TestEveryItemExactlyOnceInOrder(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	for _, limit := range []int{1, 2, 3, 4, 5, 6, 100, 0} {
		got, _ := walk(t, items, limit)
		if !slices.Equal(got, items) {
			t.Errorf("limit %d: got %v, want %v", limit, got, items)
		}
	}
}

func TestPageCountAndFinalTokenEmpty(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	if _, pages := walk(t, items, 2); pages != 3 {
		t.Errorf("5 items at 2 a page took %d pages, want 3", pages)
	}
	// Exactly a full last page must not leave a dangling token and an empty page.
	if _, pages := walk(t, []string{"a", "b", "c", "d"}, 2); pages != 2 {
		t.Errorf("4 items at 2 a page took %d pages, want 2", pages)
	}
	if pg, next, _ := Slice([]string{}, ident, "", 5); len(pg) != 0 || next != "" {
		t.Errorf("an empty listing gave %v, %q", pg, next)
	}
}

// The reason the cursor is a key: a listing that changes between two pages.
func TestAChangeBetweenPagesMovesNothing(t *testing.T) {
	items := []string{"b", "d", "f", "h"}
	first, next, _ := Slice(items, ident, "", 2) // b d
	if !slices.Equal(first, []string{"b", "d"}) {
		t.Fatal(first)
	}
	// An item is inserted before the cursor and the one at the cursor is deleted.
	changed := []string{"a", "b", "f", "h"}
	second, _, err := Slice(changed, ident, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(second, []string{"f", "h"}) {
		t.Errorf("after a change the next page was %v, want [f h]: an offset cursor would have repeated or skipped", second)
	}
}

func TestBadTokensAreRefused(t *testing.T) {
	for _, tok := range []string{"nope", "!!!", Token("x")[:3], "MTI3"} { // not base64, not ours
		if _, _, err := Slice([]string{"a"}, ident, tok, 1); !errors.Is(err, ErrBadToken) {
			t.Errorf("token %q: err = %v, want ErrBadToken", tok, err)
		}
	}
	// An empty token is the start, not an error, and a valid token for a key
	// that is not in the listing just starts after it.
	if _, _, err := Slice([]string{"a", "c"}, ident, Token("b"), 1); err != nil {
		t.Errorf("a token for a key that is gone was refused: %v", err)
	}
}

func TestLimit(t *testing.T) {
	for _, tc := range []struct{ req, def, max, want int }{
		{0, 50, 100, 50}, {-3, 50, 100, 50}, {10, 50, 100, 10}, {500, 50, 100, 100}, {7, 50, 0, 7},
	} {
		if got := Limit(tc.req, tc.def, tc.max); got != tc.want {
			t.Errorf("Limit(%d,%d,%d) = %d, want %d", tc.req, tc.def, tc.max, got, tc.want)
		}
	}
}

func TestKeysThatSortOddly(t *testing.T) {
	// A key containing the prefix, slashes and unicode must survive the token.
	items := []string{"/a/b", "dz1:x", "zz", "é"} // byte order: é is 0xC3
	got, _ := walk(t, items, 1)
	if !slices.Equal(got, items) {
		t.Errorf("got %v, want %v", got, items)
	}
}
