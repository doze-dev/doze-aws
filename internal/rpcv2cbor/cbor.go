// Package rpcv2cbor decodes and encodes the Smithy RPC v2 CBOR protocol, the
// wire modern AWS SDKs speak to CloudWatch.
//
// # Why hand-rolled
//
// doze-aws has three runtime dependencies and this is not going to be the
// fourth. The subset a service model actually needs is small, and the decoder
// has to be defensive about hostile input anyway — which is a thing to write
// deliberately rather than inherit.
//
// # What the wire actually looks like
//
// Written from bytes captured off the pinned SDKs (testdata/), not from a
// reading of the specification, because two things the spec permits but does
// not emphasise turned out to be what the SDKs do:
//
//   - aws-sdk-go-v2 encodes maps and arrays with INDEFINITE lengths (0xbf,
//     0x9f, terminated by 0xff) throughout. A decoder that handled only
//     definite lengths — the obvious simplification — would fail on every
//     real request.
//   - Request bodies may arrive gzipped. `smithy.api#requestCompression` sits
//     on PutMetricData, aws-sdk-go-v2 honours it even for small bodies, and
//     the JavaScript SDK does not compress at all. So compression is a
//     property of the caller, not of the operation, and both forms have to be
//     accepted. See Gunzip.
//
// # The value model
//
// Decoding produces exactly what encoding/json would produce for the same
// document: map[string]any, []any, string, float64, bool, nil. Integers
// become float64 and byte strings become base64 strings, so a caller reading a
// decoded body cannot tell which wire it arrived on. Nothing in the models
// this serves needs the extra precision — the widest numeric member is a
// double, and millisecond epochs are exact in float64 for centuries — and one
// value model means one set of handlers.
//
// Timestamps are the exception worth naming: tag 1 decodes to time.Time,
// because "a number that is secretly a date" is the one distinction a handler
// cannot recover on its own.
package rpcv2cbor

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Limits. A decoder reading attacker-controlled bytes needs an answer for
// "how deep" and "how big" that does not depend on the caller remembering to
// ask.
const (
	// MaxDepth bounds nesting. The deepest path in the CloudWatch model is
	// about six containers; sixty-four leaves room without letting a
	// hand-written frame recurse until the stack gives out.
	MaxDepth = 64
	// MaxDecompressed bounds what Gunzip will produce, so a small compressed
	// body cannot expand into an arbitrarily large one.
	MaxDecompressed = 32 << 20
)

// ErrTrailing reports bytes left over after a complete value, which means the
// frame was not what it claimed to be.
var ErrTrailing = errors.New("rpcv2cbor: trailing bytes after the top-level value")

// Major types, named so the switch below reads like the specification.
const (
	majUint   = 0
	majNegInt = 1
	majBytes  = 2
	majText   = 3
	majArray  = 4
	majMap    = 5
	majTag    = 6
	majSimple = 7
)

// Well-known arguments of major type 7.
const (
	simpleFalse = 20
	simpleTrue  = 21
	simpleNull  = 22
	simpleUndef = 23
	simpleF16   = 25
	simpleF32   = 26
	simpleF64   = 27
)

// indefinite marks a head whose argument is "until the break byte".
const indefiniteArg = 31

// breakByte terminates an indefinite-length container.
const breakByte = 0xff

// Tags this decoder understands. Everything else is refused by name rather
// than skipped: a tag changes what the value MEANS, so ignoring one and
// decoding the payload underneath would hand back a plausible wrong answer.
const (
	tagEpoch = 1 // RFC 8949 epoch-based date/time
)

// Gunzip returns the body a request carried, decompressing it when the caller
// gzipped it. Content-Encoding is the caller's claim; the magic number is the
// evidence, and both have to agree before this spends any effort.
//
// Bounded: a gzip stream can expand enormously, so the reader stops at
// MaxDecompressed and says so rather than filling memory.
func Gunzip(body []byte) ([]byte, error) {
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rpcv2cbor: gzipped body: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, MaxDecompressed+1))
	if err != nil {
		return nil, fmt.Errorf("rpcv2cbor: gzipped body: %w", err)
	}
	if len(out) > MaxDecompressed {
		return nil, fmt.Errorf("rpcv2cbor: gzipped body expands past %d bytes", MaxDecompressed)
	}
	return out, nil
}

// Decode reads one CBOR document, gunzipping first when the bytes are gzipped.
func Decode(body []byte) (any, error) {
	raw, err := Gunzip(body)
	if err != nil {
		return nil, err
	}
	d := &decoder{buf: raw}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.pos != len(d.buf) {
		return nil, ErrTrailing
	}
	return v, nil
}

