package expr

import (
	"encoding/json"
	"testing"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// testItem builds the sample product item AWS's expression docs use variants of.
func testItem(t testing.TB) item.Item {
	t.Helper()
	it, aerr := item.ItemFromJSON(json.RawMessage(`{
		"Id": {"N": "123"},
		"Title": {"S": "Bicycle 123"},
		"Description": {"S": "123 description"},
		"ProductCategory": {"S": "Bicycle"},
		"Price": {"N": "500"},
		"InStock": {"BOOL": true},
		"QuantityOnHand": {"N": "0"},
		"Brand": {"S": "Mountain A"},
		"Color": {"L": [{"S": "Red"}, {"S": "Black"}]},
		"ProductReviews": {"M": {
			"FiveStar": {"L": [{"S": "Excellent!"}]},
			"OneStar": {"L": [{"S": "Terrible"}]}
		}},
		"RelatedItems": {"L": [{"N": "341"}, {"N": "472"}, {"N": "649"}]},
		"Safety": {"SS": ["Helmet", "Lights"]},
		"Sizes": {"NS": ["26", "28", "29"]}
	}`))
	if aerr != nil {
		t.Fatal(aerr)
	}
	return it
}

func values(t testing.TB, wire string) map[string]item.Value {
	t.Helper()
	if wire == "" {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wire), &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]item.Value{}
	for k, raw := range m {
		v, aerr := item.FromJSON(raw)
		if aerr != nil {
			t.Fatal(aerr)
		}
		out[k] = v
	}
	return out
}

