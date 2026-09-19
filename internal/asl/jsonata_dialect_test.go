package asl

// Which of JSONata's language does a JSONata state actually get?
//
// The interpreter does not implement JSONata; it embeds blues/jsonata-go, which
// is an honest partial port. So "doze-aws supports JSONata" is a claim about a
// dependency, and until this test it was an unmeasured one — the package said
// the AWS additions were present, which is true and is the small half, and said
// nothing about the library underneath, which is the large half.
//
// This runs the documented JSONata library and the syntax features against the
// real evaluation path — jsonataScope.eval, with the AWS extensions registered
// — and freezes the result. A name in unsupported is a promise that it does NOT
// work here; a name absent from it must work. Both directions fail the test, so
// a dependency bump that fixes something is as loud as a regression that breaks
// something, and docs/api-support/stepfunctions.md can cite a measured list
// instead of a shrug.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// probeInput is what every expression below evaluates against.
var probeInput = map[string]any{
	"nums":  []any{3.0, 1.0, 2.0},
	"str":   "hello world",
	"obj":   map[string]any{"x": 1.0, "y": 2.0},
	"items": []any{map[string]any{"n": "a", "v": 1.0}, map[string]any{"n": "b", "v": 2.0}},
	"n":     7.0,
}

// dialectProbes are expressions that must each produce a value. They are
// chosen so that anything unimplemented fails loudly rather than quietly
// returning the input: every one of them transforms something.
var dialectProbes = map[string]string{
	// ---- string ----
	"$string":             `$string(42)`,
	"$length":             `$length(str)`,
	"$substring":          `$substring(str, 0, 5)`,
	"$substringBefore":    `$substringBefore(str, " ")`,
	"$substringAfter":     `$substringAfter(str, " ")`,
	"$uppercase":          `$uppercase(str)`,
	"$lowercase":          `$lowercase("ABC")`,
	"$trim":               `$trim("  x  ")`,
	"$pad":                `$pad("x", 3, "-")`,
	"$contains":           `$contains(str, "world")`,
	"$split":              `$split(str, " ")`,
	"$join":               `$join(["a","b"], ",")`,
	"$match":              `$match(str, /o/)`,
	"$replace":            `$replace(str, "world", "there")`,
	"$eval":               `$eval("[1,2]")`,
	"$base64encode":       `$base64encode("hi")`,
	"$base64decode":       `$base64decode("aGk=")`,
	"$encodeUrlComponent": `$encodeUrlComponent("a b")`,
	"$encodeUrl":          `$encodeUrl("http://x/a b")`,
	"$decodeUrlComponent": `$decodeUrlComponent("a%20b")`,
	"$decodeUrl":          `$decodeUrl("http://x/a%20b")`,

	// ---- numeric ----
	"$number":        `$number("42")`,
	"$abs":           `$abs(-1)`,
	"$floor":         `$floor(1.5)`,
	"$ceil":          `$ceil(1.5)`,
	"$round":         `$round(1.5)`,
	"$power":         `$power(2, 3)`,
	"$sqrt":          `$sqrt(4)`,
	"$formatNumber":  `$formatNumber(12345.6, "#,###.00")`,
	"$formatBase":    `$formatBase(255, 16)`,
	"$formatInteger": `$formatInteger(12, "w")`,
	"$parseInteger":  `$parseInteger("twelve", "w")`,

	// ---- aggregation ----
	"$sum":     `$sum(nums)`,
	"$max":     `$max(nums)`,
	"$min":     `$min(nums)`,
	"$average": `$average(nums)`,

	// ---- boolean ----
	"$boolean": `$boolean("x")`,
	"$not":     `$not(false)`,
	"$exists":  `$exists(str)`,

	// ---- array ----
	"$count":    `$count(nums)`,
	"$append":   `$append(nums, [4])`,
	"$sort":     `$sort(nums)`,
	"$reverse":  `$reverse(nums)`,
	"$shuffle":  `$count($shuffle(nums))`,
	"$distinct": `$distinct([1,1,2])`,
	"$zip":      `$zip([1,2],[3,4])`,

	// ---- object ----
	"$keys":   `$keys(obj)`,
	"$lookup": `$lookup(obj, "x")`,
	"$spread": `$spread(obj)`,
	"$merge":  `$merge([obj, {"z": 3}])`,
	"$sift":   `$sift(obj, function($v) { $v > 1 })`,
	"$each":   `$each(obj, function($v, $k) { $k })`,
	"$type":   `$type(str)`,
	// These two exist to RAISE, so there is no value probe for them. They are
	// classified by how they fail instead — see meansAbsent. Probing them with
	// $exists($error), which was the first attempt, asserts nothing at all:
	// it answers false for a missing function rather than failing, so the
	// probe passed identically whether the function was there or not.
	"$error":  `$error("boom")`,
	"$assert": `$assert(false, "boom")`,

	// ---- date and time ----
	"$now":        `$length($now())`,
	"$millis":     `$millis() > 0`,
	"$fromMillis": `$fromMillis(1500000000000)`,
	"$toMillis":   `$toMillis("2017-07-14T02:40:00.000Z")`,

	// ---- higher order ----
	"$map":    `$map(nums, function($v) { $v * 2 })`,
	"$filter": `$filter(nums, function($v) { $v > 1 })`,
	"$reduce": `$reduce(nums, function($a, $b) { $a + $b })`,
	"$single": `$single(nums, function($v) { $v = 3 })`,

	// ---- syntax, not functions ----
	"lambda":           `(function($x) { $x + 1 })(1)`,
	"chain operator":   `nums ~> $sum()`,
	"order-by":         `items^(v)`,
	"parent operator":  `items.%.n`,
	"transform":        `obj ~> |$|{"x": 9}|`,
	"conditional":      `n > 1 ? "big" : "small"`,
	"variable binding": `($t := 2; n * $t)`,
	"object construct": `{"k": n}`,
	"array construct":  `[n, n]`,
	"range predicate":  `nums[[0..1]]`,
	"wildcard":         `obj.*`,
	"descendant":       `**.n`,
	"string concat":    `str & "!"`,
	"in operator":      `"a" in items.n`,

	// ---- the five AWS adds, which are this repo's own code ----
	"$partition": `$partition([1,2,3,4], 2)`,
	"$range":     `$range(0, 3, 1)`,
	"$hash":      `$hash("x", "SHA-256")`,
	"$uuid":      `$length($uuid())`,
	"$random":    `$random(1) >= 0`,
	"$parse":     `$parse("{\"a\":1}")`,
	"$states":    `$states.input.n`,
}

