package modelcheck

// A Query request is validated whole.
//
// FromQuery used to keep one element of every list, on the reasoning that a
// constraint applies to every element so checking one checks the rule. It does
// not: the rule is checked against the element that was kept, and a request is
// invalid when ANY element breaks it. Worse, every `member.N` was written into
// the same slot and the form is a Go map, so which element survived was the
// order the map happened to iterate in. A request with a valid first tag and an
// invalid second was refused on some runs and accepted on others.
//
// Each test here puts the violation somewhere other than element 1 and runs
// twenty times, because the failure it guards against passed most single runs.

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const queryRuns = 20

// refusedEveryTime builds the form afresh on each run (a new map, a new
// iteration order) and requires a refusal that names the place.
func refusedEveryTime(t *testing.T, what string, build func() map[string][]string, table []Constraint, wantAt string) {
	t.Helper()
	for run := 0; run < queryRuns; run++ {
		err := ValidateMapAs(FromQuery(build()), table, CodeQuery)
		if err == nil {
			t.Fatalf("%s: accepted on run %d of %d", what, run+1, queryRuns)
		}
		if err.Code != CodeQuery {
			t.Fatalf("%s: refused as %s, want %s", what, err.Code, CodeQuery)
		}
		if !strings.Contains(err.Message, "'"+wantAt+"'") {
			t.Fatalf("%s: run %d refused at the wrong place, want '%s': %s", what, run+1, wantAt, err.Message)
		}
	}
}

func acceptedEveryTime(t *testing.T, what string, build func() map[string][]string, table []Constraint) {
	t.Helper()
	for run := 0; run < queryRuns; run++ {
		if err := ValidateMapAs(FromQuery(build()), table, CodeQuery); err != nil {
			t.Fatalf("%s: refused on run %d of %d: %v", what, run+1, queryRuns, err)
		}
	}
}

var tooLong = strings.Repeat("k", 9) // against a maximum of 8

func TestAScalarListIsCheckedInEveryElement(t *testing.T) {
	table := []Constraint{{Path: "TagKeys[]", Kind: KindLength, Min: 1, Max: 8}}
	form := func(bad int) func() map[string][]string {
		return func() map[string][]string {
			f := map[string][]string{"Action": {"UntagResource"}}
			for i := 1; i <= 3; i++ {
				v := "ok" + strconv.Itoa(i)
				if i == bad {
					v = tooLong
				}
				f["TagKeys.member."+strconv.Itoa(i)] = []string{v}
			}
			return f
		}
	}
	acceptedEveryTime(t, "three valid keys", form(0), table)
	refusedEveryTime(t, "the second key too long", form(2), table, "tagKeys.2")
	refusedEveryTime(t, "the third key too long", form(3), table, "tagKeys.3")
}

func TestAListOfStructuresIsCheckedInEveryElement(t *testing.T) {
	table := []Constraint{
		{Path: "Tags[].Key", Kind: KindRequired},
		{Path: "Tags[].Key", Kind: KindLength, Min: 1, Max: 8},
		{Path: "Tags[].Value", Kind: KindLength, Min: 0, Max: 8},
	}
	form := func(badKey, badValue, noKey int) func() map[string][]string {
		return func() map[string][]string {
			f := map[string][]string{}
			for i := 1; i <= 3; i++ {
				n := strconv.Itoa(i)
				k, v := "k"+n, "v"+n
				if i == badKey {
					k = tooLong
				}
				if i == badValue {
					v = tooLong
				}
				if i != noKey {
					f["Tags.member."+n+".Key"] = []string{k}
				}
				f["Tags.member."+n+".Value"] = []string{v}
			}
			return f
		}
	}
	acceptedEveryTime(t, "three valid tags", form(0, 0, 0), table)
	refusedEveryTime(t, "the second tag's key too long", form(2, 0, 0), table, "tags.2.key")
	refusedEveryTime(t, "the third tag's key too long", form(3, 0, 0), table, "tags.3.key")
	refusedEveryTime(t, "the second tag's value too long", form(0, 2, 0), table, "tags.2.value")
	refusedEveryTime(t, "the third tag's value too long", form(0, 3, 0), table, "tags.3.value")
	// The elements used to be merged into one, so a tag with no key borrowed
	// its neighbour's and the required check never saw it missing.
	refusedEveryTime(t, "the second tag has no key", form(0, 0, 2), table, "tags.2.key")
	refusedEveryTime(t, "the third tag has no key", form(0, 0, 3), table, "tags.3.key")
}

