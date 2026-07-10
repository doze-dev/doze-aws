package item

import (
	"encoding/json"
	"testing"
)

func mustParse(t *testing.T, s string) Decimal {
	t.Helper()
	d, aerr := ParseDecimal(s)
	if aerr != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, aerr)
	}
	return d
}

func TestDecimalParseAndString(t *testing.T) {
	cases := map[string]string{
		"0":          "0",
		"-0":         "0",
		"0.0":        "0",
		"42":         "42",
		"-42":        "-42",
		"3.14":       "3.14",
		"0.5":        "0.5",
		"-0.001":     "-0.001",
		"1e3":        "1000",
		"1.5E2":      "150",
		"12.30":      "12.3",
		"007":        "7",
		"1E+30":      "1000000000000000000000000000000", // plain within the 38-digit window
		"2.5e-7":     "2.5E-7",                          // scientific below the plain threshold
		"1234567890": "1234567890",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := mustParse(t, in).String(); got != want {
				t.Errorf("ParseDecimal(%q).String() = %q, want %q", in, got, want)
			}
		})
	}
	for _, bad := range []string{"", "abc", "1.2.3", "1e", "--5", ".", "+", "1e99999",
		"123456789012345678901234567890123456789"} { // 39 digits
		if _, aerr := ParseDecimal(bad); aerr == nil {
			t.Errorf("ParseDecimal(%q) accepted", bad)
		}
	}
}

func TestDecimalCompare(t *testing.T) {
	ordered := []string{"-1000", "-3.14", "-0.001", "0", "0.0001", "0.5", "1", "2.5", "10", "10.5", "1e5"}
	for i := range ordered {
		for j := range ordered {
			a, b := mustParse(t, ordered[i]), mustParse(t, ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	// Equality across representations.
	for _, pair := range [][2]string{{"1", "1.0"}, {"1e2", "100"}, {"-0", "0"}, {"0.50", "0.5"}} {
		if Compare(mustParse(t, pair[0]), mustParse(t, pair[1])) != 0 {
			t.Errorf("Compare(%s, %s) != 0", pair[0], pair[1])
		}
	}
}

func TestDecimalArithmetic(t *testing.T) {
	cases := []struct{ a, b, sum, diff string }{
		{"1", "2", "3", "-1"},
		{"2.5", "0.5", "3", "2"},
		{"-1", "-2", "-3", "1"},
		{"10", "-3", "7", "13"},
		{"0.1", "0.2", "0.3", "-0.1"},
		{"999", "1", "1000", "998"},
		{"1e10", "1", "10000000001", "9999999999"},
		{"0", "5", "5", "-5"},
		{"5", "5", "10", "0"},
	}
	for _, tc := range cases {
		a, b := mustParse(t, tc.a), mustParse(t, tc.b)
		if got := Add(a, b).String(); got != tc.sum {
			t.Errorf("%s + %s = %s, want %s", tc.a, tc.b, got, tc.sum)
		}
		if got := Sub(a, b).String(); got != tc.diff {
			t.Errorf("%s - %s = %s, want %s", tc.a, tc.b, got, tc.diff)
		}
	}
}

func TestValueRoundTrip(t *testing.T) {
	wire := `{
		"name": {"S": "widget"},
		"price": {"N": "19.99"},
		"blob": {"B": "aGVsbG8="},
		"active": {"BOOL": true},
		"missing": {"NULL": true},
		"tags": {"SS": ["a", "b"]},
		"scores": {"NS": ["1", "2.5"]},
		"nested": {"M": {"depth": {"N": "2"}, "list": {"L": [{"S": "x"}, {"N": "7"}]}}}
	}`
	it, aerr := ItemFromJSON(json.RawMessage(wire))
	if aerr != nil {
		t.Fatal(aerr)
	}
	// Round-trip through the wire form and compare deeply.
	back, aerr := ItemFromJSON(ItemJSON(it))
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(back) != len(it) {
		t.Fatalf("round trip lost attributes: %d vs %d", len(back), len(it))
	}
	for k, v := range it {
		if !Equal(v, back[k]) {
			t.Errorf("attribute %q changed across round trip: %s vs %s", k, v.DebugString(), back[k].DebugString())
		}
	}
	if it["nested"].M["list"].L[1].N.String() != "7" {
		t.Error("nested access broken")
	}
}

func TestValidationRejects(t *testing.T) {
	for name, wire := range map[string]string{
		"two type keys": `{"S": "x", "N": "1"}`,
		"empty SS":      `{"SS": []}`,
		"dup SS":        `{"SS": ["a", "a"]}`,
		"dup NS":        `{"NS": ["1", "1.0"]}`,
		"bad N":         `{"N": "one"}`,
		"bad B":         `{"B": "!!!"}`,
		"39-digit N":    `{"N": "123456789012345678901234567890123456789"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, aerr := FromJSON(json.RawMessage(wire)); aerr == nil {
				t.Errorf("accepted %s", wire)
			}
		})
	}
}

func TestSetEqualityIgnoresOrder(t *testing.T) {
	a, _ := FromJSON(json.RawMessage(`{"SS": ["x", "y", "z"]}`))
	b, _ := FromJSON(json.RawMessage(`{"SS": ["z", "x", "y"]}`))
	if !Equal(a, b) {
		t.Error("SS order should not matter")
	}
	c, _ := FromJSON(json.RawMessage(`{"NS": ["1", "2"]}`))
	d, _ := FromJSON(json.RawMessage(`{"NS": ["2", "1.0"]}`))
	if !Equal(c, d) {
		t.Error("NS order/representation should not matter")
	}
}

func TestItemSizeEnforceable(t *testing.T) {
	small := Item{"k": {Type: TypeS, S: "v"}}
	if got := Size(small); got != 2 {
		t.Errorf("Size = %d, want 2", got)
	}
	if Size(small) > MaxItemSize {
		t.Error("small item over limit?")
	}
}

// FuzzDecimal asserts parse/format/compare never panic and that formatting
// round-trips numerically.
func FuzzDecimal(f *testing.F) {
	for _, seed := range []string{"0", "-1.5", "1e10", "0.00001", "9e125", "abc", "", "1.2.3", "00.100"} {
		f.Add(seed, "1")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		da, aerr := ParseDecimal(a)
		if aerr != nil {
			return
		}
		// Formatting must re-parse to an equal value.
		back, aerr := ParseDecimal(da.String())
		if aerr != nil {
			t.Fatalf("String() output %q does not re-parse: %v", da.String(), aerr)
		}
		if Compare(da, back) != 0 {
			t.Fatalf("round trip changed value: %q -> %q", a, da.String())
		}
		db, aerr := ParseDecimal(b)
		if aerr != nil {
			return
		}
		// Compare antisymmetry.
		if Compare(da, db) != -Compare(db, da) {
			t.Fatal("Compare not antisymmetric")
		}
		// Add/Sub inverse: (a+b)-b == a.
		if Compare(Sub(Add(da, db), db), da) != 0 {
			t.Fatalf("(%s + %s) - %s != %s", a, b, b, a)
		}
	})
}
