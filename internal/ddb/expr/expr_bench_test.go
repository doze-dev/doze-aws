package expr

// The expression engine runs on every DynamoDB request that carries one —
// which is most of them: a Query has a key condition and usually a filter, an
// UpdateItem has an update expression, and a conditional write has both.
//
// Parse and Eval are measured apart because they are paid differently:
// parsing happens once per request, evaluation once per item scanned. A Scan
// over a thousand items pays Eval a thousand times and Parse once, so the
// per-item number is the one that decides how a big table behaves.

import (
	"encoding/json"
	"testing"

	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

func benchValues(tb testing.TB, wire string) map[string]item.Value {
	tb.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wire), &m); err != nil {
		tb.Fatal(err)
	}
	out := map[string]item.Value{}
	for k, raw := range m {
		v, aerr := item.FromJSON(raw)
		if aerr != nil {
			tb.Fatal(aerr)
		}
		out[k] = v
	}
	return out
}

func benchItem(tb testing.TB) item.Item {
	tb.Helper()
	return item.Item(benchValues(tb, `{
		"pk": {"S": "customer#77"},
		"sk": {"S": "order#1029"},
		"total": {"N": "149.5"},
		"currency": {"S": "GBP"},
		"tier": {"S": "gold"},
		"tags": {"SS": ["priority", "gift"]},
		"meta": {"M": {"region": {"S": "us-east-1"}, "retries": {"N": "0"}}}
	}`))
}

const filterExpr = `total > :min AND begins_with(sk, :prefix) AND ` +
	`attribute_exists(tier) AND NOT contains(tags, :excluded)`

func benchEnv(tb testing.TB) *Env {
	tb.Helper()
	return NewEnv(nil, benchValues(tb, `{
		":min": {"N": "100"},
		":prefix": {"S": "order#"},
		":excluded": {"S": "cancelled"}
	}`))
}

// Parse: once per request.
//
// The Env is built once, outside the loop. Building it inside measured the
// JSON decode of the expression-attribute values as if it were parsing cost,
// which roughly tripled the number and attributed it to the wrong code.
func BenchmarkParseCondition(b *testing.B) {
	env := benchEnv(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, aerr := ParseCondition(filterExpr, env); aerr != nil {
			b.Fatal(aerr)
		}
	}
}

// Eval: once per item scanned, and the number that decides how a Scan over a
// large table behaves.
func BenchmarkEvalCondition(b *testing.B) {
	cond, aerr := ParseCondition(filterExpr, benchEnv(b))
	if aerr != nil {
		b.Fatal(aerr)
	}
	it := benchItem(b)
	b.ReportAllocs()
	for b.Loop() {
		ok, aerr := cond.Eval(it)
		if aerr != nil || !ok {
			b.Fatalf("the fixture item should match (%v)", aerr)
		}
	}
}

func BenchmarkParseUpdate(b *testing.B) {
	env := NewEnv(
		map[string]string{"#t": "total"},
		benchValues(b, `{":inc": {"N": "1"}, ":now": {"S": "2026-09-10"}}`),
	)
	b.ReportAllocs()
	for b.Loop() {
		if _, aerr := ParseUpdate("SET #t = #t + :inc, updated = :now REMOVE tier", env); aerr != nil {
			b.Fatal(aerr)
		}
	}
}

func BenchmarkParseKeyCondition(b *testing.B) {
	env := NewEnv(nil, benchValues(b, `{":pk": {"S": "customer#77"}, ":sk": {"S": "order#"}}`))
	b.ReportAllocs()
	for b.Loop() {
		if _, aerr := ParseKeyCondition("pk = :pk AND begins_with(sk, :sk)", env); aerr != nil {
			b.Fatal(aerr)
		}
	}
}