func TestAMapSpelledAsEntriesIsCheckedInEveryEntry(t *testing.T) {
	// Scalar values (SNS topic attributes) and structure values (message
	// attributes): the two things a Query map holds.
	table := []Constraint{
		{Path: "Attributes{}", Kind: KindLength, Min: 0, Max: 8},
		{Path: "MessageAttributes{}.DataType", Kind: KindEnum, Enum: []string{"String", "Number", "Binary"}},
	}
	form := func(badAttr, badType int) func() map[string][]string {
		return func() map[string][]string {
			f := map[string][]string{}
			for i := 1; i <= 3; i++ {
				n := strconv.Itoa(i)
				v, typ := "v"+n, "String"
				if i == badAttr {
					v = tooLong
				}
				if i == badType {
					typ = "NotAType"
				}
				f["Attributes.entry."+n+".key"] = []string{"a" + n}
				f["Attributes.entry."+n+".value"] = []string{v}
				f["MessageAttributes.entry."+n+".Name"] = []string{"m" + n}
				f["MessageAttributes.entry."+n+".Value.DataType"] = []string{typ}
			}
			return f
		}
	}
	acceptedEveryTime(t, "three valid entries of each", form(0, 0), table)
	refusedEveryTime(t, "the second attribute too long", form(2, 0), table, "attributes.a2")
	refusedEveryTime(t, "the third attribute too long", form(3, 0), table, "attributes.a3")
	refusedEveryTime(t, "the second message attribute's type", form(0, 2), table, "messageAttributes.m2.dataType")
	refusedEveryTime(t, "the third message attribute's type", form(0, 3), table, "messageAttributes.m3.dataType")
}

func TestAListInsideAListIsCheckedInEveryElement(t *testing.T) {
	// A list in a structure in a list (CloudWatch's dimensions of a datum) and
	// a list directly in a list.
	table := []Constraint{
		{Path: "MetricData[].Dimensions[].Name", Kind: KindLength, Min: 1, Max: 8},
		{Path: "Grid[][]", Kind: KindLength, Min: 1, Max: 8},
	}
	form := func(badOuter, badInner int, grid bool) func() map[string][]string {
		return func() map[string][]string {
			f := map[string][]string{}
			for o := 1; o <= 3; o++ {
				for i := 1; i <= 3; i++ {
					v := fmt.Sprintf("d%d%d", o, i)
					if o == badOuter && i == badInner {
						v = tooLong
					}
					if grid {
						f[fmt.Sprintf("Grid.member.%d.member.%d", o, i)] = []string{v}
					} else {
						f[fmt.Sprintf("MetricData.member.%d.Dimensions.member.%d.Name", o, i)] = []string{v}
						f[fmt.Sprintf("MetricData.member.%d.Dimensions.member.%d.Value", o, i)] = []string{"x"}
					}
				}
			}
			return f
		}
	}
	for _, grid := range []bool{false, true} {
		at := func(o, i int) string {
			if grid {
				return fmt.Sprintf("grid.%d.%d", o, i)
			}
			return fmt.Sprintf("metricData.%d.dimensions.%d.name", o, i)
		}
		acceptedEveryTime(t, "nine valid elements", form(0, 0, grid), table)
		refusedEveryTime(t, "outer 1, inner 2", form(1, 2, grid), table, at(1, 2))
		refusedEveryTime(t, "outer 2, inner 1", form(2, 1, grid), table, at(2, 1))
		refusedEveryTime(t, "outer 2, inner 3", form(2, 3, grid), table, at(2, 3))
		refusedEveryTime(t, "outer 3, inner 3", form(3, 3, grid), table, at(3, 3))
	}
}

