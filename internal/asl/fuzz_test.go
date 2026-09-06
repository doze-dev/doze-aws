package asl

import (
	"encoding/json"
	"testing"
	"time"
)

// Fuzz targets for the three hand-written parsers in the language. The
// property is the one every parser here owes: any input is either accepted
// or refused with an error, and nothing panics — a definition arrives on the
// wire from a deploy tool, and a panic inside CreateStateMachine takes the
// whole gateway request with it.

func FuzzValidateDefinition(f *testing.F) {
	f.Add([]byte(`{"StartAt":"A","States":{"A":{"Type":"Succeed"}}}`))
	f.Add([]byte(`{"StartAt":"C","States":{"C":{"Type":"Choice","Choices":[{"Variable":"$.x","NumericEquals":1,"Next":"C"}]}}}`))
	f.Add([]byte(`{"StartAt":"P","States":{"P":{"Type":"Parallel","End":true,"Branches":[{"StartAt":"X","States":{"X":{"Type":"Pass","End":true}}}]}}}`))
	f.Add([]byte(`{"StartAt":"M","States":{"M":{"Type":"Map","End":true,"ItemProcessor":{"StartAt":"I","States":{"I":{"Type":"Pass","End":true}}}}}}`))
	f.Add([]byte(`{"StartAt":"T","States":{"T":{"Type":"Task","Resource":"arn:aws:states:::lambda:invoke","Parameters":{"FunctionName":"f","Payload.$":"States.Format('{}', $.a)"},"Retry":[{"ErrorEquals":["States.ALL"]}],"Catch":[{"ErrorEquals":["States.ALL"],"Next":"T"}],"End":true}}}`))
	f.Add([]byte(`{"StartAt":"W","States":{"W":{"Type":"Wait","TimestampPath":"$.at","End":true}}}`))
	f.Add([]byte(`{"QueryLanguage":"JSONata","StartAt":"A","States":{"A":{"Type":"Pass","Output":"{% $states.input %}","End":true}}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"States":{}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, rep := ValidateDefinition(raw)
		if rep.OK() && d == nil {
			t.Fatal("accepted with no definition")
		}
		if !rep.OK() && rep.Error() == "" {
			t.Fatal("refused with no diagnostic")
		}
	})
}

func FuzzParsePath(f *testing.F) {
	for _, s := range []string{
		"$", "$.a", "$.a.b", "$['a']", "$[\"a b\"]", "$.a[0]", "$.a[-1]", "$[0][1]",
		"$$.Execution.Name", "$$.Map.Item.Value", "$.a[*]", "$..b", "$.a[?(@.x)]",
		"$.a[0:2]", "", "$.", ".a", "$['", "$[", "$.a[", "$.a[b]", "$.é",
	} {
		f.Add(s)
	}
	doc := map[string]any{"a": []any{map[string]any{"x": 1.0}, "s"}, "b": map[string]any{"c": true}}
	ctx := map[string]any{"Execution": map[string]any{"Name": "e"}}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePath(s)
		if err != nil {
			return
		}
		if p.Raw != s {
			t.Fatalf("Raw = %q, want %q", p.Raw, s)
		}
		// A parsed path evaluates without panicking on any document.
		pathValue(s, "fuzz", doc, ctx)
		selectPath(doc, ctx, &s, "fuzz")
	})
}

func FuzzIntrinsic(f *testing.F) {
	for _, s := range []string{
		`States.Format('{} and {}', $.n, 'x')`, `States.Format('\{literal\}')`, `States.Array(1, 'two', $.n, null)`,
		`States.ArrayPartition($.arr, 3)`, `States.ArrayRange(1, 9, 2)`, `States.JsonMerge($.obj, $.obj, false)`,
		`States.StringSplit($.s, ',')`, `States.Hash('x', 'SHA-256')`, `States.Base64Decode('aGk=')`,
		`States.MathAdd($.n, -2)`, `States.UUID()`, `States.MathRandom(1, 10)`,
		`States.Format('{}', States.ArrayLength($.arr))`, `States.NoSuch(1)`, `States.Format(`, `States.Format('unterminated`,
		`States.Format('it''s')`, `States.Array(`, `)`, `States.JsonToString($$.Execution)`, `$.n`, ``,
	} {
		f.Add(s)
	}
	data := map[string]any{"n": 7.0, "s": "a,b,,c", "arr": []any{1.0, 2.0}, "obj": map[string]any{"a": 1.0}}
	ctx := map[string]any{"Execution": map[string]any{"Name": "e", "StartTime": "2024-01-01T00:00:00Z"}}
	env := Env{Now: time.Unix(0, 0), Rand: func() float64 { return 0.5 }, NewToken: func() string { return "tok" }}
	f.Fuzz(func(t *testing.T, expr string) {
		v, fail := evalIntrinsic(expr, data, ctx, env)
		if fail != nil {
			if fail.Name == "" {
				t.Fatalf("failure with no name for %q", expr)
			}
			return
		}
		// Whatever it produced must be a JSON value the engine can persist.
		if _, err := json.Marshal(v); err != nil {
			t.Fatalf("%q produced an unencodable value: %v", expr, err)
		}
	})
}
