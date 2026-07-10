package item

// Decimal is DynamoDB's number type: arbitrary precision (up to 38
// significant digits), range ±1E-130 .. ±9.9999999999999999999999999999999999999E+125,
// compared numerically. Stored normalized: no leading/trailing zero digits,
// exponent relative to a 1-digit-then-point mantissa ("d.dddd × 10^exp").

import (
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// Decimal is a normalized arbitrary-precision decimal.
type Decimal struct {
	neg    bool
	digits string // significant digits, no leading/trailing zeros; "" means zero
	exp    int    // value = 0.digits × 10^exp (i.e. digits[0] is the 10^(exp-1) place)
}

// Zero is the zero Decimal.
var Zero = Decimal{}

// IsZero reports whether d is zero.
func (d Decimal) IsZero() bool { return d.digits == "" }

// ParseDecimal parses a DynamoDB wire number.
func ParseDecimal(s string) (Decimal, *awshttp.APIError) {
	orig := s
	bad := func() (Decimal, *awshttp.APIError) {
		return Decimal{}, errValidation("%q is not a valid number", orig)
	}
	if s == "" {
		return bad()
	}
	var d Decimal
	if s[0] == '+' || s[0] == '-' {
		d.neg = s[0] == '-'
		s = s[1:]
	}
	// Split mantissa/exponent.
	expPart := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e := s[i+1:]
		s = s[:i]
		if e == "" {
			return bad()
		}
		esign := 1
		if e[0] == '+' || e[0] == '-' {
			if e[0] == '-' {
				esign = -1
			}
			e = e[1:]
		}
		if e == "" {
			return bad()
		}
		for _, c := range e {
			if c < '0' || c > '9' {
				return bad()
			}
			expPart = expPart*10 + int(c-'0')
			if expPart > 1000 {
				return Decimal{}, errValidation("number %q overflows DynamoDB's range", orig)
			}
		}
		expPart *= esign
	}
	intPart, fracPart, hasPoint := strings.Cut(s, ".")
	if intPart == "" && fracPart == "" {
		return bad()
	}
	if hasPoint && fracPart == "" && intPart == "" {
		return bad()
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return bad()
		}
	}
	// digits = intPart+fracPart with value digits × 10^(-len(frac)) × 10^exp.
	all := intPart + fracPart
	pointExp := len(intPart) + expPart // exponent such that value = 0.all × 10^pointExp

	// Normalize: strip leading zeros (adjusting exponent) and trailing zeros.
	lead := 0
	for lead < len(all) && all[lead] == '0' {
		lead++
	}
	all = all[lead:]
	pointExp -= lead
	all = strings.TrimRight(all, "0")
	if all == "" {
		return Decimal{}, nil // zero (sign of zero is dropped, like DynamoDB)
	}
	if len(all) > 38 {
		return Decimal{}, errValidation("number %q exceeds 38 significant digits", orig)
	}
	if pointExp > 126 {
		return Decimal{}, errValidation("number %q overflows DynamoDB's range", orig)
	}
	if pointExp < -129 {
		return Decimal{}, errValidation("number %q underflows DynamoDB's range", orig)
	}
	d.digits = all
	d.exp = pointExp
	return d, nil
}

// String renders the normalized decimal in plain notation where reasonable,
// scientific otherwise (matching how values survive round-trips acceptably).
func (d Decimal) String() string {
	if d.IsZero() {
		return "0"
	}
	var b strings.Builder
	if d.neg {
		b.WriteByte('-')
	}
	switch {
	case d.exp >= 1 && d.exp <= 38:
		// Digits before the point (pad with zeros if needed).
		if d.exp >= len(d.digits) {
			b.WriteString(d.digits)
			b.WriteString(strings.Repeat("0", d.exp-len(d.digits)))
		} else {
			b.WriteString(d.digits[:d.exp])
			b.WriteByte('.')
			b.WriteString(d.digits[d.exp:])
		}
	case d.exp <= 0 && d.exp > -6:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -d.exp))
		b.WriteString(d.digits)
	default:
		// Scientific: d.ddd E(exp-1)
		b.WriteString(d.digits[:1])
		if len(d.digits) > 1 {
			b.WriteByte('.')
			b.WriteString(d.digits[1:])
		}
		b.WriteByte('E')
		e := d.exp - 1
		if e >= 0 {
			b.WriteByte('+')
		}
		b.WriteString(itoa(e))
	}
	return b.String()
}

