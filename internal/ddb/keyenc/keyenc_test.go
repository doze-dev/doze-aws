package keyenc

import (
	"bytes"
	"testing"

	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

func sVal(s string) item.Value { return item.Value{Type: item.TypeS, S: s} }
func bVal(b []byte) item.Value { return item.Value{Type: item.TypeB, B: b} }

func nVal(t testing.TB, s string) item.Value {
	t.Helper()
	d, aerr := item.ParseDecimal(s)
	if aerr != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, aerr)
	}
	return item.Value{Type: item.TypeN, N: d}
}

func enc(t testing.TB, v item.Value) []byte {
	t.Helper()
	b, err := Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestOrderPreservation is the package's contract: encoded order == value order.
func TestOrderPreservation(t *testing.T) {
	// Strictly ascending values within each type.
	ordered := []item.Value{
		// Numbers: negatives large→small magnitude, zero, positives.
		nVal(t, "-1e20"), nVal(t, "-1000"), nVal(t, "-3.5"), nVal(t, "-3.14"),
		nVal(t, "-0.0001"), nVal(t, "0"), nVal(t, "0.00001"), nVal(t, "0.5"),
		nVal(t, "1"), nVal(t, "1.0000001"), nVal(t, "2"), nVal(t, "10"),
		nVal(t, "999999"), nVal(t, "1e20"),
		// Strings, incl. prefix relationships and embedded NULs.
		sVal(""), sVal("a"), sVal("a\x00b"), sVal("aa"), sVal("ab"), sVal("b"), sVal("ba"),
	}
	for i := 0; i < len(ordered)-1; i++ {
		a, b := ordered[i], ordered[i+1]
		if a.Type != b.Type {
			continue // types are compared within their own runs
		}
		ea, eb := enc(t, a), enc(t, b)
		if bytes.Compare(ea, eb) >= 0 {
			t.Errorf("Encode(%s) !< Encode(%s)", a.DebugString(), b.DebugString())
		}
	}

	// Binary ordering.
	bins := [][]byte{{}, {0x00}, {0x00, 0x00}, {0x00, 0x01}, {0x01}, {0xFF}}
	for i := 0; i < len(bins)-1; i++ {
		if bytes.Compare(enc(t, bVal(bins[i])), enc(t, bVal(bins[i+1]))) >= 0 {
			t.Errorf("binary order broken at %v !< %v", bins[i], bins[i+1])
		}
	}
}

func TestCompositePrefixScan(t *testing.T) {
	// All composite keys with the same partition key share an encoded prefix —
	// what makes Query a cursor seek.
	pk := sVal("user#42")
	pkEnc := enc(t, pk)
	for _, sk := range []item.Value{sVal("order#1"), nVal(t, "17"), bVal([]byte{9})} {
		skv := sk
		comp, err := Composite(pk, &skv)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(comp, pkEnc) {
			t.Errorf("composite key does not start with the partition key encoding")
		}
	}
	// Different partition keys never collide as prefixes (terminator guarantees it).
	other := enc(t, sVal("user#421"))
	if bytes.HasPrefix(other, pkEnc) {
		t.Error("user#421 encoding has user#42 encoding as a prefix")
	}
}

func TestRejectsNonKeyTypes(t *testing.T) {
	if _, err := Encode(item.Value{Type: item.TypeBool, Bool: true}); err == nil {
		t.Error("BOOL accepted as a key")
	}
}

// FuzzOrderProperty: CompareValues(a,b) must equal bytes.Compare(enc(a),enc(b))
// for every same-type pair the fuzzer can construct.
func FuzzOrderProperty(f *testing.F) {
	f.Add("12.5", "-3", "alpha", "beta")
	f.Add("0", "0.0", "", "\x00")
	f.Add("-1e100", "1e-100", "a\x00", "a")
	f.Fuzz(func(t *testing.T, n1, n2, s1, s2 string) {
		if d1, aerr := item.ParseDecimal(n1); aerr == nil {
			if d2, aerr := item.ParseDecimal(n2); aerr == nil {
				a := item.Value{Type: item.TypeN, N: d1}
				b := item.Value{Type: item.TypeN, N: d2}
				checkPair(t, a, b)
			}
		}
		checkPair(t, sVal(s1), sVal(s2))
		checkPair(t, bVal([]byte(s1)), bVal([]byte(s2)))
	})
}

func checkPair(t *testing.T, a, b item.Value) {
	t.Helper()
	ea, err := Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	eb, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	want := CompareValues(a, b)
	if got := bytes.Compare(ea, eb); got != want {
		t.Fatalf("order mismatch for %s vs %s: bytes %d, values %d",
			a.DebugString(), b.DebugString(), got, want)
	}
}
