package dozetest

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awspage"
)

// say records what AssertPaginates said, so a test can hold the helper to
// failing for the right reason and not merely to failing.
type say struct {
	testing.TB
	msgs []string
}

func (r *say) Errorf(f string, a ...any) { r.msgs = append(r.msgs, fmt.Sprintf(f, a...)) }
func (r *say) Fatalf(f string, a ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(f, a...))
	runtime.Goexit()
}
func (r *say) Helper() {}

// good2 pages any list correctly, as awspage does.
func good2(items []string, limit int, token string) ([]string, string, error) {
	pg, next, err := awspage.Slice(items, func(s string) string { return s }, token, limit)
	return pg, next, err
}

func saysOn(t *testing.T, fn PageFunc, want []string, opt PagingOptions) string {
	t.Helper()
	r := &say{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		AssertPaginates(r, fn, want, opt)
	}()
	<-done
	return strings.Join(r.msgs, " | ")
}

func TestAssertPaginatesFailsForTheReasonItGives(t *testing.T) {
	// An empty page that still has a next token is a page nobody can use.
	empty := func(limit int, token string) ([]string, string, error) { return nil, "more", nil }
	if got := saysOn(t, empty, pagingItems, PagingOptions{}); !strings.Contains(got, "an empty page with a next token") {
		t.Errorf("an empty page with a token: said %q", got)
	}

	// A token that never ends is cut off after len(want)+2 pages and says so:
	// five items allow seven pages, so the eighth is the one that fails.
	forever := func(limit int, token string) ([]string, string, error) { return pagingItems[:1], "more", nil }
	if got := saysOn(t, forever, pagingItems, PagingOptions{}); !strings.Contains(got, "still returning tokens after 8 pages for 5 items") {
		t.Errorf("a token that never ends: said %q", got)
	}

	// With the order asked for, a listing that drops its last item says its
	// order was wrong. (It must not index past the end of what it saw.)
	dropsLast := func(limit int, token string) ([]string, string, error) {
		items := pagingItems[:len(pagingItems)-1]
		pg, next, err := good2(items, limit, token)
		return pg, next, err
	}
	if got := saysOn(t, dropsLast, pagingItems, PagingOptions{Ordered: true}); !strings.Contains(got, "order =") {
		t.Errorf("a listing that drops an item, asked to be in order: said %q", got)
	}

	// Exactly three items is the least that means anything, and passes.
	three := pagingItems[:3]
	ok := func(limit int, token string) ([]string, string, error) { return good2(three, limit, token) }
	if got := saysOn(t, ok, three, PagingOptions{Ordered: true}); got != "" {
		t.Errorf("a correct listing of three items was failed: %q", got)
	}
	// Two is too few to tell a limit of one from no paging at all.
	if got := saysOn(t, ok, pagingItems[:2], PagingOptions{}); !strings.Contains(got, "needs at least 3 items") {
		t.Errorf("two items: said %q", got)
	}
}