// DecodeMap is Decode for the usual case: a request body is a structure, so
// anything else is a malformed request rather than an interesting value.
func DecodeMap(body []byte) (map[string]any, error) {
	v, err := Decode(body)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return map[string]any{}, nil // an empty body is an empty input
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("rpcv2cbor: body is %T, want a map", v)
	}
	return m, nil
}

type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) more(n int) error {
	if d.pos+n > len(d.buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (d *decoder) byteAt() (byte, error) {
	if err := d.more(1); err != nil {
		return 0, err
	}
	b := d.buf[d.pos]
	d.pos++
	return b, nil
}

// head reads an initial byte and its argument. indef reports the
// "until break" form, which is legal only for the container types.
//
// low — the additional-information nibble — is returned alongside arg because
// for major type 7 they mean different things: low says WHICH simple value or
// float width, and arg holds the float's bits. Reading the float width off
// arg instead is a bug that only shows up on floats, since for low < 24 the
// two are equal.
func (d *decoder) head() (major, low byte, arg uint64, indef bool, err error) {
	ib, err := d.byteAt()
	if err != nil {
		return 0, 0, 0, false, err
	}
	major = ib >> 5
	low = ib & 0x1f

	switch {
	case low < 24:
		return major, low, uint64(low), false, nil
	case low == 24:
		b, err := d.byteAt()
		return major, low, uint64(b), false, err
	case low == 25:
		if err := d.more(2); err != nil {
			return 0, 0, 0, false, err
		}
		arg = uint64(d.buf[d.pos])<<8 | uint64(d.buf[d.pos+1])
		d.pos += 2
		return major, low, arg, false, nil
	case low == 26:
		if err := d.more(4); err != nil {
			return 0, 0, 0, false, err
		}
		for i := range 4 {
			arg = arg<<8 | uint64(d.buf[d.pos+i])
		}
		d.pos += 4
		return major, low, arg, false, nil
	case low == 27:
		if err := d.more(8); err != nil {
			return 0, 0, 0, false, err
		}
		for i := range 8 {
			arg = arg<<8 | uint64(d.buf[d.pos+i])
		}
		d.pos += 8
		return major, low, arg, false, nil
	case low == indefiniteArg:
		// Only the containers and the two string types may be indefinite;
		// the caller checks, because "which" is per major type.
		return major, low, 0, true, nil
	}
	// low is 28, 29 or 30: reserved, and a frame using one is malformed.
	return 0, 0, 0, false, fmt.Errorf("rpcv2cbor: reserved additional-information %d", low)
}

func (d *decoder) value(depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("rpcv2cbor: nesting deeper than %d", MaxDepth)
	}
	major, low, arg, indef, err := d.head()
	if err != nil {
		return nil, err
	}

	switch major {
	case majUint:
		return float64(arg), nil

	case majNegInt:
		// -1 - arg, per RFC 8949.
		return -1 - float64(arg), nil

	case majBytes:
		raw, err := d.byteString(major, arg, indef)
		if err != nil {
			return nil, err
		}
		// Laundered to what encoding/json would give for the same member.
		return base64.StdEncoding.EncodeToString(raw), nil

	case majText:
		raw, err := d.byteString(major, arg, indef)
		if err != nil {
			return nil, err
		}
		return string(raw), nil

	case majArray:
		return d.array(arg, indef, depth)

	case majMap:
		return d.mapValue(arg, indef, depth)

	case majTag:
		if indef {
			return nil, errors.New("rpcv2cbor: a tag cannot be indefinite")
		}
		return d.tagged(arg, depth)

	case majSimple:
		return d.simple(low, arg, indef)
	}
	return nil, fmt.Errorf("rpcv2cbor: unknown major type %d", major)
}

// byteString reads a definite run, or concatenates the chunks of an
// indefinite one. Chunks must repeat the same major type — a text string
// cannot be assembled out of byte-string pieces.
func (d *decoder) byteString(major byte, arg uint64, indef bool) ([]byte, error) {
	if !indef {
		n := int(arg)
		if n < 0 || uint64(n) != arg {
			return nil, errors.New("rpcv2cbor: string length out of range")
		}
		if err := d.more(n); err != nil {
			return nil, err
		}
		out := d.buf[d.pos : d.pos+n]
		d.pos += n
		return out, nil
	}
	var out []byte
	for {
		if err := d.more(1); err != nil {
			return nil, err
		}
		if d.buf[d.pos] == breakByte {
			d.pos++
			return out, nil
		}
		cMajor, _, cArg, cIndef, err := d.head()
		if err != nil {
			return nil, err
		}
		if cMajor != major || cIndef {
			return nil, errors.New("rpcv2cbor: mismatched chunk in an indefinite string")
		}
		chunk, err := d.byteString(cMajor, cArg, false)
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
	}
}

func (d *decoder) array(arg uint64, indef bool, depth int) ([]any, error) {
	// Always non-nil: an empty CBOR array is an empty list, not a missing one,
	// and the difference is visible to a caller checking for presence.
	out := []any{}
	if indef {
		for {
			if err := d.more(1); err != nil {
				return nil, err
			}
			if d.buf[d.pos] == breakByte {
				d.pos++
				return out, nil
			}
			el, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, el)
		}
	}
	for i := uint64(0); i < arg; i++ {
		// Bounded by the buffer, so a header claiming a huge count cannot
		// preallocate: each element has to actually be there.
		el, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, el)
	}
	return out, nil
}