// Compare returns -1, 0, or 1 for a<b, a==b, a>b.
func Compare(a, b Decimal) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		if b.neg {
			return 1
		}
		return -1
	case b.IsZero():
		if a.neg {
			return -1
		}
		return 1
	case a.neg != b.neg:
		if a.neg {
			return -1
		}
		return 1
	}
	// Same sign: compare magnitude, flip for negatives.
	mag := compareMagnitude(a, b)
	if a.neg {
		return -mag
	}
	return mag
}

func compareMagnitude(a, b Decimal) int {
	if a.exp != b.exp {
		if a.exp < b.exp {
			return -1
		}
		return 1
	}
	ad, bd := a.digits, b.digits
	if len(ad) < len(bd) {
		ad += strings.Repeat("0", len(bd)-len(ad))
	} else if len(bd) < len(ad) {
		bd += strings.Repeat("0", len(ad)-len(bd))
	}
	return strings.Compare(ad, bd)
}

// Add returns a+b (used by ADD update actions and SET arithmetic).
func Add(a, b Decimal) Decimal {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.neg == b.neg {
		mag := addMagnitude(a, b)
		mag.neg = a.neg
		return mag
	}
	// Different signs: subtract smaller magnitude from larger.
	switch compareMagnitude(a, b) {
	case 0:
		return Zero
	case 1:
		mag := subMagnitude(a, b)
		mag.neg = a.neg
		return mag
	default:
		mag := subMagnitude(b, a)
		mag.neg = b.neg
		return mag
	}
}

// Sub returns a-b.
func Sub(a, b Decimal) Decimal {
	b.neg = !b.neg
	if b.IsZero() {
		b.neg = false
	}
	return Add(a, b)
}

// align renders both magnitudes as digit strings sharing a common exponent.
func align(a, b Decimal) (ad, bd string, exp int) {
	exp = max(a.exp, b.exp)
	ad = strings.Repeat("0", exp-a.exp) + a.digits
	bd = strings.Repeat("0", exp-b.exp) + b.digits
	if len(ad) < len(bd) {
		ad += strings.Repeat("0", len(bd)-len(ad))
	} else if len(bd) < len(ad) {
		bd += strings.Repeat("0", len(ad)-len(bd))
	}
	return ad, bd, exp
}

func addMagnitude(a, b Decimal) Decimal {
	ad, bd, exp := align(a, b)
	n := len(ad)
	out := make([]byte, n+1)
	carry := byte(0)
	for i := n - 1; i >= 0; i-- {
		sum := ad[i] - '0' + bd[i] - '0' + carry
		out[i+1] = sum%10 + '0'
		carry = sum / 10
	}
	out[0] = carry + '0'
	digits := string(out)
	if carry > 0 {
		exp++
	} else {
		digits = digits[1:]
	}
	return normalize(digits, exp)
}

// subMagnitude computes |a|-|b| assuming |a| > |b|.
func subMagnitude(a, b Decimal) Decimal {
	ad, bd, exp := align(a, b)
	n := len(ad)
	out := make([]byte, n)
	borrow := byte(0)
	for i := n - 1; i >= 0; i-- {
		ai := ad[i] - '0'
		bi := bd[i] - '0' + borrow
		if ai < bi {
			out[i] = ai + 10 - bi + '0'
			borrow = 1
		} else {
			out[i] = ai - bi + '0'
			borrow = 0
		}
	}
	return normalize(string(out), exp)
}

// normalize strips leading/trailing zeros, adjusting the exponent.
func normalize(digits string, exp int) Decimal {
	lead := 0
	for lead < len(digits) && digits[lead] == '0' {
		lead++
	}
	digits = digits[lead:]
	exp -= lead
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return Zero
	}
	return Decimal{digits: digits, exp: exp}
}

// Digits exposes the significant digits (keyenc needs them).
func (d Decimal) Digits() string { return d.digits }

// Exp exposes the normalized exponent (keyenc needs it).
func (d Decimal) Exp() int { return d.exp }

// Neg reports the sign (keyenc needs it).
func (d Decimal) Neg() bool { return d.neg }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
