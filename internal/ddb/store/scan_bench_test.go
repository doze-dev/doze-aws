package store

// Scan and Query over a populated table.
//
// docs/storage.md picks bbolt over pebble largely on scan cost — "reads and
// scans dominate; every console render fans out across services" — and the
// number it quotes for that (10k items in 55.9 µs) came from a benchmark that
// lives OUTSIDE this repo. The decision record's central claim was therefore
// unreproducible here, and the code path it describes had no benchmark at all.
//
// internal/ddb/expr measures the per-item filter evaluation in isolation and
// says, correctly, that the per-item number is what decides how a big table
// behaves. This measures the loop around it: the bbolt cursor walk, the item
// decode, and the filter call, which together are what a Scan actually costs.
//
// The pairs matter more than any single number:
//
//	Scan vs ScanFiltered   what the filter adds per item
//	Scan vs Query          what having a key instead of a walk is worth
//	10k vs 1k              whether the walk is linear, which is the property
//	                       the storage decision assumed

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/ddb/expr"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// benchStore fills one table with n items across 100 partitions, so a Query
// has a realistic number of rows under one hash key rather than one or all.
func benchStore(b *testing.B, n int) *Store {
	b.Helper()
	db, err := bolt.Open(filepath.Join(b.TempDir(), "ddb.bolt"), 0o600, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	s := New(db)
	if _, err := s.CreateTable(Table{
		Name:  "bench",
		Hash:  KeyPart{Name: "pk", Type: "S"},
		Range: &KeyPart{Name: "sk", Type: "S"},
	}); err != nil {
		b.Fatal(err)
	}
	for i := range n {
		raw := fmt.Sprintf(`{"pk":{"S":"customer#%d"},"sk":{"S":"order#%06d"},`+
			`"total":{"N":"%d.5"},"currency":{"S":"GBP"},"tier":{"S":"gold"},`+
			`"tags":{"SS":["priority","gift"]}}`, i%100, i, 50+i%500)
		if _, err := s.PutItem("bench", json.RawMessage(raw), nil); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// scanFilter is the same expression shape internal/ddb/expr benchmarks, so the
// per-item cost measured there is comparable with the loop measured here.
func scanFilter(b *testing.B) *expr.Cond {
	b.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{":min":{"N":"100"},":prefix":{"S":"order#"},":excluded":{"S":"cancelled"}}`), &m); err != nil {
		b.Fatal(err)
	}
	vals := map[string]item.Value{}
	for k, raw := range m {
		v, aerr := item.FromJSON(raw)
		if aerr != nil {
			b.Fatal(aerr)
		}
		vals[k] = v
	}
	cond, aerr := expr.ParseCondition(
		`total > :min AND begins_with(sk, :prefix) AND attribute_exists(tier) AND NOT contains(tags, :excluded)`,
		expr.NewEnv(nil, vals))
	if aerr != nil {
		b.Fatal(aerr)
	}
	return cond
}

func benchScan(b *testing.B, n int, filter *expr.Cond) {
	s := benchStore(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out, err := s.Scan(ScanInput{Table: "bench", Filter: filter})
		if err != nil {
			b.Fatal(err)
		}
		if len(out.Items) == 0 {
			b.Fatal("scan returned nothing — the fixture is wrong, not the code")
		}
	}
}

func BenchmarkScan1k(b *testing.B)          { benchScan(b, 1000, nil) }
func BenchmarkScan10k(b *testing.B)         { benchScan(b, 10000, nil) }
func BenchmarkScanFiltered1k(b *testing.B)  { benchScan(b, 1000, scanFilter(b)) }
func BenchmarkScanFiltered10k(b *testing.B) { benchScan(b, 10000, scanFilter(b)) }

// Query under one hash key: the same store, reached by key rather than walked.
func BenchmarkQuery10k(b *testing.B) {
	s := benchStore(b, 10000)
	vals := map[string]item.Value{}
	v, aerr := item.FromJSON(json.RawMessage(`{"S":"customer#7"}`))
	if aerr != nil {
		b.Fatal(aerr)
	}
	vals[":pk"] = v
	kc, aerr := expr.ParseKeyCondition("pk = :pk", expr.NewEnv(nil, vals))
	if aerr != nil {
		b.Fatal(aerr)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out, err := s.Query(QueryInput{Table: "bench", KeyCond: kc})
		if err != nil {
			b.Fatal(err)
		}
		if len(out.Items) == 0 {
			b.Fatal("query returned nothing — the fixture is wrong, not the code")
		}
	}
}
