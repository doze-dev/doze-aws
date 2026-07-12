package sns

import "testing"

func TestMatchFilterNumericAndOperators(t *testing.T) {
	num := func(v string) Attr { return Attr{DataType: "Number", StringValue: v} }
	str := func(v string) Attr { return Attr{DataType: "String", StringValue: v} }
	arr := func(v string) Attr { return Attr{DataType: "String.Array", StringValue: v} }

	cases := []struct {
		name   string
		policy string
		attrs  map[string]Attr
		want   bool
	}{
		{"empty policy matches", ``, map[string]Attr{}, true},
		{"exact string", `{"eventType":["created"]}`, map[string]Attr{"eventType": str("created")}, true},
		{"exact string miss", `{"eventType":["created"]}`, map[string]Attr{"eventType": str("deleted")}, false},

		// The regression: numeric conditions must actually match (they silently
		// dropped every message before delegating to the shared matcher).
		{"bare number", `{"price":[100]}`, map[string]Attr{"price": num("100")}, true},
		{"numeric gt match", `{"price":[{"numeric":[">",50]}]}`, map[string]Attr{"price": num("100")}, true},
		{"numeric gt miss", `{"price":[{"numeric":[">",50]}]}`, map[string]Attr{"price": num("10")}, false},
		{"numeric range", `{"price":[{"numeric":[">=",0,"<",100]}]}`, map[string]Attr{"price": num("42")}, true},
		{"numeric range out", `{"price":[{"numeric":[">=",0,"<",100]}]}`, map[string]Attr{"price": num("100")}, false},

		{"prefix", `{"k":[{"prefix":"ord"}]}`, map[string]Attr{"k": str("order-1")}, true},
		{"anything-but", `{"k":[{"anything-but":["x","y"]}]}`, map[string]Attr{"k": str("z")}, true},
		{"anything-but excluded", `{"k":[{"anything-but":["x","y"]}]}`, map[string]Attr{"k": str("x")}, false},
		{"exists true present", `{"k":[{"exists":true}]}`, map[string]Attr{"k": str("v")}, true},
		{"exists false absent", `{"k":[{"exists":false}]}`, map[string]Attr{}, true},
		{"exists true absent", `{"k":[{"exists":true}]}`, map[string]Attr{}, false},

		// String.Array attribute: any element may satisfy the condition.
		{"array member", `{"tags":["red"]}`, map[string]Attr{"tags": arr(`["blue","red"]`)}, true},
		{"array miss", `{"tags":["red"]}`, map[string]Attr{"tags": arr(`["blue","green"]`)}, false},

		// AND across attributes.
		{"multi-attr AND", `{"a":["1"],"b":["2"]}`, map[string]Attr{"a": str("1"), "b": str("2")}, true},
		{"multi-attr AND fail", `{"a":["1"],"b":["2"]}`, map[string]Attr{"a": str("1"), "b": str("3")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchFilter(tc.policy, tc.attrs); got != tc.want {
				t.Fatalf("matchFilter(%s) = %v, want %v", tc.policy, got, tc.want)
			}
		})
	}
}
