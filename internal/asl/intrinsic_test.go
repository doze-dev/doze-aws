package asl

import (
	"encoding/json"
	"testing"
)

// evalIn evaluates one intrinsic expression against a data document.
func evalIn(t *testing.T, expr, dataJSON string) (any, *Failure) {
	t.Helper()
	return evalIntrinsic(expr, decodeDoc(json.RawMessage(dataJSON)), map[string]any{
		"Execution": map[string]any{"Name": "e"},
	}, Env{})
}

func wantValue(t *testing.T, expr, dataJSON, wantJSON string) {
	t.Helper()
	v, fail := evalIn(t, expr, dataJSON)
	if fail != nil {
		t.Fatalf("%s failed: %v", expr, fail)
	}
	got := string(encodeDoc(v))
	var norm any
	json.Unmarshal([]byte(wantJSON), &norm)
	want, _ := json.Marshal(norm)
	if got != string(want) {
		t.Fatalf("%s = %s, want %s", expr, got, want)
	}
}

func wantIntrinsicFail(t *testing.T, expr, dataJSON string) {
	t.Helper()
	_, fail := evalIn(t, expr, dataJSON)
	if fail == nil {
		t.Fatalf("%s should have failed", expr)
	}
	if fail.Name != ErrIntrinsicFailure {
		t.Fatalf("%s failed as %s, want %s", expr, fail.Name, ErrIntrinsicFailure)
	}
}

func TestIntrinsics(t *testing.T) {
	data := `{"n": 7, "s": "a,b,,c", "arr": [1, 2, 2, 3], "obj": {"a": 1}, "obj2": {"a": 2, "b": 3}}`
	for _, tc := range []struct{ expr, want string }{
		{`States.Format('{} and {}', $.n, 'x')`, `"7 and x"`},
		{`States.Format('\{literal\}')`, `"{literal}"`},
		{`States.Format('{}', $.obj)`, `"{\"a\":1}"`},
		{`States.StringToJson('{"a": 1}')`, `{"a": 1}`},
		{`States.JsonToString($.obj)`, `"{\"a\":1}"`},
		{`States.Array(1, 'two', $.n, null)`, `[1, "two", 7, null]`},
		{`States.Array()`, `[]`},
		{`States.ArrayPartition($.arr, 3)`, `[[1, 2, 2], [3]]`},
		{`States.ArrayContains($.arr, 2)`, `true`},
		{`States.ArrayContains($.arr, 9)`, `false`},
		{`States.ArrayRange(1, 9, 2)`, `[1, 3, 5, 7, 9]`},
		{`States.ArrayGetItem($.arr, 1)`, `2`},
		{`States.ArrayLength($.arr)`, `4`},
		{`States.ArrayUnique($.arr)`, `[1, 2, 3]`},
		{`States.Base64Encode('hi')`, `"aGk="`},
		{`States.Base64Decode('aGk=')`, `"hi"`},
		{`States.Hash('input data', 'SHA-1')`, `"aaff4a450a104cd177d28d18d74485e8cae074b7"`},
		{`States.JsonMerge($.obj, $.obj2, false)`, `{"a": 2, "b": 3}`},
		{`States.MathAdd($.n, -2)`, `5`},
		{`States.StringSplit($.s, ',')`, `["a", "b", "c"]`},
		{`States.Format('{}', States.ArrayLength($.arr))`, `"4"`},
	} {
		wantValue(t, tc.expr, data, tc.want)
	}
}

func TestIntrinsicFailures(t *testing.T) {
	data := `{"arr": [1]}`
	for _, expr := range []string{
		`States.Format('{} {}', 'one')`,          // more {} than args
		`States.ArrayGetItem($.arr, 5)`,          // out of range
		`States.ArrayRange(1, 9000, 1)`,          // over 1000 items
		`States.Hash('x', 'CRC32')`,              // unknown algorithm
		`States.JsonMerge($.arr, $.arr, false)`,  // not objects
		`States.StringToJson('not json')`,        //
		`States.NoSuchFunction(1)`,               //
		`States.ArrayLength($.missing)`,          // path selects nothing
		`States.Format('{}', $.arr[0], 'extra')`, // fewer {} than args is fine on AWS? No — extra args are allowed
	} {
		if expr == `States.Format('{}', $.arr[0], 'extra')` {
			// Extra arguments beyond the placeholders are accepted.
			wantValue(t, expr, data, `"1"`)
			continue
		}
		wantIntrinsicFail(t, expr, data)
	}
}

func TestIntrinsicUUIDShape(t *testing.T) {
	v, fail := evalIntrinsic("States.UUID()", nil, nil, Env{})
	if fail != nil {
		t.Fatal(fail)
	}
	s := v.(string)
	if len(s) != 36 || s[8] != '-' || s[14] != '4' {
		t.Fatalf("not a v4 UUID: %q", s)
	}
}

func TestIntrinsicMathRandomDeterministic(t *testing.T) {
	env := Env{Rand: func() float64 { return 0.999 }}
	v, fail := evalIntrinsic("States.MathRandom(1, 10)", nil, nil, env)
	if fail != nil {
		t.Fatal(fail)
	}
	if v.(json.Number) != "10" {
		t.Fatalf("MathRandom pinned high = %v, want 10", v)
	}
}
