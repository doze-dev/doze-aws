package console_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/console"
)

// The add-item editor opens with the table's key attributes already in it. The
// types matter as much as the names: DynamoDB refuses a numeric key sent as a
// string, and that refusal names the key schema rather than the attribute, so
// getting this wrong reproduces the exact confusion the prefill exists to
// prevent.
func TestItemSkeletonTypesTheKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table console.Table
		want  string
	}{
		{
			name:  "hash only, string",
			table: console.Table{HashKey: "id", HashType: "S"},
			want:  "{\n  \"id\": \"\"\n}",
		},
		{
			name:  "numeric hash is unquoted",
			table: console.Table{HashKey: "seq", HashType: "N"},
			want:  "{\n  \"seq\": 0\n}",
		},
		{
			name:  "hash and range, mixed types",
			table: console.Table{HashKey: "pk", HashType: "S", RangeKey: "ts", RangeType: "N"},
			want:  "{\n  \"pk\": \"\",\n  \"ts\": 0\n}",
		},
		{
			// Binary is base64 text on the wire, so it starts as a string.
			name:  "binary key reads as a string",
			table: console.Table{HashKey: "blob", HashType: "B"},
			want:  "{\n  \"blob\": \"\"\n}",
		},
		{
			// An attribute name needing escapes must not break the JSON. A
			// table cannot normally have one, but the editor is fed straight
			// from DescribeTable and a hand-rolled quote would be a hole.
			name:  "a quote in the key name is escaped",
			table: console.Table{HashKey: `we"ird`, HashType: "S"},
			want:  "{\n  \"we\\\"ird\": \"\"\n}",
		},
		{
			name:  "no key schema still leaves somewhere to type",
			table: console.Table{},
			want:  "{\n  \n}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.table.ItemSkeleton(); got != tc.want {
				t.Errorf("ItemSkeleton() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// The skeleton is only useful if it reaches the textarea. It is rendered into
// the element's content rather than set from JS precisely so that a form reset
// restores it, so an assertion that it is present in the page is the one that
// protects that choice.
func TestPutItemEditorOpensWithTheKeysInIt(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/ddb/create", url.Values{
		"name": {"skeleton-demo"}, "hash_key": {"pk"}, "hash_type": {"S"},
		"range_key": {"ts"}, "range_type": {"N"},
	})

	page := req(t, h, "GET", "/_console/ddb/skeleton-demo", nil).Body.String()
	// Inside a textarea Go escapes the quotes, so this is what the skeleton
	// looks like on the wire. The numeric range key must arrive unquoted.
	want := "&#34;pk&#34;: &#34;&#34;,\n  &#34;ts&#34;: 0"
	if !strings.Contains(page, want) {
		t.Errorf("the add-item editor did not open with the key schema in it; wanted\n%s\nin the page", want)
	}
}

// Editing an item and changing part of its key is not an edit: PutItem is
// keyed, so it stores a second item and leaves the original. The console
// refuses it rather than doing it silently, because the button says Edit.
func TestEditRefusesToMoveTheKey(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/ddb/create", url.Values{
		"name": {"keymove"}, "hash_key": {"pk"}, "hash_type": {"S"},
		"range_key": {"ts"}, "range_type": {"N"},
	})
	orig := `{"pk":"abc","ts":0,"note":"first"}`
	if rec := req(t, h, "POST", "/_console/ddb/keymove/put", url.Values{"item": {orig}}); rec.Code != 200 {
		t.Fatalf("seed put: %d\n%s", rec.Code, rec.Body)
	}

	// Changing a non-key attribute is a real edit and must go through.
	ok := req(t, h, "POST", "/_console/ddb/keymove/put", url.Values{
		"item": {`{"pk":"abc","ts":0,"note":"second"}`}, "orig_item": {orig},
	})
	if ok.Code != 200 {
		t.Fatalf("editing a non-key attribute was refused: %d\n%s", ok.Code, ok.Body)
	}

	// Changing the sort key is refused, and the refusal names the attribute and
	// both values rather than leaving the user to work out what happened.
	moved := req(t, h, "POST", "/_console/ddb/keymove/put", url.Values{
		"item": {`{"pk":"abc","ts":12,"note":"first"}`}, "orig_item": {orig},
	})
	if moved.Code != 400 {
		t.Fatalf("moving the sort key was accepted: %d\n%s", moved.Code, moved.Body)
	}
	for _, want := range []string{"ts", "0", "12", "NEW item"} {
		if !strings.Contains(moved.Body.String(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, moved.Body)
		}
	}

	// And the table still holds exactly the one item.
	page := req(t, h, "GET", "/_console/ddb/keymove", nil).Body.String()
	if strings.Contains(page, ">12<") {
		t.Error("the refused edit still wrote a second item")
	}

	// Adding an item sends no orig_item, so any key is allowed.
	add := req(t, h, "POST", "/_console/ddb/keymove/put", url.Values{
		"item": {`{"pk":"abc","ts":12,"note":"deliberate"}`},
	})
	if add.Code != 200 {
		t.Fatalf("Add item was blocked by the edit guard: %d\n%s", add.Code, add.Body)
	}
}
