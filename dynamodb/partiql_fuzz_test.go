package dynamodb

import (
	"encoding/json"
	"testing"
)

// FuzzPartiQL feeds arbitrary statements through the tokenizer, parser, and
// value binder, asserting none of them panic — the parser must reject malformed
// input with an error, never a crash.
func FuzzPartiQL(f *testing.F) {
	seeds := []string{
		`SELECT * FROM "t" WHERE "pk" = ?`,
		`SELECT * FROM "t" WHERE pk = 'v' AND sk = ?`,
		`INSERT INTO "t" VALUE {'pk': 'x', 'n': 5, 'b': true}`,
		`UPDATE "t" SET "a" = 'b', "c" = ? WHERE "pk" = ?`,
		`DELETE FROM "t" WHERE "pk" = ?`,
		``, `{`, `SELECT`, `INSERT INTO`, `UPDATE x SET`, `'unterminated`,
		`SELECT * FROM "t" WHERE`, `VALUE {'k'}`, `?????`, `= = =`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, stmt string) {
		st, aerr := parsePartiQL(stmt)
		if aerr != nil || st == nil {
			return
		}
		// Exercise binding + translation helpers against a few parameter counts.
		for _, n := range []int{0, 1, 4} {
			b := &paramBinder{params: makeParams(n)}
			_, _ = st.where.attributeMap(b)
			_, _ = st.values.attributeMap(b)
			_, _, _, _ = st.set.updateExpr(b)
		}
	})
}

func makeParams(n int) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = json.RawMessage(`{"S":"x"}`)
	}
	return out
}
