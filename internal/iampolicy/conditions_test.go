package iampolicy

import "testing"

func ctxOf(kv ...string) map[string][]string {
	m := map[string][]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = append(m[kv[i]], kv[i+1])
	}
	return m
}

// Every operator, on both sides of its boundary. Condition evaluation decides
// whether a Deny fires, so an operator that is quietly wrong is an access
// decision that is quietly wrong.
func TestEveryConditionOperatorOnBothSidesOfItsBoundary(t *testing.T) {
	cases := []struct {
		name string
		op   string
		key  string
		want []string
		ctx  map[string][]string
		pass bool
	}{
		{"StringEquals match", "StringEquals", "k", []string{"a"}, ctxOf("k", "a"), true},
		{"StringEquals other value of several", "StringEquals", "k", []string{"a", "b"}, ctxOf("k", "b"), true},
		{"StringEquals none", "StringEquals", "k", []string{"a", "b"}, ctxOf("k", "c"), false},
		{"StringEquals is case sensitive", "StringEquals", "k", []string{"a"}, ctxOf("k", "A"), false},
		{"StringEqualsIgnoreCase", "StringEqualsIgnoreCase", "k", []string{"a"}, ctxOf("k", "A"), true},
		{"StringLike glob", "StringLike", "k", []string{"logs/*"}, ctxOf("k", "logs/2026/x"), true},
		{"StringLike miss", "StringLike", "k", []string{"logs/*"}, ctxOf("k", "other/x"), false},
		{"StringLike question mark", "StringLike", "k", []string{"a?c"}, ctxOf("k", "abc"), true},
		{"ArnLike", "ArnLike", "k", []string{"arn:aws:s3:::b/*"}, ctxOf("k", "arn:aws:s3:::b/key"), true},
		{"ArnEquals miss", "ArnEquals", "k", []string{"arn:aws:s3:::b"}, ctxOf("k", "arn:aws:s3:::c"), false},

		// A negated operator with several values means "none of them".
		{"StringNotEquals none of several", "StringNotEquals", "k", []string{"a", "b"}, ctxOf("k", "c"), true},
		{"StringNotEquals first of several", "StringNotEquals", "k", []string{"a", "b"}, ctxOf("k", "a"), false},
		{"StringNotEquals last of several", "StringNotEquals", "k", []string{"a", "b"}, ctxOf("k", "b"), false},
		{"StringNotLike excluded", "StringNotLike", "k", []string{"x*", "y*"}, ctxOf("k", "yes"), false},
		{"StringNotLike included", "StringNotLike", "k", []string{"x*", "y*"}, ctxOf("k", "zed"), true},
		{"ArnNotLike excluded", "ArnNotLike", "k", []string{"arn:aws:iam::1:*", "arn:aws:iam::2:*"}, ctxOf("k", "arn:aws:iam::2:role/r"), false},
		{"NotIpAddress inside either", "NotIpAddress", "k", []string{"10.0.0.0/8", "192.168.0.0/16"}, ctxOf("k", "192.168.1.1"), false},
		{"NotIpAddress outside both", "NotIpAddress", "k", []string{"10.0.0.0/8", "192.168.0.0/16"}, ctxOf("k", "8.8.8.8"), true},
		{"NumericNotEquals either", "NumericNotEquals", "k", []string{"1", "2"}, ctxOf("k", "2"), false},

		{"Bool true", "Bool", "k", []string{"true"}, ctxOf("k", "true"), true},
		{"Bool false vs true", "Bool", "k", []string{"true"}, ctxOf("k", "false"), false},
		{"Bool case-insensitive", "Bool", "k", []string{"TRUE"}, ctxOf("k", "true"), true},

		{"NumericEquals", "NumericEquals", "k", []string{"5"}, ctxOf("k", "5"), true},
		{"NumericEquals miss", "NumericEquals", "k", []string{"5"}, ctxOf("k", "6"), false},
		{"NumericLessThan below", "NumericLessThan", "k", []string{"10"}, ctxOf("k", "9"), true},
		{"NumericLessThan at", "NumericLessThan", "k", []string{"10"}, ctxOf("k", "10"), false},
		{"NumericLessThanEquals at", "NumericLessThanEquals", "k", []string{"10"}, ctxOf("k", "10"), true},
		{"NumericLessThanEquals above", "NumericLessThanEquals", "k", []string{"10"}, ctxOf("k", "11"), false},
		{"NumericGreaterThan above", "NumericGreaterThan", "k", []string{"10"}, ctxOf("k", "11"), true},
		{"NumericGreaterThan at", "NumericGreaterThan", "k", []string{"10"}, ctxOf("k", "10"), false},
		{"NumericGreaterThanEquals at", "NumericGreaterThanEquals", "k", []string{"10"}, ctxOf("k", "10"), true},
		{"NumericGreaterThanEquals below", "NumericGreaterThanEquals", "k", []string{"10"}, ctxOf("k", "9"), false},
		{"Numeric decimals", "NumericLessThan", "k", []string{"2.5"}, ctxOf("k", "2.25"), true},
		{"Numeric not a number", "NumericEquals", "k", []string{"5"}, ctxOf("k", "five"), false},

		{"DateLessThan before", "DateLessThan", "k", []string{"2026-06-01T00:00:00Z"}, ctxOf("k", "2026-05-31T23:59:59Z"), true},
		{"DateLessThan at", "DateLessThan", "k", []string{"2026-06-01T00:00:00Z"}, ctxOf("k", "2026-06-01T00:00:00Z"), false},
		{"DateGreaterThan after", "DateGreaterThan", "k", []string{"2026-06-01T00:00:00Z"}, ctxOf("k", "2026-06-01T00:00:01Z"), true},
		{"DateEquals same instant", "DateEquals", "k", []string{"2026-06-01"}, ctxOf("k", "2026-06-01T00:00:00Z"), true},
		{"DateLessThanEquals at", "DateLessThanEquals", "k", []string{"2026-06-01"}, ctxOf("k", "2026-06-01"), true},
		{"DateGreaterThanEquals before", "DateGreaterThanEquals", "k", []string{"2026-06-02"}, ctxOf("k", "2026-06-01"), false},
		{"DateNotEquals differs", "DateNotEquals", "k", []string{"2026-06-01"}, ctxOf("k", "2026-06-02"), true},
		{"Date as epoch seconds", "DateGreaterThan", "k", []string{"2026-01-01T00:00:00Z"}, ctxOf("k", "1790000000"), true},
		{"Date unparsable", "DateLessThan", "k", []string{"2026-06-01"}, ctxOf("k", "yesterday"), false},

		{"IpAddress inside CIDR", "IpAddress", "k", []string{"10.0.0.0/8"}, ctxOf("k", "10.2.3.4"), true},
		{"IpAddress outside CIDR", "IpAddress", "k", []string{"10.0.0.0/8"}, ctxOf("k", "11.0.0.1"), false},
		{"IpAddress exact", "IpAddress", "k", []string{"203.0.113.9"}, ctxOf("k", "203.0.113.9"), true},
		{"IpAddress v6 CIDR", "IpAddress", "k", []string{"2001:db8::/32"}, ctxOf("k", "2001:db8::1"), true},
		{"IpAddress not an address", "IpAddress", "k", []string{"10.0.0.0/8"}, ctxOf("k", "ten"), false},

		{"unknown operator never matches", "Frobnicate", "k", []string{"a"}, ctxOf("k", "a"), false},
		{"key lookup ignores case", "StringEquals", "AWS:Username", []string{"mina"}, ctxOf("aws:username", "mina"), true},
	}
	for _, c := range cases {
		if got := conditionMatches(c.op, c.key, c.want, c.ctx); got != c.pass {
			t.Errorf("%s: %s %v against %v = %v, want %v", c.name, c.op, c.want, c.ctx, got, c.pass)
		}
	}
}

