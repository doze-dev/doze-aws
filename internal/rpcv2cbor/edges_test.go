package rpcv2cbor

import (
	"bytes"
	"compress/gzip"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every bound in this package is one a hostile or broken client meets, so each
// is held on both sides: the largest value allowed passes, one more is refused
// for the reason the bound exists.

func nest(open []byte, n int, tail ...byte) []byte {
	var b []byte
	for range n {
		b = append(b, open...)
	}
	return append(b, tail...)
}

func TestDecodeNestingIsBoundedAtExactlyMaxDepth(t *testing.T) {
	for name, build := range map[string]func(n int) []byte{
		"definite arrays":   func(n int) []byte { return nest([]byte{0x81}, n, 0x00) },
		"indefinite arrays": func(n int) []byte { return append(nest([]byte{0x9f}, n, 0x00), bytes.Repeat([]byte{0xff}, n)...) },
		"maps, in values":   func(n int) []byte { return nest([]byte{0xa1, 0x61, 'k'}, n, 0x00) },
	} {
		if _, err := Decode(build(MaxDepth)); err != nil {
			t.Errorf("%s nested %d deep (the bound) were refused: %v", name, MaxDepth, err)
		}
		_, err := Decode(build(MaxDepth + 1))
		if err == nil || !strings.Contains(err.Error(), "nesting deeper") {
			t.Errorf("%s nested %d deep: got %v, want the nesting bound", name, MaxDepth+1, err)
		}
	}
	// A map KEY is read at the next depth down as well: a key that is itself a
	// deep structure hits the bound before it is found not to be a string.
	key := nest([]byte{0x81}, MaxDepth, 0x00)
	_, err := Decode(append(append([]byte{0xa1}, key...), 0x00))
	if err == nil || !strings.Contains(err.Error(), "nesting deeper") {
		t.Errorf("a map key nested %d deep: got %v, want the nesting bound", MaxDepth, err)
	}
	// And a tag wraps what is under it one level down.
	_, err = Decode(nest([]byte{0xc1}, MaxDepth+1, 0x00))
	if err == nil || !strings.Contains(err.Error(), "nesting deeper") {
		t.Errorf("tags nested %d deep: got %v, want the nesting bound", MaxDepth+1, err)
	}
	_, err = Decode(nest([]byte{0xc1}, MaxDepth, 0x00))
	if err == nil || strings.Contains(err.Error(), "nesting deeper") {
		t.Errorf("tags nested %d deep are within the bound and fail only for what they wrap: %v", MaxDepth, err)
	}
}

func TestATimestampKeepsItsFractionOfASecond(t *testing.T) {
	// tag(1) over the float64 1.5: one second and half of another.
	got, err := Decode([]byte{0xc1, 0xfb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(1, 500_000_000).UTC(); !got.(time.Time).Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func gz(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, n)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGunzipIsBoundedAtExactlyMaxDecompressed(t *testing.T) {
	out, err := Gunzip(gz(t, MaxDecompressed))
	if err != nil || len(out) != MaxDecompressed {
		t.Errorf("a body that expands to exactly the bound: %d bytes, %v", len(out), err)
	}
	if _, err := Gunzip(gz(t, MaxDecompressed+1)); err == nil || !strings.Contains(err.Error(), "expands past") {
		t.Errorf("a body one byte over the bound: got %v", err)
	}
}

func TestGunzipRecognisesTheMagicNumberFromTwoBytes(t *testing.T) {
	// Two magic bytes and nothing else is a gzip stream that is not one.
	if _, err := Gunzip([]byte{0x1f, 0x8b}); err == nil {
		t.Error("a bare gzip magic number was passed through as a body")
	}
	for _, plain := range [][]byte{{}, {0x1f}, {0x1f, 0x00}, {0x00, 0x8b}} {
		out, err := Gunzip(plain)
		if err != nil || !bytes.Equal(out, plain) {
			t.Errorf("% x: got % x, %v; want it passed through", plain, out, err)
		}
	}
}

func TestEncodeNestingIsBoundedAtExactlyMaxDepth(t *testing.T) {
	type node struct{ Next any }
	for name, build := range map[string]func(n int) any{
		"slices": func(n int) any {
			var v any = 0
			for range n {
				v = []any{v}
			}
			return v
		},
		"maps": func(n int) any {
			var v any = 0
			for range n {
				v = map[string]any{"k": v}
			}
			return v
		},
		"structs": func(n int) any {
			var v any = 0
			for range n {
				v = node{Next: v}
			}
			return v
		},
	} {
		// Find where the encoder stops, and hold that to the decoder's own bound:
		// what Marshal will write, Decode will read, and one more is refused by
		// the side that wrote it.
		edge := -1
		for n := 1; n <= MaxDepth+4; n++ {
			if _, err := Marshal(build(n)); err != nil {
				edge = n
				break
			}
		}
		if edge != MaxDepth+1 {
			t.Errorf("%s: Marshal first refuses at %d levels, want %d", name, edge, MaxDepth+1)
		}
		out, err := Marshal(build(MaxDepth))
		if err != nil {
			t.Errorf("%s: %d levels refused: %v", name, MaxDepth, err)
			continue
		}
		if _, err := Decode(out); err != nil {
			t.Errorf("%s: what Marshal wrote at the bound, Decode refused: %v", name, err)
		}
	}
}

func TestEncodeSpellsTimestampsAndEmptiesAsTheProtocolDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		in   any
		want []byte
	}{
		// tag 1 over a plain integer when there is no fraction, including the epoch itself.
		"the epoch":          {time.Unix(0, 0), []byte{0xc1, 0x00}},
		"before the epoch":   {time.Unix(-1, 0), []byte{0xc1, 0x20}},
		"a whole second":     {time.Unix(1700000000, 0), []byte{0xc1, 0x1a, 0x65, 0x53, 0xf1, 0x00}},
		"half a second":      {time.Unix(1, 500_000_000), []byte{0xc1, 0xfb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}},
		"a nil slice":        {struct{ A []string }{}, []byte{0xa1, 0x61, 'A', 0xf6}},
		"an empty slice":     {struct{ A []string }{A: []string{}}, []byte{0xa1, 0x61, 'A', 0x80}},
		"a nil map":          {struct{ A map[string]string }{}, []byte{0xa1, 0x61, 'A', 0xf6}},
		"a positive integer": {struct{ A int }{A: 5}, []byte{0xa1, 0x61, 'A', 0x05}},
		"a negative integer": {struct{ A int }{A: -5}, []byte{0xa1, 0x61, 'A', 0x24}},
	} {
		got, err := Marshal(tc.in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("%s: wrote % x, want % x", name, got, tc.want)
		}
	}
}

func TestWriteSendsTheResultItWasGiven(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, map[string]any{"Count": 3})
	got, err := DecodeMap(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got["Count"] != uint64(3) && got["Count"] != int64(3) && got["Count"] != float64(3) {
		t.Errorf("the body decoded to %#v, want Count 3", got)
	}
}
