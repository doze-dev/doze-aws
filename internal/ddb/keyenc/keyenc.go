// Package keyenc encodes DynamoDB key attribute values (S, N, B) into byte
// strings whose lexicographic order matches DynamoDB's key order — so bbolt
// cursors give range queries for free. The load-bearing property, fuzz-tested:
//
//	CompareValues(a, b) == bytes.Compare(Encode(a), Encode(b))
//
// Layout per value: a type byte, then the payload:
//
//	S/B: payload with 0x00 escaped as 0x00 0xFF, terminated by 0x00 0x01
//	     (terminator sorts below any escaped or literal byte, so prefixes
//	     sort first).
//	N:   sign class byte (2=positive, 1=zero, 0=negative), then for nonzero:
//	     biased exponent (uint16), then BCD-ish digit bytes terminated by 0x00.
//	     For negatives, exponent and digits are bitwise complemented so
//	     bigger magnitudes sort earlier.
package keyenc

import (
	"encoding/binary"
	"fmt"

	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// Type bytes keep different key types in disjoint ranges (composite keys mix
// them only when a schema is misused, but order stays total regardless).
const (
	tagB byte = 0x01
	tagN byte = 0x02
	tagS byte = 0x03
)

// Encode renders one key value. Only S, N, and B are legal DynamoDB key
// types; anything else errors.
func Encode(v item.Value) ([]byte, error) {
	switch v.Type {
	case item.TypeS:
		return append([]byte{tagS}, escapeBytes([]byte(v.S))...), nil
	case item.TypeB:
		return append([]byte{tagB}, escapeBytes(v.B)...), nil
	case item.TypeN:
		return encodeNumber(v.N), nil
	}
	return nil, fmt.Errorf("keyenc: %s is not a valid key type (want S, N, or B)", v.Type)
}

// Composite encodes a partition key followed by an optional sort key.
func Composite(pk item.Value, sk *item.Value) ([]byte, error) {
	out, err := Encode(pk)
	if err != nil {
		return nil, err
	}
	if sk != nil {
		skb, err := Encode(*sk)
		if err != nil {
			return nil, err
		}
		out = append(out, skb...)
	}
	return out, nil
}

// escapeBytes escapes 0x00 and appends the terminator.
func escapeBytes(b []byte) []byte {
	out := make([]byte, 0, len(b)+2)
	for _, c := range b {
		if c == 0x00 {
			out = append(out, 0x00, 0xFF)
			continue
		}
		out = append(out, c)
	}
	return append(out, 0x00, 0x01)
}

// encodeNumber renders a Decimal in order-preserving form.
func encodeNumber(d item.Decimal) []byte {
	out := []byte{tagN}
	switch {
	case d.IsZero():
		return append(out, 1)
	case d.Neg():
		out = append(out, 0)
	default:
		out = append(out, 2)
	}
	// Biased exponent: Decimal exponents live in [-129, 126]; bias to unsigned.
	exp := uint16(d.Exp() + 200)
	var expb [2]byte
	binary.BigEndian.PutUint16(expb[:], exp)
	digits := []byte(d.Digits())
	payload := make([]byte, 0, 2+len(digits)+1)
	payload = append(payload, expb[:]...)
	// Digit bytes: '1'..'9' map to 0x31..0x39 already ordered; terminate with
	// 0x00 so shorter digit strings (smaller magnitude at equal exponent
	// prefix) sort first.
	payload = append(payload, digits...)
	payload = append(payload, 0x00)
	if d.Neg() {
		for i := range payload {
			payload[i] = ^payload[i]
		}
	}
	return append(out, payload...)
}

// CompareValues orders two key values the way DynamoDB does (needed by tests
// and by scan segmentation). Mixed types order by type tag, mirroring Encode.
func CompareValues(a, b item.Value) int {
	ta, tb := typeTag(a), typeTag(b)
	if ta != tb {
		if ta < tb {
			return -1
		}
		return 1
	}
	switch a.Type {
	case item.TypeS:
		if a.S < b.S {
			return -1
		}
		if a.S > b.S {
			return 1
		}
		return 0
	case item.TypeB:
		if string(a.B) < string(b.B) {
			return -1
		}
		if string(a.B) > string(b.B) {
			return 1
		}
		return 0
	case item.TypeN:
		return item.Compare(a.N, b.N)
	}
	return 0
}

func typeTag(v item.Value) byte {
	switch v.Type {
	case item.TypeB:
		return tagB
	case item.TypeN:
		return tagN
	default:
		return tagS
	}
}
