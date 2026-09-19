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
	// The other two path-binding operators. Added after the first 84 probes,
	// because the reference implementation ships all three of %, @ and # on one
	// piece of machinery (a tuple stream carried through path evaluation) and
	// the embedded library has none of it — no parent step, no focus binding,
	// no index binding. Probing % and not these two measured a third of one
	// feature and called it a gap of one.
	"focus binding": `items@$i.$i.v`,
	"index binding": `items#$idx.$idx`,
	// Function-signature variants, which a port can plausibly get partly right.
	"regex flags":        `$match(str, /O/i)`,
	"replace with fn":    `$replace(str, /o/, function($m) { "0" })`,
	"string prettify":    `$string(obj, true)`,
	"sort with fn":       `$sort(nums, function($a, $b) { $a > $b })`,
	"reduce with init":   `$reduce(nums, function($a, $b) { $a + $b }, 10)`,
	"map with index":     `$map(nums, function($v, $i) { $i })`,
	"filter with index":  `$filter(nums, function($v, $i) { $i > 0 })`,
	"block in path":      `items.($x := v; $x)`,
	"positional predic.": `items[0]`,
	"negative index":     `nums[-1]`,

	// ---- the gaps jsonata_gaps.go fills ----
	// Presence only; jsonata_gaps_test.go checks that the answers are right,
	// which is a different question and the one that matters for new code.
	// $assert's raising branch is in raisingProbes; this is the true branch,
	// which must yield nothing and so is probed through $exists.
	"$assert true": `$exists($assert(true, "fine")) = false`,

	// ---- the five AWS adds, which are this repo's own code ----
	"$partition": `$partition([1,2,3,4], 2)`,
	"$range":     `$range(0, 3, 1)`,
	"$hash":      `$hash("x", "SHA-256")`,
	"$uuid":      `$length($uuid())`,
	"$random":    `$random(1) >= 0`,
	"$parse":     `$parse("{\"a\":1}")`,
	"$states":    `$states.input.n`,
}

// unsupported is the measured gap: every probe above that does not work.
//
// Frozen in both directions. A name missing from it that stops working fails
// the test; a name in it that starts working also fails, naming the doc that
// cites it — a dependency bump quietly widening what we claim is the same
// failure as a regression quietly narrowing it.
//
// $eval is NOT here, and that is the point of the note in the doc: AWS does not
// support $eval either ("All built-in JSONata functions and operators in the
// 2.0.6 specification are supported, with one exception: $eval is not
// available—use $parse instead"), and $parse — AWS's prescribed replacement —
// is implemented. Not having it is parity, not a gap.
var unsupported = map[string]string{
	// $assert, $formatInteger, $parseInteger and the two-argument $string were
	// all here until jsonata_gaps.go supplied them. They were supplied rather
	// than documented because the extension mechanism already existed — the
	// AWS additions use it, and $random already shadows a built-in with it — so
	// the whole cost was writing the functions.
	//
	// These three are one missing feature, not three. The reference
	// implementation carries a tuple stream through path evaluation, and all of
	// %, @ and # ride on it; jsonata-go has no such machinery, so none of them
	// exist. Probing % alone measured a third of one feature and reported a gap
	// of one — see the note on focus/index binding above.
	"parent operator": "`%`, the step to the enclosing object. Needs the " +
		"tuple stream the port does not have, so no extension can add it",
	"focus binding": "`@$v`, binding the context at a path step. Same " +
		"missing machinery as `%`",
	"index binding": "`#$i`, binding the position at a path step. Same " +
		"missing machinery as `%`",
}

// raisingProbes exist in order to throw, so they have no value to check. They
// are asserted on how they fail: raising is correct, being absent is not.
var raisingProbes = map[string]string{
	"$error":       `$error("boom")`,
	"$assert":      `$assert(false, "boom")`,
	"$assert bare": `$assert(false)`,
}

// awsExcluded must NOT work, because AWS does not offer it either. $eval is the
// only entry and is the whole reason this map exists rather than a comment:
// implementing it would be a DIVERGENCE from Step Functions, so if a future
// dependency bump adds it, this build should keep refusing it and somebody
// should have to decide that on purpose.
var awsExcluded = map[string]string{
	"$eval": `$eval("[1,2]")`,
}

// TestTheJSONataDialectIsWhatWeSayItIs holds every probe to ONE rule: it must
// produce a value. Any failure at all — a missing symbol, a parse error, a
// wrong arity, an expression that yields nothing — is a gap.
//
// That rule is the third attempt, and the first two both let real gaps through
// by asking something weaker:
//
//	$exists($error) — answers false for a missing function instead of failing,
//	so the probe passed whether or not the function existed. It was hiding
//	$assert.
//
//	"failed with 'cannot call non-function' or 'does not compile'" — a
//	classifier over error TEXT, which missed the three failures that phrase
//	themselves differently: @ and # yield no result, and $string(v, true) is a
//	wrong-arity error. It was hiding all three.
//
// Both had the same shape as the inert heap floor a few commits ago: an
// assertion that cannot fail, inside a test that passes. Every probe producing
// a value has no such loophole — if it does not produce one, it is a gap, and
// there is nothing to classify.
func TestTheJSONataDialectIsWhatWeSayItIs(t *testing.T) {
	sc := &jsonataScope{input: probeInput, context: map[string]any{}}

	var broke, fixed []string
	for name, src := range dialectProbes {
		v, fail := sc.eval(src)
		works := fail == nil && v != nil
		_, listed := unsupported[name]
		switch {
		case !works && !listed:
			broke = append(broke, name+": "+failText(fail))
		case works && listed:
			fixed = append(fixed, name)
		}
	}
	// A raising function must fail, but not by being absent.
	for name, src := range raisingProbes {
		_, fail := sc.eval(src)
		switch {
		case fail == nil:
			broke = append(broke, name+" did not raise, which is all it does")
		case strings.Contains(failText(fail), "cannot call non-function"):
			broke = append(broke, name+": "+failText(fail))
		}
	}
	// Parity with AWS's own exclusion, asserted rather than assumed.
	for name, src := range awsExcluded {
		if _, fail := sc.eval(src); fail == nil {
			broke = append(broke, name+" evaluates, but Step Functions does not "+
				"offer it — matching AWS means refusing it, so this needs a "+
				"deliberate decision rather than a silent divergence")
		}
	}
	sort.Strings(broke)
	sort.Strings(fixed)

	for _, b := range broke {
		t.Errorf("%s does not work and is not listed in unsupported — either it "+
			"regressed or the list was optimistic", b)
	}
	for _, f := range fixed {
		t.Errorf("%s is listed unsupported but works — remove it from the list "+
			"and from docs/api-support/stepfunctions.md, which cites it", f)
	}
	t.Logf("%d value probes + %d raising + %d AWS-excluded, %d unsupported",
		len(dialectProbes), len(raisingProbes), len(awsExcluded), len(unsupported))
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
