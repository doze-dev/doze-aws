// Package awspage is limit-and-token paging over a sorted listing, once.
//
// Most services here answered a list operation with everything and no token,
// whatever limit the caller sent: SQS ListQueues, SSM's three lists, SNS's
// three, EventBridge, Kinesis, CloudWatch, IAM, CloudFormation, DynamoDB's
// ListTables, both API Gateway planes. An SDK paginator tolerates that. A
// caller that does not page, written against AWS, gets the first page there
// and every item here, and finds out on deploy; a test that asks for one item
// at a time and counts the pages passes here and means nothing.
//
// # The cursor is a key, not an offset
//
// An offset token (Step Functions' page() was one) skips or repeats an item if
// the listing changes between two pages. Here the token carries the last key
// returned and the next page is whatever sorts after it, so a deletion or an
// insertion between pages moves nothing. It is opaque on purpose: a caller must
// treat it as a string, and a token this package did not mint is refused
// rather than guessed at.
//
// The listing must be sorted by key, ascending, and keys must be unique. The
// caller owns that; a test helper (dozetest.AssertPaginates) drives any list
// operation through a limit of one and checks every item is seen exactly once,
// in order.
package awspage

import (
	"encoding/base64"
	"errors"
	"sort"
	"strings"
)

// ErrBadToken is returned for a token this package did not mint. A token minted
// for ANOTHER listing is not detected: it decodes to a key, and the page starts
// after wherever that key would sort. A service maps the error to its own code
// (InvalidNextToken, InvalidToken, ValidationException).
var ErrBadToken = errors.New("awspage: invalid pagination token")

const tokenPrefix = "dz1:"

// Token encodes the key an item sorts at, for use as the next page's cursor.
func Token(lastKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tokenPrefix + lastKey))
}

// After decodes a token to the key the page must start after. The empty token
// is the start of the listing and is not an error.
func After(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(b), tokenPrefix) {
		return "", ErrBadToken
	}
	return string(b)[len(tokenPrefix):], nil
}

// Limit is the page size to use: the caller's value if it is positive and no
// more than max, otherwise def. Whether a value above max is refused or
// clamped is the operation's own rule, applied before this is called; AWS does
// both, depending on the operation.
func Limit(requested, def, max int) int {
	switch {
	case requested <= 0:
		return def
	case max > 0 && requested > max:
		return max
	}
	return requested
}

// Slice returns the page of items that starts after token, at most limit long,
// and the token for the next page, which is empty when this is the last.
//
// items must be sorted by key ascending. A limit of zero or less returns
// everything after the token. A token that names a key no longer in the
// listing is fine: the page starts at whatever sorts after it.
func Slice[T any](items []T, key func(T) string, token string, limit int) (page []T, next string, err error) {
	after, err := After(token)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if token != "" {
		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}
	rest := items[start:]
	if limit <= 0 || len(rest) <= limit {
		return rest, "", nil
	}
	page = rest[:limit]
	return page, Token(key(page[len(page)-1])), nil
}