func TestConditionAbsentKeys(t *testing.T) {
	absent := ctxOf()
	for _, c := range []struct {
		op   string
		pass bool
	}{
		{"StringEquals", false},
		{"StringLike", false},
		{"NumericLessThan", false},
		{"IpAddress", false},
		{"Bool", false},
		// A negated operator holds when the key is not there at all.
		{"StringNotEquals", true},
		{"StringNotLike", true},
		{"ArnNotLike", true},
		{"NotIpAddress", true},
		{"NumericNotEquals", true},
		{"DateNotEquals", true},
		// ...IfExists makes any operator pass on absence.
		{"StringEqualsIfExists", true},
		{"NumericLessThanIfExists", true},
	} {
		if got := conditionMatches(c.op, "k", []string{"v"}, absent); got != c.pass {
			t.Errorf("%s on an absent key = %v, want %v", c.op, got, c.pass)
		}
	}
	// IfExists still tests a key that is present.
	if conditionMatches("StringEqualsIfExists", "k", []string{"a"}, ctxOf("k", "b")) {
		t.Error("StringEqualsIfExists passed a present, different value")
	}
	if !conditionMatches("StringEqualsIfExists", "k", []string{"a"}, ctxOf("k", "a")) {
		t.Error("StringEqualsIfExists failed a present, equal value")
	}
}