func TestConditions(t *testing.T) {
	it := testItem(t)
	cases := []struct {
		name   string
		src    string
		names  map[string]string
		values string
		want   bool
	}{
		{"equality", "ProductCategory = :cat", nil, `{":cat": {"S": "Bicycle"}}`, true},
		{"inequality", "Price <> :p", nil, `{":p": {"N": "600"}}`, true},
		{"numeric compare", "Price > :p", nil, `{":p": {"N": "499.5"}}`, true},
		{"numeric compare false", "Price >= :p", nil, `{":p": {"N": "500.01"}}`, false},
		{"between", "Price BETWEEN :lo AND :hi", nil, `{":lo": {"N": "400"}, ":hi": {"N": "600"}}`, true},
		{"in list", "ProductCategory IN (:c1, :c2)", nil, `{":c1": {"S": "Book"}, ":c2": {"S": "Bicycle"}}`, true},
		{"and or", "Price > :lo AND (ProductCategory = :cat OR InStock = :t)", nil,
			`{":lo": {"N": "1"}, ":cat": {"S": "Nope"}, ":t": {"BOOL": true}}`, true},
		{"not", "NOT Price = :p", nil, `{":p": {"N": "1"}}`, true},
		{"attribute_exists", "attribute_exists(Brand)", nil, "", true},
		{"attribute_not_exists", "attribute_not_exists(Discontinued)", nil, "", true},
		{"attribute_type", "attribute_type(Safety, :t)", nil, `{":t": {"S": "SS"}}`, true},
		{"begins_with", "begins_with(Title, :prefix)", nil, `{":prefix": {"S": "Bic"}}`, true},
		{"contains string", "contains(Description, :word)", nil, `{":word": {"S": "descr"}}`, true},
		{"contains set", "contains(Safety, :s)", nil, `{":s": {"S": "Helmet"}}`, true},
		{"contains list", "contains(Color, :c)", nil, `{":c": {"S": "Red"}}`, true},
		{"contains NS", "contains(Sizes, :n)", nil, `{":n": {"N": "28"}}`, true},
		{"size", "size(Description) > :len", nil, `{":len": {"N": "10"}}`, true},
		{"size of list", "size(RelatedItems) = :n", nil, `{":n": {"N": "3"}}`, true},
		{"nested path", "ProductReviews.FiveStar[0] = :review", nil, `{":review": {"S": "Excellent!"}}`, true},
		{"name substitution", "#cat = :v", map[string]string{"#cat": "ProductCategory"}, `{":v": {"S": "Bicycle"}}`, true},
		{"missing attr comparison", "Discontinued = :v", nil, `{":v": {"BOOL": true}}`, false},
		{"type mismatch ordered", "Title > :n", nil, `{":n": {"N": "5"}}`, false},
		// An attribute really called "size" is reached through a #ref: bare, it
		// is a reserved word (TestReservedWords). Absent -> false, not an error.
		{"attr named size", "#s = :v", map[string]string{"#s": "size"}, `{":v": {"S": "L"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := NewEnv(tc.names, values(t, tc.values))
			cond, aerr := ParseCondition(tc.src, env)
			if aerr != nil {
				t.Fatalf("parse: %v", aerr)
			}
			got, aerr := cond.Eval(it)
			if aerr != nil {
				t.Fatalf("eval: %v", aerr)
			}
			if got != tc.want {
				t.Errorf("%q = %v, want %v", tc.src, got, tc.want)
			}
			if aerr := env.CheckAllUsed(); aerr != nil {
				t.Errorf("unused refs: %v", aerr)
			}
		})
	}
}

func TestConditionErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		src    string
		values string
	}{
		"undefined value ref": {"Price = :missing", ""},
		"trailing garbage":    {"Price = :p AND", `{":p": {"N": "1"}}`},
		"lone operand":        {"Price", ""},
		"bad char":            {"Price = :p; DROP", `{":p": {"N": "1"}}`},
		"between without and": {"Price BETWEEN :p :p", `{":p": {"N": "1"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := NewEnv(nil, values(t, tc.values))
			if _, aerr := ParseCondition(tc.src, env); aerr == nil {
				t.Errorf("accepted %q", tc.src)
			}
		})
	}

	// Unused values must be detected.
	env := NewEnv(nil, values(t, `{":used": {"N": "1"}, ":unused": {"N": "2"}}`))
	cond, aerr := ParseCondition("Price = :used", env)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if _, aerr := cond.Eval(testItem(t)); aerr != nil {
		t.Fatal(aerr)
	}
	if aerr := env.CheckAllUsed(); aerr == nil {
		t.Error("unused :unused not detected")
	}
}

func TestUpdateApply(t *testing.T) {
	it := testItem(t)
	env := NewEnv(
		map[string]string{"#qty": "QuantityOnHand"},
		values(t, `{
			":price": {"N": "450"},
			":inc": {"N": "5"},
			":newColors": {"L": [{"S": "Blue"}]},
			":defaultRating": {"N": "3"},
			":safetyAdd": {"SS": ["Reflectors"]},
			":sizeDel": {"NS": ["26"]}
		}`),
	)
	src := `SET Price = :price, #qty = #qty + :inc, Color = list_append(Color, :newColors), Rating = if_not_exists(Rating, :defaultRating) REMOVE Description, RelatedItems[1] ADD Safety :safetyAdd DELETE Sizes :sizeDel`
	upd, aerr := ParseUpdate(src, env)
	if aerr != nil {
		t.Fatalf("parse: %v", aerr)
	}
	out, aerr := upd.Apply(it)
	if aerr != nil {
		t.Fatalf("apply: %v", aerr)
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		t.Errorf("unused refs: %v", aerr)
	}

	if out["Price"].N.String() != "450" {
		t.Errorf("Price = %s", out["Price"].N.String())
	}
	if out["QuantityOnHand"].N.String() != "5" {
		t.Errorf("QuantityOnHand = %s", out["QuantityOnHand"].N.String())
	}
	if n := len(out["Color"].L); n != 3 || out["Color"].L[2].S != "Blue" {
		t.Errorf("Color = %v", out["Color"].DebugString())
	}
	if out["Rating"].N.String() != "3" {
		t.Errorf("Rating = %s", out["Rating"].DebugString())
	}
	if _, ok := out["Description"]; ok {
		t.Error("Description not removed")
	}
	if n := len(out["RelatedItems"].L); n != 2 || out["RelatedItems"].L[1].N.String() != "649" {
		t.Errorf("RelatedItems = %s", out["RelatedItems"].DebugString())
	}
	if len(out["Safety"].SS) != 3 {
		t.Errorf("Safety = %s", out["Safety"].DebugString())
	}
	if len(out["Sizes"].NS) != 2 {
		t.Errorf("Sizes = %s", out["Sizes"].DebugString())
	}

	// The original item must be untouched (deep copy).
	if it["Price"].N.String() != "500" || len(it["Color"].L) != 2 {
		t.Error("Apply mutated the original item")
	}
}

func TestUpdateErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		src    string
		values string
	}{
		"duplicate clause": {"SET a = :v SET b = :v", `{":v": {"N": "1"}}`},
		"overlapping path": {"SET a = :v REMOVE a", `{":v": {"N": "1"}}`},
		"no actions":       {"", ""},
		"arith on string":  {"SET a = Title + :v", `{":v": {"N": "1"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := NewEnv(nil, values(t, tc.values))
			upd, aerr := ParseUpdate(tc.src, env)
			if aerr != nil {
				return // parse-time rejection is fine
			}
			if _, aerr := upd.Apply(testItem(t)); aerr == nil {
				t.Errorf("accepted %q", tc.src)
			}
		})
	}
}

func TestKeyCondition(t *testing.T) {
	env := NewEnv(map[string]string{"#s": "SongTitle"}, values(t, `{":a": {"S": "No One"}, ":t": {"S": "Call"}}`))
	kc, aerr := ParseKeyCondition("Artist = :a AND begins_with(#s, :t)", env)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if kc.PKName != "Artist" || kc.PKValue.S != "No One" {
		t.Errorf("pk = %s %s", kc.PKName, kc.PKValue.DebugString())
	}
	if kc.SKName != "SongTitle" || kc.SKOp != "begins_with" || kc.SKValue.S != "Call" {
		t.Errorf("sk = %+v", kc)
	}

	// Reversed clause order still finds the equality.
	env2 := NewEnv(map[string]string{"#y": "Year"}, values(t, `{":a": {"S": "X"}, ":lo": {"N": "1"}, ":hi": {"N": "9"}}`))
	kc2, aerr := ParseKeyCondition("#y BETWEEN :lo AND :hi AND Artist = :a", env2)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if kc2.PKName != "Artist" || kc2.SKOp != "BETWEEN" || kc2.SKValue2.N.String() != "9" {
		t.Errorf("kc2 = %+v", kc2)
	}

	// Invalid shapes.
	for _, bad := range []string{
		"Artist > :a",                // pk must use =
		"Artist = :a OR Song = :a",   // OR not allowed
		"contains(Artist, :a)",       // unsupported function
		"Artist = :a AND Song <> :a", // <> not allowed on sk
	} {
		env := NewEnv(nil, values(t, `{":a": {"S": "X"}}`))
		if _, aerr := ParseKeyCondition(bad, env); aerr == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestProjection(t *testing.T) {
	it := testItem(t)
	env := NewEnv(map[string]string{"#pc": "ProductCategory"}, nil)
	pr, aerr := ParseProjection("Title, #pc, ProductReviews.FiveStar, Absent", env)
	if aerr != nil {
		t.Fatal(aerr)
	}
	out := pr.Apply(it)
	if out["Title"].S != "Bicycle 123" || out["ProductCategory"].S != "Bicycle" {
		t.Errorf("projection top-level: %v", out)
	}
	if len(out["ProductReviews"].M["FiveStar"].L) != 1 {
		t.Errorf("nested projection: %v", out["ProductReviews"].DebugString())
	}
	if _, ok := out["Absent"]; ok {
		t.Error("missing path materialized")
	}
	if _, ok := out["Price"]; ok {
		t.Error("unrequested attribute leaked")
	}
}

// FuzzParsers asserts none of the parsers panic on arbitrary input.
func FuzzParsers(f *testing.F) {
	f.Add("Price > :p AND begins_with(Title, :t)")
	f.Add("SET a = b + :v REMOVE c[0] ADD d :n DELETE e :s")
	f.Add("pk = :v AND sk BETWEEN :a AND :b")
	f.Add("a.b[0].c, d, #n")
	f.Add("size(x) <= :y OR NOT contains(z, :w)")
	f.Add("((((")
	f.Add("#")
	f.Fuzz(func(t *testing.T, src string) {
		vals := values(t, `{":p": {"N": "1"}, ":t": {"S": "x"}, ":v": {"N": "1"}, ":n": {"N": "1"}, ":s": {"SS": ["x"]}, ":a": {"N": "0"}, ":b": {"N": "9"}, ":y": {"N": "2"}, ":w": {"S": "q"}}`)
		names := map[string]string{"#n": "n"}
		it := item.Item{"Price": {Type: item.TypeN}}
		if c, aerr := ParseCondition(src, NewEnv(names, vals)); aerr == nil {
			c.Eval(it)
		}
		if u, aerr := ParseUpdate(src, NewEnv(names, vals)); aerr == nil {
			u.Apply(it)
		}
		if k, aerr := ParseKeyCondition(src, NewEnv(names, vals)); aerr == nil {
			_ = k
		}
		if p, aerr := ParseProjection(src, NewEnv(names, vals)); aerr == nil {
			p.Apply(it)
		}
	})
}

// A bare reserved word is refused in every expression, in any case, at any
// depth of a path, under the name of the expression it was found in — and is
// allowed through a #ref, and as the function it also names.
func TestReservedWords(t *testing.T) {
	one := values(t, `{":v": {"S": "x"}}`)
	refused := []struct {
		what, word string
		parse      func() *awshttp.APIError
	}{
		{"UpdateExpression", "name", func() *awshttp.APIError {
			_, aerr := ParseUpdate("SET name = :v", NewEnv(nil, one))
			return aerr
		}},
		{"UpdateExpression", "Status", func() *awshttp.APIError {
			_, aerr := ParseUpdate("REMOVE profile.Status", NewEnv(nil, nil))
			return aerr
		}},
		{"ConditionExpression", "TYPE", func() *awshttp.APIError {
			_, aerr := ParseCondition("attribute_exists(TYPE)", NewEnv(nil, nil))
			return aerr
		}},
		{"ConditionExpression", "size", func() *awshttp.APIError {
			_, aerr := ParseCondition("size = :v", NewEnv(nil, one))
			return aerr
		}},
		{"FilterExpression", "data", func() *awshttp.APIError {
			_, aerr := ParseFilter("data = :v", NewEnv(nil, one))
			return aerr
		}},
		{"KeyConditionExpression", "Year", func() *awshttp.APIError {
			_, aerr := ParseKeyCondition("Year = :v", NewEnv(nil, one))
			return aerr
		}},
		{"ProjectionExpression", "count", func() *awshttp.APIError {
			_, aerr := ParseProjection("Title, count", NewEnv(nil, nil))
			return aerr
		}},
	}
	for _, tc := range refused {
		aerr := tc.parse()
		want := "Invalid " + tc.what + ": Attribute name is a reserved keyword; reserved keyword: " + tc.word
		if aerr == nil || aerr.Code != "ValidationException" || aerr.Message != want {
			t.Errorf("%s with %q: got %v, want %s", tc.what, tc.word, aerr, want)
		}
	}

	names := map[string]string{"#n": "name"}
	if _, aerr := ParseUpdate("SET #n = :v", NewEnv(names, one)); aerr != nil {
		t.Errorf("a reserved word through a #ref: %v", aerr)
	}
	// SIZE is reserved and is also a function; the call is not an attribute name.
	if _, aerr := ParseCondition("size(Title) > :v", NewEnv(nil, one)); aerr != nil {
		t.Errorf("size() as a function: %v", aerr)
	}
	// Containing a reserved word is not being one.
	if _, aerr := ParseUpdate("SET username = :v", NewEnv(nil, one)); aerr != nil {
		t.Errorf("username: %v", aerr)
	}
}

// The list is AWS's and it is 573 words. A word dropped while editing would
// turn a refusal back into an acceptance, silently.
func TestReservedWordCount(t *testing.T) {
	if len(reservedList) != 573 || len(reservedWords) != 573 {
		t.Fatalf("reserved words: %d listed, %d distinct, want 573", len(reservedList), len(reservedWords))
	}
}
