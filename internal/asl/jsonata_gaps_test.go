package asl

// Do the filled-in functions give the RIGHT answers?
//
// jsonata_dialect_test.go asks only whether each feature produces a value,
// which is the right question for a dependency's function library and the wrong
// one for code written here. These are new implementations of documented
// behaviour, so they are checked against the documented behaviour.

import (
	"encoding/json"
	"testing"
)

func evalTo(t *testing.T, src string, want any) {
	t.Helper()
	sc := &jsonataScope{input: probeInput, context: map[string]any{}}
	got, fail := sc.eval(src)
	if fail != nil {
		t.Errorf("%s failed: %s", src, failText(fail))
		return
	}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Errorf("%s = %s, want %s", src, gj, wj)
	}
}

func evalFails(t *testing.T, src string) {
	t.Helper()
	sc := &jsonataScope{input: probeInput, context: map[string]any{}}
	if v, fail := sc.eval(src); fail == nil {
		gj, _ := json.Marshal(v)
		t.Errorf("%s should have failed, gave %s", src, gj)
	}
}

// ---- $string ----

// The single-argument form is a SHADOWED built-in, so every type it used to
// handle is checked. Getting this wrong would break expressions that have
// nothing to do with the prettify argument this entry exists for.
func TestStringKeepsItsSingleArgumentBehaviour(t *testing.T) {
	evalTo(t, `$string(42)`, "42")
	evalTo(t, `$string(1.5)`, "1.5")
	evalTo(t, `$string(-0.25)`, "-0.25")
	evalTo(t, `$string(true)`, "true")
	evalTo(t, `$string(false)`, "false")
	// A string comes back as itself, NOT as a quoted JSON string. This is the
	// one case where "serialise it as JSON" gives the wrong answer, and the
	// reason jsonataString does not just call json.Marshal on everything.
	evalTo(t, `$string("already")`, "already")
	evalTo(t, `$string("")`, "")
	evalTo(t, `$string(nums)`, "[3,1,2]")
	evalTo(t, `$string(obj)`, `{"x":1,"y":2}`)
	// Composed with itself, which is how a stringified payload gets built.
	evalTo(t, `$string($string(42))`, "42")
}

func TestStringPrettifies(t *testing.T) {
	evalTo(t, `$string(obj, true)`, "{\n  \"x\": 1,\n  \"y\": 2\n}")
	evalTo(t, `$string(nums, true)`, "[\n  3,\n  1,\n  2\n]")
	// false must behave exactly like the one-argument form.
	evalTo(t, `$string(obj, false)`, `{"x":1,"y":2}`)
	evalTo(t, `$string(obj, false) = $string(obj)`, true)
}

// ---- $assert ----

func TestAssert(t *testing.T) {
	// True yields nothing, so $exists sees nothing.
	evalTo(t, `$exists($assert(true, "fine"))`, false)
	evalFails(t, `$assert(false, "boom")`)
	evalFails(t, `$assert(false)`)
	// The message reaches the failure, which is the only reason to pass one.
	sc := &jsonataScope{input: probeInput, context: map[string]any{}}
	_, fail := sc.eval(`$assert(false, "the cart was empty")`)
	if fail == nil {
		t.Fatal("$assert(false, ...) did not fail")
	}
	if got := failText(fail); !contains(got, "the cart was empty") {
		t.Errorf("the assertion message did not reach the failure: %s", got)
	}
	// The idiom it replaces still works, so a definition written either way runs.
	evalFails(t, `$count(nums) > 5 ? true : $error("too few")`)
}

// ---- $formatInteger ----