// A bound on the list itself: the model's "at most N members". With one
// element kept, no list was ever longer than one.
func TestAListsOwnLengthIsEnforced(t *testing.T) {
	table := []Constraint{
		{Path: "Tags", Kind: KindLength, Min: 0, Max: 50},
		{Path: "TagKeys", Kind: KindLength, Min: 0, Max: 50},
		{Path: "Attributes", Kind: KindLength, Min: 0, Max: 50},
	}
	form := func(kind string, n int) func() map[string][]string {
		return func() map[string][]string {
			f := map[string][]string{}
			for i := 1; i <= n; i++ {
				s := strconv.Itoa(i)
				switch kind {
				case "Tags":
					f["Tags.member."+s+".Key"] = []string{"k" + s}
					f["Tags.member."+s+".Value"] = []string{"v"}
				case "TagKeys":
					f["TagKeys.member."+s] = []string{"k" + s}
				case "Attributes":
					f["Attributes.entry."+s+".key"] = []string{"k" + s}
					f["Attributes.entry."+s+".value"] = []string{"v"}
				}
			}
			return f
		}
	}
	for kind, at := range map[string]string{"Tags": "tags", "TagKeys": "tagKeys", "Attributes": "attributes"} {
		acceptedEveryTime(t, kind+": fifty members", form(kind, 50), table)
		refusedEveryTime(t, kind+": fifty-one members", form(kind, 51), table, at)
	}
}

// The tree itself: every element, in index order, none merged into another.
func TestFromQueryKeepsEveryElementInIndexOrder(t *testing.T) {
	build := func() map[string][]string {
		f := map[string][]string{}
		// Twelve, so an ordering by the index's text (1, 10, 11, 12, 2 ...)
		// would show.
		for i := 1; i <= 12; i++ {
			s := strconv.Itoa(i)
			f["Names.member."+s] = []string{"n" + s}
			f["Tags.member."+s+".Key"] = []string{"k" + s}
		}
		// Each element carries a different member. Merged, they made one
		// element with both.
		f["Parts.member.1.Left"] = []string{"l"}
		f["Parts.member.2.Right"] = []string{"r"}
		// The memberless spelling some services use.
		f["Plain.1"] = []string{"p1"}
		f["Plain.2"] = []string{"p2"}
		return f
	}
	var names, tags []any
	for i := 1; i <= 12; i++ {
		s := strconv.Itoa(i)
		names = append(names, "n"+s)
		tags = append(tags, map[string]any{"Key": "k" + s})
	}
	want := map[string]any{
		"Names": names, "Tags": tags,
		"Parts": []any{map[string]any{"Left": "l"}, map[string]any{"Right": "r"}},
		"Plain": []any{"p1", "p2"},
	}
	for run := 0; run < queryRuns; run++ {
		if got := FromQuery(build()); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d:\n got: %#v\nwant: %#v", run+1, got, want)
		}
	}
}

// A gap in the indices is compacted: the elements that were sent, in the order
// of their indices, and nothing where nothing was sent.
//
// Kept as holes, a single key naming member 2000000000 would be two billion
// slots, and each hole would be a null element that a required check refuses,
// for a member the caller never sent. The list's length stays the number of
// members in the request, which is what the model's bound counts. The cost is
// that a refusal names an element by its position among those sent.
func TestFromQueryCompactsAGapAndNeverMerges(t *testing.T) {
	build := func() map[string][]string {
		return map[string][]string{
			"Tags.member.2.Key":             {"second"},
			"Tags.member.7.Key":             {"seventh"},
			"Tags.member.7.Value":           {"v7"},
			"Tags.member.2000000000.Key":    {tooLong},
			"TagKeys.member.5":              {"five"},
			"TagKeys.member.3":              {"three"},
			"Attributes.entry.9.key":        {"nine"},
			"Attributes.entry.9.value":      {"v9"},
			"Attributes.entry.4.key":        {"four"},
			"Attributes.entry.4.value":      {"v4"},
			"Outer.member.3.Inner.member.8": {"deep"},
		}
	}
	want := map[string]any{
		"Tags": []any{
			map[string]any{"Key": "second"},
			map[string]any{"Key": "seventh", "Value": "v7"},
			map[string]any{"Key": tooLong},
		},
		"TagKeys":    []any{"three", "five"},
		"Attributes": map[string]any{"four": "v4", "nine": "v9"},
		"Outer":      []any{map[string]any{"Inner": []any{"deep"}}},
	}
	table := []Constraint{{Path: "Tags[].Key", Kind: KindLength, Min: 1, Max: 8}}
	for run := 0; run < queryRuns; run++ {
		got := FromQuery(build())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d:\n got: %#v\nwant: %#v", run+1, got, want)
		}
		err := ValidateMapAs(got, table, CodeQuery)
		if err == nil || !strings.Contains(err.Message, "'tags.3.key'") {
			t.Fatalf("run %d: the last element's key is too long and was not refused there: %v", run+1, err)
		}
	}
}
