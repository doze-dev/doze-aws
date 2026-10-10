package dozetest

import (
	"errors"
	"runtime"
	"strconv"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awspage"
)

// The helper is itself held to catching the failures it exists for: each fake
// below is a list operation that is wrong in one way, and AssertPaginates must
// fail on it.
type pagingRecorder struct {
	testing.TB
	failed bool
}

func (r *pagingRecorder) Errorf(string, ...any) { r.failed = true }
func (r *pagingRecorder) Fatalf(string, ...any) { r.failed = true; runtime.Goexit() }
func (r *pagingRecorder) Helper()               {}

// failsOn runs AssertPaginates on its own goroutine, because a Fatalf ends
// the goroutine it was called on.
func failsOn(t *testing.T, fn PageFunc, want []string, opt PagingOptions) bool {
	t.Helper()
	r := &pagingRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		AssertPaginates(r, fn, want, opt)
	}()
	<-done
	return r.failed
}

var pagingItems = []string{"a", "b", "c", "d", "e"}

func clip(limit int) int { return min(limit, len(pagingItems)) }

func good(limit int, token string) ([]string, string, error) {
	pg, next, err := awspage.Slice(pagingItems, func(s string) string { return s }, token, limit)
	if err != nil {
		return nil, "", errors.New("bad token")
	}
	return pg, next, nil
}

func TestAssertPaginatesPassesAGoodListing(t *testing.T) {
	if failsOn(t, good, pagingItems, PagingOptions{Ordered: true, BadToken: "not-ours"}) {
		t.Fatal("a correct listing was failed")
	}
}

func TestAssertPaginatesCatchesTheRealFailures(t *testing.T) {
	cases := map[string]PageFunc{
		// What most list operations in the tree did: everything, no token.
		"ignores the limit": func(limit int, token string) ([]string, string, error) {
			return pagingItems, "", nil
		},
		"repeats its first page": func(limit int, token string) ([]string, string, error) {
			if token == "" {
				return pagingItems[:clip(limit)], "again", nil
			}
			return pagingItems[:clip(limit)], "", nil
		},
		"a token that never ends": func(limit int, token string) ([]string, string, error) {
			return pagingItems[:1], "more", nil
		},
		"drops the last page": func(limit int, token string) ([]string, string, error) {
			n, _ := strconv.Atoi(token)
			if n+limit >= len(pagingItems) {
				return nil, "", nil
			}
			return pagingItems[n : n+limit], strconv.Itoa(n + limit), nil
		},
		"accepts a token it did not issue": func(limit int, token string) ([]string, string, error) {
			if token == "not-ours" {
				token = ""
			}
			return good(limit, token)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if !failsOn(t, fn, pagingItems, PagingOptions{BadToken: "not-ours"}) {
				t.Fatal("not caught")
			}
		})
	}
}