func TestFormatInteger(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		// Words, the picture AWS's own docs reach for.
		{`$formatInteger(0, "w")`, "zero"},
		{`$formatInteger(12, "w")`, "twelve"},
		{`$formatInteger(21, "w")`, "twenty-one"},
		{`$formatInteger(105, "w")`, "one hundred and five"},
		{`$formatInteger(1234, "w")`, "one thousand two hundred and thirty-four"},
		{`$formatInteger(1000000, "w")`, "one million"},
		{`$formatInteger(-7, "w")`, "minus seven"},
		{`$formatInteger(12, "W")`, "TWELVE"},
		{`$formatInteger(21, "Ww")`, "Twenty-One"},
		// Ordinals, cardinal and worded.
		{`$formatInteger(1, "1;o")`, "1st"},
		{`$formatInteger(2, "1;o")`, "2nd"},
		{`$formatInteger(3, "1;o")`, "3rd"},
		{`$formatInteger(4, "1;o")`, "4th"},
		{`$formatInteger(11, "1;o")`, "11th"},
		{`$formatInteger(12, "1;o")`, "12th"},
		{`$formatInteger(13, "1;o")`, "13th"},
		{`$formatInteger(21, "1;o")`, "21st"},
		{`$formatInteger(12, "w;o")`, "twelfth"},
		{`$formatInteger(21, "w;o")`, "twenty-first"},
		{`$formatInteger(30, "w;o")`, "thirtieth"},
		// Roman and alphabetic.
		{`$formatInteger(4, "I")`, "IV"},
		{`$formatInteger(1984, "I")`, "MCMLXXXIV"},
		{`$formatInteger(4, "i")`, "iv"},
		{`$formatInteger(1, "a")`, "a"},
		{`$formatInteger(26, "a")`, "z"},
		{`$formatInteger(27, "a")`, "aa"},
		{`$formatInteger(27, "A")`, "AA"},
		// Decimal patterns: the 0s set the minimum width, the last separator
		// sets the grouping.
		{`$formatInteger(5, "000")`, "005"},
		{`$formatInteger(12345, "#,##0")`, "12,345"},
		{`$formatInteger(1234567, "#,##0")`, "1,234,567"},
		{`$formatInteger(-12345, "#,##0")`, "-12,345"},
		{`$formatInteger(5, "#")`, "5"},
		// Non-integers round down, per the AWS note about built-ins.
		{`$formatInteger(12.9, "w")`, "twelve"},
	} {
		evalTo(t, tc.expr, tc.want)
	}
}

// A picture this does not understand is an ERROR, not a quiet fallback to
// decimal. A caller who wrote one has a definition that will behave differently
// on AWS, and finding that out locally is the point of running locally.
func TestAnUnknownPictureIsRefused(t *testing.T) {
	evalFails(t, `$formatInteger(1, "Q")`)
	evalFails(t, `$formatInteger(1, "")`)
	evalFails(t, `$formatInteger(1, "1;zzz")`)
	evalFails(t, `$formatInteger(0, "I")`)    // roman has no zero
	evalFails(t, `$formatInteger(4000, "I")`) // nor anything past MMMCMXCIX
	evalFails(t, `$formatInteger(0, "a")`)
}

// ---- $parseInteger ----

func TestParseInteger(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want float64
	}{
		{`$parseInteger("twelve", "w")`, 12},
		{`$parseInteger("twenty-one", "w")`, 21},
		{`$parseInteger("one hundred and five", "w")`, 105},
		{`$parseInteger("one thousand two hundred and thirty-four", "w")`, 1234},
		{`$parseInteger("TWELVE", "W")`, 12},
		{`$parseInteger("Twenty-One", "Ww")`, 21},
		{`$parseInteger("minus seven", "w")`, -7},
		{`$parseInteger("twelfth", "w;o")`, 12},
		{`$parseInteger("twenty-first", "w;o")`, 21},
		{`$parseInteger("thirtieth", "w;o")`, 30},
		{`$parseInteger("IV", "I")`, 4},
		{`$parseInteger("MCMLXXXIV", "I")`, 1984},
		{`$parseInteger("iv", "i")`, 4},
		{`$parseInteger("aa", "a")`, 27},
		{`$parseInteger("AA", "A")`, 27},
		{`$parseInteger("12,345", "#,##0")`, 12345},
		{`$parseInteger("005", "000")`, 5},
		{`$parseInteger("21st", "1;o")`, 21},
	} {
		evalTo(t, tc.expr, tc.want)
	}
}

func TestParseIntegerRefusesNonsense(t *testing.T) {
	evalFails(t, `$parseInteger("banana", "w")`)
	evalFails(t, `$parseInteger("IIII", "I")`) // parseable greedily, not valid
	evalFails(t, `$parseInteger("VX", "I")`)
	evalFails(t, `$parseInteger("a1", "a")`)
	evalFails(t, `$parseInteger("", "w")`)
	evalFails(t, `$parseInteger("x", "#,##0")`)
}

// Round-tripping is the strongest single check on the pair, and it covers the
// carries a table of examples steps over — 99 to 100, 999 to 1000.
func TestFormatAndParseIntegerRoundTrip(t *testing.T) {
	for _, picture := range []string{"w", "W", "Ww", "I", "i", "a", "A", "#,##0"} {
		for _, n := range []int{1, 2, 3, 9, 10, 11, 19, 20, 21, 26, 27, 52, 99,
			100, 101, 115, 999, 1000, 1001, 3999} {
			src := `$parseInteger($formatInteger(` + itoa(n) + `, "` + picture + `"), "` + picture + `")`
			evalTo(t, src, float64(n))
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
