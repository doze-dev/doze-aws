package dozetest

// A list operation, driven through its paging, held to what a caller relies on.
//
// The check is the same for every service and does not know the wire: a service
// supplies a PageFunc that makes one call with a limit and a token and returns
// the identifiers of the items on that page and the token for the next, and
// this walks it. It exists because "ignores MaxResults and returns everything
// with no token" was the answer of most list operations in the tree, and
// nothing failed: every SDK paginator, given one page, is satisfied.

import (
	"sort"
	"testing"
)

// PageFunc makes one call to a list operation. ids are stable identifiers of
// the items on the page, in the order returned; next is the token for the
// following page, or "" when this was the last.
type PageFunc func(limit int, token string) (ids []string, next string, err error)

// PagingOptions tunes AssertPaginates for an operation with its own rules.
type PagingOptions struct {
	// Ordered also asserts the items come back in exactly want's order. Leave
	// it false for an operation whose order AWS does not promise; each item is
	// still seen exactly once.
	Ordered bool
	// BadToken, when set, is a token the operation must refuse. Its refusal is
	// the operation's own code, so only that an error came back is checked.
	BadToken string
}

// AssertPaginates walks fetch with limits of one, two and "more than there
// are", and fails unless every one of want comes back exactly once and the
// last page carries no token.
//
// want must have at least three items, or a limit of one and a limit of two
// cannot be told apart from no paging at all.
func AssertPaginates(t testing.TB, fetch PageFunc, want []string, opt PagingOptions) {
	t.Helper()
	if len(want) < 3 {
		t.Fatalf("AssertPaginates needs at least 3 items to mean anything, got %d", len(want))
	}

	for _, limit := range []int{1, 2} {
		seen := map[string]int{}
		var order []string
		tok := ""
		pages := 0
		for {
			pages++
			if pages > len(want)+2 {
				t.Fatalf("limit %d: still returning tokens after %d pages for %d items", limit, pages, len(want))
			}
			ids, next, err := fetch(limit, tok)
			if err != nil {
				t.Fatalf("limit %d, page %d: %v", limit, pages, err)
			}
			if len(ids) > limit {
				t.Fatalf("limit %d: page %d held %d items; the limit was ignored", limit, pages, len(ids))
			}
			if len(ids) == 0 && next != "" {
				t.Fatalf("limit %d: an empty page with a next token", limit)
			}
			for _, id := range ids {
				seen[id]++
				order = append(order, id)
			}
			if next == "" {
				break
			}
			tok = next
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("limit %d: %q was returned %d times", limit, id, n)
			}
		}
		if len(seen) != len(want) {
			t.Errorf("limit %d: saw %d distinct items, want %d (%v)", limit, len(seen), len(want), order)
		}
		if opt.Ordered {
			for i := range want {
				if i >= len(order) || order[i] != want[i] {
					t.Errorf("limit %d: order = %v, want %v", limit, order, want)
					break
				}
			}
		}
		if limit == 1 && pages < len(want) {
			t.Errorf("limit 1 took %d pages for %d items: the limit is not honoured", pages, len(want))
		}
	}

	// A limit above the count is one page and no token.
	ids, next, err := fetch(len(want)+50, "")
	if err != nil {
		t.Fatalf("a large limit: %v", err)
	}
	if next != "" || len(ids) != len(want) {
		t.Errorf("a large limit returned %d items and token %q, want %d and none", len(ids), next, len(want))
	}
	got := append([]string(nil), ids...)
	sort.Strings(got)
	w := append([]string(nil), want...)
	sort.Strings(w)
	for i := range w {
		if i >= len(got) || got[i] != w[i] {
			t.Errorf("a large limit returned %v, want %v", got, w)
			break
		}
	}

	if opt.BadToken != "" {
		if _, _, err := fetch(1, opt.BadToken); err == nil {
			t.Errorf("a token the operation did not issue (%q) was accepted", opt.BadToken)
		}
	}
}