func (d *decoder) mapValue(arg uint64, indef bool, depth int) (map[string]any, error) {
	out := map[string]any{}
	put := func() error {
		k, err := d.value(depth + 1)
		if err != nil {
			return err
		}
		ks, ok := k.(string)
		if !ok {
			// Every structure and map in the models this serves is keyed by
			// string. Coercing a non-string key would invent a member name.
			return fmt.Errorf("rpcv2cbor: map key is %T, want a string", k)
		}
		v, err := d.value(depth + 1)
		if err != nil {
			return err
		}
		out[ks] = v
		return nil
	}
	if indef {
		for {
			if err := d.more(1); err != nil {
				return nil, err
			}
			if d.buf[d.pos] == breakByte {
				d.pos++
				return out, nil
			}
			if err := put(); err != nil {
				return nil, err
			}
		}
	}
	for i := uint64(0); i < arg; i++ {
		if err := put(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (d *decoder) tagged(tag uint64, depth int) (any, error) {
	inner, err := d.value(depth + 1)
	if err != nil {
		return nil, err
	}
	switch tag {
	case tagEpoch:
		f, ok := inner.(float64)
		if !ok {
			return nil, fmt.Errorf("rpcv2cbor: tag 1 wraps %T, want a number", inner)
		}
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(math.Round(frac*1e9))).UTC(), nil
	}
	// Refused rather than unwrapped: a tag says the bytes underneath mean
	// something other than what they look like, so handing back the payload
	// would be a confident wrong answer. Bignums and decimal fractions land
	// here, and nothing in the models served needs them.
	return nil, fmt.Errorf("rpcv2cbor: unsupported tag %d", tag)
}

// simple decodes major type 7. It dispatches on low, the additional-info
// nibble, NOT on arg: for the three float widths arg holds the bits, and only
// low says how wide they were.
func (d *decoder) simple(low byte, arg uint64, indef bool) (any, error) {
	if indef {
		// 0xff on its own — a break with no container open.
		return nil, errors.New("rpcv2cbor: unexpected break")
	}
	switch low {
	case simpleFalse:
		return false, nil
	case simpleTrue:
		return true, nil
	case simpleNull:
		return nil, nil
	case simpleUndef:
		// The spec says undefined is not supported and is to be treated as
		// null, which is also what JSON would have carried.
		return nil, nil
	case simpleF16:
		return float16(uint16(arg)), nil
	case simpleF32:
		return float64(math.Float32frombits(uint32(arg))), nil
	case simpleF64:
		return math.Float64frombits(arg), nil
	}
	return nil, fmt.Errorf("rpcv2cbor: unsupported simple value %d", low)
}

// float16 expands a half-precision float. The spec says never to emit one and
// always to accept one, so this is decode-only.
func float16(h uint16) float64 {
	sign := uint64(h>>15) << 63
	exp := int64(h>>10) & 0x1f
	mant := uint64(h) & 0x3ff
	switch exp {
	case 0:
		if mant == 0 {
			return math.Float64frombits(sign) // ±0
		}
		// Subnormal: normalise into the double's exponent range.
		e := int64(-14)
		for mant&0x400 == 0 {
			mant <<= 1
			e--
		}
		mant &= 0x3ff
		return math.Float64frombits(sign | uint64(e+1023)<<52 | mant<<42)
	case 0x1f:
		if mant == 0 {
			return math.Float64frombits(sign | 0x7ff<<52) // ±Inf
		}
		return math.NaN()
	}
	return math.Float64frombits(sign | uint64(exp-15+1023)<<52 | mant<<42)
}
