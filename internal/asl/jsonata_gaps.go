package asl

// Standard JSONata that AWS supports and the embedded library does not.
//
// blues/jsonata-go is an honest partial port, and jsonata_dialect_test.go
// measures which parts are missing rather than assuming. These are the ones
// that can be supplied as functions — registered in jsonata_fn.go through the
// same RegisterExts mechanism the AWS additions use, which can shadow a
// built-in as $random already does.
//
// The three that CANNOT be supplied this way are %, @ and #. All three are path
// operators riding on one mechanism the reference implementation has and this
// port does not: a tuple stream threaded through path evaluation, which is how
// `%` gets resolved by static analysis at compile time. No extension function
// can add an operator to the parser, and a source-level rewrite does not work
// either — `Order.Product.%.OrderID` yields one OrderID per PRODUCT, so dropping
// a path step changes the cardinality. They would need a fork of the dependency,
// and docs/SUPPORT.md says so rather than implying otherwise.

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---- $string(value, prettify) ----

// jsonataString reproduces JSONata's $string and adds the prettify argument the
// port omits.
//
// Shadowing a working built-in is the risk here, so the rules are written out
// rather than delegated to encoding/json wholesale: a bare string must come back
// as itself and NOT as a quoted JSON string, which is the one case where
// "serialise it as JSON" gives the wrong answer.
func jsonataString(v any, prettify bool) (any, error) {
	switch t := v.(type) {
	case nil:
		// JSONata has no null-to-"null" coercion here that matters for Step
		// Functions, and returning the string "null" for an absent value would
		// be worse than saying nothing.
		return nil, nil
	case string:
		return t, nil
	}
	var (
		b   []byte
		err error
	)
	if prettify {
		b, err = json.MarshalIndent(v, "", "  ")
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return nil, fmt.Errorf("$string: %v cannot be represented as a string: %v", v, err)
	}
	return string(b), nil
}

// ---- $formatInteger and $parseInteger ----

// The picture strings these accept, which is the documented set and not the
// whole of XPath's fn:format-integer:
//
//	0 # , .    a decimal pattern, with grouping separators — "#,##0"
//	a A        alphabetic: 1 -> a, 27 -> aa (A, AA when uppercase)
//	i I        roman numerals
//	w W Ww     words: "twelve", "TWELVE", "Twelve"
//
// plus the ";o" modifier on any of them for ordinals: "1;o" -> "1st",
// "w;o" -> "twelfth".
//
// A picture outside that set is an error naming what is accepted, rather than a
// silent fallback to decimal. A caller who wrote a picture this does not
// understand has a definition that will behave differently on AWS, and finding
// that out here is the entire point of running locally.
func splitPicture(picture string) (body string, ordinal bool, err error) {
	body = picture
	if i := strings.Index(body, ";"); i >= 0 {
		mod := body[i+1:]
		body = body[:i]
		switch mod {
		case "o", "o(-en)":
			ordinal = true
		case "c":
			// Cardinal is the default, so this is a no-op rather than an error.
		default:
			return "", false, fmt.Errorf("unsupported picture modifier %q: only ;o (ordinal) and ;c (cardinal) are understood", mod)
		}
	}
	if body == "" {
		return "", false, fmt.Errorf("the picture string is empty")
	}
	return body, ordinal, nil
}

func formatInteger(n int64, picture string) (any, error) {
	body, ordinal, err := splitPicture(picture)
	if err != nil {
		return nil, fmt.Errorf("$formatInteger: %v", err)
	}
	switch body {
	case "a", "A":
		if n < 1 {
			return nil, fmt.Errorf("$formatInteger: alphabetic pictures need a positive number, got %d", n)
		}
		s := alphaOf(n)
		if body == "A" {
			s = strings.ToUpper(s)
		}
		return s, nil
	case "i", "I":
		if n < 1 || n > 3999 {
			return nil, fmt.Errorf("$formatInteger: roman numerals cover 1 to 3999, got %d", n)
		}
		s := romanOf(n)
		if body == "i" {
			s = strings.ToLower(s)
		}
		return s, nil
	case "w", "W", "Ww":
		s := wordsOf(n, ordinal)
		switch body {
		case "W":
			s = strings.ToUpper(s)
		case "Ww":
			s = titleWords(s)
		}
		return s, nil
	}
	if !isDecimalPicture(body) {
		return nil, fmt.Errorf("$formatInteger: unsupported picture %q; understood are "+
			"decimal patterns of 0 # , and ., plus a A i I w W Ww, each optionally with ;o", picture)
	}
	return decimalOf(n, body, ordinal), nil
}

