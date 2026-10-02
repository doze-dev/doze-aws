package dynamodb

// Two things a read may ask of a table and not of a global secondary index.
// Found by the boto3 conformance suite (conformance/tests/test_dynamodb.py):
// with one copy of the data every read here is consistent and complete, so
// both were simply answered.

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/ddb/store"
)

func TestIndexReadRules(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.store.CreateTable(store.Table{
		Name: "t", Hash: store.KeyPart{Name: "pk", Type: "S"}, Range: &store.KeyPart{Name: "sk", Type: "S"},
		Indexes: []store.Index{
			{Name: "gsi-keys", Hash: store.KeyPart{Name: "owner", Type: "S"}, Projection: "KEYS_ONLY"},
			{Name: "gsi-all", Hash: store.KeyPart{Name: "owner", Type: "S"}, Projection: "ALL"},
			{Name: "lsi", Hash: store.KeyPart{Name: "pk", Type: "S"}, Range: &store.KeyPart{Name: "at", Type: "S"},
				Projection: "KEYS_ONLY", Local: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		index      string
		consistent bool
		sel, want  string
	}{
		{"", true, "ALL_ATTRIBUTES", ""}, // the table itself
		{"gsi-keys", false, "", ""},
		{"gsi-keys", true, "", "Consistent reads are not supported on global secondary indexes"},
		{"gsi-keys", false, "ALL_ATTRIBUTES", "its projection type is not ALL"},
		{"gsi-keys", false, "ALL_PROJECTED_ATTRIBUTES", ""},
		{"gsi-all", false, "ALL_ATTRIBUTES", ""},
		{"lsi", true, "ALL_ATTRIBUTES", ""}, // a local index shares the table's partition
	} {
		aerr := s.indexReadRules("t", tc.index, tc.consistent, tc.sel)
		switch {
		case tc.want == "" && aerr != nil:
			t.Errorf("%q consistent=%v select=%q: refused: %v", tc.index, tc.consistent, tc.sel, aerr)
		case tc.want != "" && (aerr == nil || aerr.Code != "ValidationException" || !strings.Contains(aerr.Message, tc.want)):
			t.Errorf("%q consistent=%v select=%q: got %v, want %q", tc.index, tc.consistent, tc.sel, aerr, tc.want)
		}
	}
}

func TestBatchKeyIgnoresSpelling(t *testing.T) {
	tbl := &store.Table{Hash: store.KeyPart{Name: "pk"}, Range: &store.KeyPart{Name: "sk"}}
	a, ok1 := batchKey(tbl, []byte(`{"pk":{"S":"a"},"sk":{"N":"1"},"other":{"S":"x"}}`))
	b, ok2 := batchKey(tbl, []byte(`{ "sk": {"N": "1"}, "pk": { "S": "a" } }`))
	c, _ := batchKey(tbl, []byte(`{"pk":{"S":"a"},"sk":{"N":"2"}}`))
	if !ok1 || !ok2 || a != b || a == c {
		t.Fatalf("keys: %q %q %q (ok %v %v)", a, b, c, ok1, ok2)
	}
	if _, ok := batchKey(tbl, []byte(`{"pk":{"S":"a"}}`)); ok {
		t.Fatal("a key missing its sort attribute was treated as complete")
	}
}