// unsupported is the measured gap: every probe above that does NOT work on the
// embedded library. Frozen, so a dependency bump that closes one of these fails
// here and gets noticed rather than silently widening what is claimed.
var unsupported = map[string]string{
	"$eval": "dynamic evaluation of an expression string; the port omits it, " +
		"and a definition that needs it is doing something AWS's own docs " +
		"discourage inside a state machine",
	"$formatInteger": "integer to words or roman numerals (the ICU picture " +
		"strings); the port omits it",
	"$parseInteger": "the inverse of $formatInteger; omitted with it",
	// Found only because the first version of its probe was vacuous. $exists()
	// on a missing function answers false rather than failing, so $error and
	// $assert both "passed" while one of them was not there at all.
	"$assert": "raises when a condition is false; the port omits it, though " +
		"$error — the other raising function — is present",
	"parent operator": "the `%` navigation to a parent node does not parse: " +
		"\"the symbol '%' cannot be used as a prefix operator\"",
}

func TestTheJSONataDialectIsWhatWeSayItIs(t *testing.T) {
	sc := &jsonataScope{input: probeInput, context: map[string]any{}}

	var broke, fixed []string
	for name, src := range dialectProbes {
		_, fail := sc.eval(src)
		absent := fail != nil && meansAbsent(fail)
		_, listed := unsupported[name]
		switch {
		case absent && !listed:
			broke = append(broke, name+": "+failText(fail))
		case !absent && listed:
			fixed = append(fixed, name)
		case fail != nil && !absent && !listed:
			// Present, and raised — which is what $error and $assert are for.
			// Nothing to report.
		}
	}
	sort.Strings(broke)
	sort.Strings(fixed)

	for _, b := range broke {
		t.Errorf("%s is absent from the embedded library and is not listed in "+
			"unsupported — either it regressed or the list was optimistic", b)
	}
	for _, f := range fixed {
		t.Errorf("%s is listed unsupported but works — remove it from the list "+
			"and from docs/api-support/stepfunctions.md, which cites it", f)
	}
	t.Logf("%d JSONata probes, %d of them unsupported", len(dialectProbes), len(unsupported))
}

// meansAbsent separates "this build does not have the feature" from "the
// feature ran and said no".
//
// The distinction is the whole point: $error and $assert exist in order to
// raise, so a failure from them is evidence they are PRESENT. Only a missing
// symbol or a parse error means the dialect is smaller than claimed.
func meansAbsent(f *Failure) bool {
	s := failText(f)
	return strings.Contains(s, "cannot call non-function") ||
		strings.Contains(s, "does not compile")
}

func failText(f *Failure) string {
	b, err := json.Marshal(f)
	if err != nil {
		return "(unprintable failure)"
	}
	s := string(b)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}