func parseInteger(s, picture string) (any, error) {
	body, _, err := splitPicture(picture)
	if err != nil {
		return nil, fmt.Errorf("$parseInteger: %v", err)
	}
	s = strings.TrimSpace(s)
	switch body {
	case "a", "A":
		n, ok := alphaValue(s)
		if !ok {
			return nil, fmt.Errorf("$parseInteger: %q is not an alphabetic numeral", s)
		}
		return float64(n), nil
	case "i", "I":
		n, ok := romanValue(s)
		if !ok {
			return nil, fmt.Errorf("$parseInteger: %q is not a roman numeral", s)
		}
		return float64(n), nil
	case "w", "W", "Ww":
		n, ok := wordsValue(s)
		if !ok {
			return nil, fmt.Errorf("$parseInteger: %q is not a number in words", s)
		}
		return float64(n), nil
	}
	if !isDecimalPicture(body) {
		return nil, fmt.Errorf("$parseInteger: unsupported picture %q; understood are "+
			"decimal patterns of 0 # , and ., plus a A i I w W Ww", picture)
	}
	// Strip whatever grouping the picture used, then read the digits. Ordinal
	// suffixes go too, so $parseInteger("1st", "1;o") round-trips.
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return -1
	}, strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(
		strings.ToLower(s), "st"), "nd"), "rd"), "th"))
	n, cerr := strconv.ParseInt(clean, 10, 64)
	if cerr != nil {
		return nil, fmt.Errorf("$parseInteger: %q does not hold an integer", s)
	}
	return float64(n), nil
}

// isDecimalPicture accepts a decimal-digit-pattern: optional-digit signs (#),
// mandatory-digit signs, and grouping separators.
//
// ANY decimal digit is a mandatory-digit sign, not just 0 — which is why the
// canonical ordinal picture in AWS's own examples is "1;o" rather than "0;o".
// Accepting only 0 made that spelling an "unsupported picture" error.
func isDecimalPicture(body string) bool {
	if body == "" {
		return false
	}
	digits := 0
	for _, r := range body {
		switch {
		case r >= '0' && r <= '9' || r == '#':
			digits++
		case r == ',' || r == '.':
		default:
			return false
		}
	}
	return digits > 0
}