func TestConditionNull(t *testing.T) {
	if !conditionMatches("Null", "k", []string{"true"}, ctxOf()) {
		t.Error("Null true on an absent key should hold")
	}
	if conditionMatches("Null", "k", []string{"true"}, ctxOf("k", "x")) {
		t.Error("Null true on a present key should not hold")
	}
	if !conditionMatches("Null", "k", []string{"false"}, ctxOf("k", "x")) {
		t.Error("Null false on a present key should hold")
	}
	if conditionMatches("Null", "k", []string{"false"}, ctxOf()) {
		t.Error("Null false on an absent key should not hold")
	}
}

// ForAllValues: every value the request carries must match; ForAnyValue: one is
// enough. An unquantified operator over several values passes on a single match.
func TestConditionQuantifiers(t *testing.T) {
	tags := ctxOf("k", "a", "k", "b", "k", "z")
	if conditionMatches("ForAllValues:StringEquals", "k", []string{"a", "b"}, tags) {
		t.Error("ForAllValues passed with a value (z) outside the allowed set")
	}
	if !conditionMatches("ForAllValues:StringEquals", "k", []string{"a", "b", "z"}, tags) {
		t.Error("ForAllValues failed with every value allowed")
	}
	if !conditionMatches("ForAnyValue:StringEquals", "k", []string{"b"}, tags) {
		t.Error("ForAnyValue failed with one matching value")
	}
	if conditionMatches("ForAnyValue:StringEquals", "k", []string{"q"}, tags) {
		t.Error("ForAnyValue passed with no matching value")
	}
}

func TestConditionBlockIsANDOfOperatorsAndKeys(t *testing.T) {
	block := conditionBlock{
		"StringEquals": {"a": {"1"}, "b": {"2"}},
		"Bool":         {"c": {"true"}},
	}
	if !conditionsMatch(block, ctxOf("a", "1", "b", "2", "c", "true")) {
		t.Error("every condition holds but the block does not")
	}
	if conditionsMatch(block, ctxOf("a", "1", "b", "9", "c", "true")) {
		t.Error("one key fails but the block holds")
	}
	if conditionsMatch(block, ctxOf("a", "1", "b", "2", "c", "false")) {
		t.Error("one operator fails but the block holds")
	}
	if !conditionsMatch(nil, ctxOf()) {
		t.Error("an empty block should hold")
	}
}