// mandatoryDigits counts the digit signs, which set the minimum width. `#` is
// optional and does not count; "000" pads to three, "#,##0" pads to one.
func mandatoryDigits(body string) int {
	n := 0
	for _, r := range body {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// decimalOf renders n against a decimal picture: the count of 0s is the minimum
// width, and the gap between the last separator and the end is the grouping
// size. "#,##0" over 12345 gives "12,345".
func decimalOf(n int64, body string, ordinal bool) string {
	minWidth := mandatoryDigits(body)
	group := 0
	if i := strings.LastIndexAny(body, ",."); i >= 0 {
		group = len(body) - i - 1
	}
	neg := n < 0
	digits := strconv.FormatInt(abs64(n), 10)
	for len(digits) < minWidth {
		digits = "0" + digits
	}
	if group > 0 {
		digits = groupDigits(digits, group, string(body[strings.LastIndexAny(body, ",.")]))
	}
	if ordinal {
		digits += ordinalSuffix(abs64(n))
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func groupDigits(s string, size int, sep string) string {
	if size <= 0 || len(s) <= size {
		return s
	}
	var parts []string
	for len(s) > size {
		parts = append([]string{s[len(s)-size:]}, parts...)
		s = s[:len(s)-size]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	return strings.Join(parts, sep)
}

func ordinalSuffix(n int64) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ---- alphabetic ----

// alphaOf is spreadsheet-column numbering: 1 is a, 26 is z, 27 is aa.
func alphaOf(n int64) string {
	var b []byte
	for n > 0 {
		n--
		b = append([]byte{byte('a' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

func alphaValue(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, r := range strings.ToLower(s) {
		if r < 'a' || r > 'z' {
			return 0, false
		}
		n = n*26 + int64(r-'a') + 1
	}
	return n, true
}

// ---- roman ----

var romanValues = []struct {
	v int64
	s string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
	{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
	{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

func romanOf(n int64) string {
	var b strings.Builder
	for _, r := range romanValues {
		for n >= r.v {
			b.WriteString(r.s)
			n -= r.v
		}
	}
	return b.String()
}

func romanValue(s string) (int64, bool) {
	up := strings.ToUpper(strings.TrimSpace(s))
	rest := up
	var n int64
	for _, r := range romanValues {
		for strings.HasPrefix(rest, r.s) {
			n += r.v
			rest = rest[len(r.s):]
		}
	}
	if rest != "" || n == 0 {
		return 0, false
	}
	// Round-tripping is the real check. A greedy left-to-right scan accepts
	// "IIII" as 4 and reads "VX" as 5 with a trailing X; rendering the answer
	// back and demanding it match the input rejects both.
	if romanOf(n) != up {
		return 0, false
	}
	return n, true
}

// ---- words ----

var ones = []string{"zero", "one", "two", "three", "four", "five", "six",
	"seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen",
	"fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}

var tens = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty",
	"seventy", "eighty", "ninety"}

var scales = []struct {
	v int64
	s string
}{
	{1e15, "quadrillion"}, {1e12, "trillion"}, {1e9, "billion"},
	{1e6, "million"}, {1000, "thousand"}, {100, "hundred"},
}

// ordinalWords are the irregular forms. Everything else takes "th", and the
// last word of a compound is the one that changes: "twenty-first".
var ordinalWords = map[string]string{
	"one": "first", "two": "second", "three": "third", "five": "fifth",
	"eight": "eighth", "nine": "ninth", "twelve": "twelfth",
	"twenty": "twentieth", "thirty": "thirtieth", "forty": "fortieth",
	"fifty": "fiftieth", "sixty": "sixtieth", "seventy": "seventieth",
	"eighty": "eightieth", "ninety": "ninetieth",
}

func wordsOf(n int64, ordinal bool) string {
	s := cardinalWords(n)
	if !ordinal {
		return s
	}
	// Only the final word inflects, and it may sit after a hyphen.
	sep := strings.LastIndexAny(s, " -")
	head, last := "", s
	if sep >= 0 {
		head, last = s[:sep+1], s[sep+1:]
	}
	if o, ok := ordinalWords[last]; ok {
		return head + o
	}
	if strings.HasSuffix(last, "y") {
		return head + strings.TrimSuffix(last, "y") + "ieth"
	}
	return head + last + "th"
}

func cardinalWords(n int64) string {
	if n < 0 {
		return "minus " + cardinalWords(-n)
	}
	if n < 20 {
		return ones[n]
	}
	if n < 100 {
		if n%10 == 0 {
			return tens[n/10]
		}
		return tens[n/10] + "-" + ones[n%10]
	}
	for _, sc := range scales {
		if n >= sc.v {
			head := cardinalWords(n/sc.v) + " " + sc.s
			rest := n % sc.v
			if rest == 0 {
				return head
			}
			// "one hundred and five" is the en-GB reading AWS's picture "w"
			// produces; the conjunction only appears below a hundred.
			if rest < 100 {
				return head + " and " + cardinalWords(rest)
			}
			return head + " " + cardinalWords(rest)
		}
	}
	return strconv.FormatInt(n, 10)
}

func titleWords(s string) string {
	out := []rune(s)
	up := true
	for i, r := range out {
		if up && r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
		up = r == ' ' || r == '-'
	}
	return string(out)
}

// wordsValue reads the output of wordsOf back, cardinal or ordinal.
func wordsValue(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, " and ", " ")
	neg := false
	if rest, ok := strings.CutPrefix(s, "minus "); ok {
		neg, s = true, rest
	}
	var total, group int64
	seen := false
	for _, w := range strings.Fields(s) {
		w = deOrdinal(w)
		switch {
		case indexOf(ones, w) >= 0:
			group += int64(indexOf(ones, w))
			seen = true
		case indexOf(tens, w) >= 1:
			group += int64(indexOf(tens, w)) * 10
			seen = true
		case w == "hundred":
			if group == 0 {
				group = 1
			}
			group *= 100
			seen = true
		default:
			sc := scaleValue(w)
			if sc == 0 {
				return 0, false
			}
			if group == 0 {
				group = 1
			}
			total += group * sc
			group = 0
			seen = true
		}
	}
	if !seen {
		return 0, false
	}
	total += group
	if neg {
		total = -total
	}
	return total, true
}

// deOrdinal maps an ordinal word back to its cardinal, so "twenty first" and
// "twelfth" read as 21 and 12.
func deOrdinal(w string) string {
	for card, ord := range ordinalWords {
		if w == ord {
			return card
		}
	}
	if rest, ok := strings.CutSuffix(w, "ieth"); ok {
		return rest + "y"
	}
	if rest, ok := strings.CutSuffix(w, "th"); ok {
		if indexOf(ones, rest) >= 0 || indexOf(tens, rest) >= 1 {
			return rest
		}
	}
	return w
}

func scaleValue(w string) int64 {
	for _, sc := range scales {
		if sc.s == w {
			return sc.v
		}
	}
	return 0
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s && v != "" {
			return i
		}
	}
	return -1
}

// guard against a float64 that cannot be an integer reaching formatInteger.
func integralOf(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e18 {
		return 0, false
	}
	return int64(math.Trunc(f)), true
}
