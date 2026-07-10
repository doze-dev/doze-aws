package store

// Query and Scan: cursor walks over the order-preserving key encodings, with
// DynamoDB's paging semantics — Limit counts items evaluated BEFORE the filter,
// LastEvaluatedKey/ExclusiveStartKey are plain key-attribute maps, and index
// queries chase key references back to base items.

import (
	"bytes"
	"encoding/json"
	"hash/fnv"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/expr"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
	"github.com/doze-dev/doze-aws/internal/ddb/keyenc"
)

// pageSizeBytes mirrors DynamoDB's 1 MB evaluated-data page bound.
const pageSizeBytes = 1 << 20

// QueryInput drives Query.
type QueryInput struct {
	Table    string
	Index    string // optional
	KeyCond  *expr.KeyCondition
	Filter   *expr.Cond
	Forward  bool // ScanIndexForward (default true)
	Limit    int
	StartKey json.RawMessage // ExclusiveStartKey (item key attrs, incl. index keys)
}

// QueryOutput is a page of results.
type QueryOutput struct {
	Items            []item.Item
	Count            int
	ScannedCount     int
	LastEvaluatedKey item.Item // nil when the page completes the result set
}

// Query runs a key-condition query against the table or one of its indexes.
func (s *Store) Query(in QueryInput) (*QueryOutput, error) {
	out := &QueryOutput{}
	err := s.db.View(func(tx *bolt.Tx) error {
		t, err := getTable(tx, in.Table)
		if err != nil {
			return err
		}

		// Resolve the key schema being queried.
		hash, rng := t.Hash, t.Range
		var idx *Index
		if in.Index != "" {
			idx = t.FindIndex(in.Index)
			if idx == nil {
				return awshttp.Errf(400, "ValidationException", "table %s has no index %s", in.Table, in.Index)
			}
			hash, rng = idx.Hash, idx.Range
		}
		if in.KeyCond.PKName != hash.Name {
			return awshttp.Errf(400, "ValidationException",
				"query condition tests %s, but the partition key is %s", in.KeyCond.PKName, hash.Name)
		}
		if in.KeyCond.SKName != "" && (rng == nil || in.KeyCond.SKName != rng.Name) {
			return awshttp.Errf(400, "ValidationException", "query condition tests a non-sort-key attribute %s", in.KeyCond.SKName)
		}

		pkEnc, kerr := keyenc.Encode(in.KeyCond.PKValue)
		if kerr != nil {
			return awshttp.Errf(400, "ValidationException", "%v", kerr)
		}

		var bucket *bolt.Bucket
		if idx != nil {
			bucket = tx.Bucket(indexBucket(t.Name, idx.Name))
		} else {
			bucket = tx.Bucket(dataBucket(t.Name))
		}
		if bucket == nil {
			return nil
		}
		dataB := tx.Bucket(dataBucket(t.Name))

		// Resolve the exclusive start position.
		var startAfter []byte
		if len(in.StartKey) > 0 {
			sk, aerr := s.startPosition(t, idx, in.StartKey)
			if aerr != nil {
				return aerr
			}
			startAfter = sk
		}

		c := bucket.Cursor()
		scannedBytes := 0

		// visit processes one entry; returns false to stop the walk.
		visit := func(k, v []byte) (bool, error) {
			if !bytes.HasPrefix(k, pkEnc) {
				return false, nil
			}
			// Load the candidate item.
			var raw []byte
			if idx != nil {
				raw = dataB.Get(v)
			} else {
				raw = v
			}
			if raw == nil {
				return true, nil
			}
			it, aerr := item.ItemFromJSON(raw)
			if aerr != nil {
				return false, aerr
			}
			if s.expired(t, it) {
				return true, nil
			}
			// Sort-key condition.
			if in.KeyCond.SKName != "" {
				match, aerr := skMatches(in.KeyCond, it)
				if aerr != nil {
					return false, aerr
				}
				if !match {
					return true, nil
				}
			}
			out.ScannedCount++
			scannedBytes += len(raw)
			kept := true
			if in.Filter != nil {
				ok, aerr := in.Filter.Eval(it)
				if aerr != nil {
					return false, aerr
				}
				kept = ok
			}
			if kept {
				out.Items = append(out.Items, projectIndex(idx, t, it))
				out.Count++
			}
			// Page bounds: Limit counts pre-filter evaluations; 1 MB bound too.
			if (in.Limit > 0 && out.ScannedCount >= in.Limit) || scannedBytes >= pageSizeBytes {
				out.LastEvaluatedKey = lastKey(t, idx, it)
				return false, nil
			}
			return true, nil
		}

		walk := func() error {
			if in.Forward {
				k, v := c.Seek(pkEnc)
				if startAfter != nil {
					k, v = c.Seek(startAfter)
					if bytes.Equal(k, startAfter) {
						k, v = c.Next()
					}
				}
				for ; k != nil; k, v = c.Next() {
					cont, err := visit(k, v)
					if err != nil || !cont {
						return err
					}
				}
				return nil
			}
			// Backward: position at the LAST entry with the pk prefix.
			var k, v []byte
			if startAfter != nil {
				k, v = c.Seek(startAfter)
				if k != nil {
					k, v = c.Prev()
				} else {
					k, v = c.Last()
				}
			} else {
				// Seek past the prefix, then step back.
				upper := append(append([]byte(nil), pkEnc...), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
				k, v = c.Seek(upper)
				if k == nil {
					k, v = c.Last()
				} else {
					k, v = c.Prev()
				}
			}
			for ; k != nil; k, v = c.Prev() {
				if !bytes.HasPrefix(k, pkEnc) {
					if bytes.Compare(k, pkEnc) < 0 {
						return nil
					}
					continue
				}
				cont, err := visit(k, v)
				if err != nil || !cont {
					return err
				}
			}
			return nil
		}
		return walk()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// skMatches evaluates the sort-key clause against an item.
func skMatches(kc *expr.KeyCondition, it item.Item) (bool, *awshttp.APIError) {
	v, ok := it[kc.SKName]
	if !ok {
		return false, nil
	}
	cmp := func(a, b item.Value) (int, bool) {
		if a.Type != b.Type {
			return 0, false
		}
		return keyenc.CompareValues(a, b), true
	}
	switch kc.SKOp {
	case "=":
		return item.Equal(v, kc.SKValue), nil
	case "<", "<=", ">", ">=":
		c, ok := cmp(v, kc.SKValue)
		if !ok {
			return false, nil
		}
		switch kc.SKOp {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	case "BETWEEN":
		lo, ok1 := cmp(v, kc.SKValue)
		hi, ok2 := cmp(v, kc.SKValue2)
		return ok1 && ok2 && lo >= 0 && hi <= 0, nil
	case "begins_with":
		if v.Type == item.TypeS && kc.SKValue.Type == item.TypeS {
			return len(v.S) >= len(kc.SKValue.S) && v.S[:len(kc.SKValue.S)] == kc.SKValue.S, nil
		}
		if v.Type == item.TypeB && kc.SKValue.Type == item.TypeB {
			return bytes.HasPrefix(v.B, kc.SKValue.B), nil
		}
		return false, nil
	}
	return false, awshttp.Errf(400, "ValidationException", "unsupported sort key operator %q", kc.SKOp)
}

// projectIndex applies an index's projection to an item.
func projectIndex(idx *Index, t *Table, it item.Item) item.Item {
	if idx == nil || idx.Projection == "" || idx.Projection == "ALL" {
		return it
	}
	keep := map[string]bool{
		t.Hash.Name:   true,
		idx.Hash.Name: true,
	}
	if t.Range != nil {
		keep[t.Range.Name] = true
	}
	if idx.Range != nil {
		keep[idx.Range.Name] = true
	}
	if idx.Projection == "INCLUDE" {
		for _, a := range idx.NonKeyAttrs {
			keep[a] = true
		}
	}
	out := item.Item{}
	for k, v := range it {
		if keep[k] {
			out[k] = v
		}
	}
	return out
}

// lastKey builds the LastEvaluatedKey attribute map for an item.
func lastKey(t *Table, idx *Index, it item.Item) item.Item {
	out := item.Item{t.Hash.Name: it[t.Hash.Name]}
	if t.Range != nil {
		out[t.Range.Name] = it[t.Range.Name]
	}
	if idx != nil {
		out[idx.Hash.Name] = it[idx.Hash.Name]
		if idx.Range != nil {
			out[idx.Range.Name] = it[idx.Range.Name]
		}
	}
	return out
}

// startPosition re-encodes an ExclusiveStartKey into the cursor key to resume
// after.
func (s *Store) startPosition(t *Table, idx *Index, raw json.RawMessage) ([]byte, *awshttp.APIError) {
	attrs, aerr := item.ItemFromJSON(raw)
	if aerr != nil {
		return nil, aerr
	}
	primary, aerr := primaryKey(t, attrs)
	if aerr != nil {
		return nil, aerr
	}
	if idx == nil {
		return primary, nil
	}
	xkey, ok, err := indexKeyFor(idx, attrs, primary)
	if err != nil || !ok {
		return nil, awshttp.Errf(400, "ValidationException", "ExclusiveStartKey does not match the index schema")
	}
	return xkey, nil
}

// ScanInput drives Scan.
type ScanInput struct {
	Table         string
	Index         string
	Filter        *expr.Cond
	Limit         int
	StartKey      json.RawMessage
	Segment       int
	TotalSegments int
}

// Scan walks a table (or index) with optional segment partitioning.
func (s *Store) Scan(in ScanInput) (*QueryOutput, error) {
	out := &QueryOutput{}
	err := s.db.View(func(tx *bolt.Tx) error {
		t, err := getTable(tx, in.Table)
		if err != nil {
			return err
		}
		var idx *Index
		bucket := tx.Bucket(dataBucket(t.Name))
		if in.Index != "" {
			idx = t.FindIndex(in.Index)
			if idx == nil {
				return awshttp.Errf(400, "ValidationException", "table %s has no index %s", in.Table, in.Index)
			}
			bucket = tx.Bucket(indexBucket(t.Name, idx.Name))
		}
		if bucket == nil {
			return nil
		}
		dataB := tx.Bucket(dataBucket(t.Name))

		var startAfter []byte
		if len(in.StartKey) > 0 {
			sk, aerr := s.startPosition(t, idx, in.StartKey)
			if aerr != nil {
				return aerr
			}
			startAfter = sk
		}

		c := bucket.Cursor()
		k, v := c.First()
		if startAfter != nil {
			k, v = c.Seek(startAfter)
			if bytes.Equal(k, startAfter) {
				k, v = c.Next()
			}
		}
		scannedBytes := 0
		for ; k != nil; k, v = c.Next() {
			raw := v
			if idx != nil {
				raw = dataB.Get(v)
				if raw == nil {
					continue
				}
			}
			it, aerr := item.ItemFromJSON(raw)
			if aerr != nil {
				return aerr
			}
			if s.expired(t, it) {
				continue
			}
			// Segmenting: hash the encoded partition key into segments.
			if in.TotalSegments > 1 {
				if segmentOf(k, in.TotalSegments) != in.Segment {
					continue
				}
			}
			out.ScannedCount++
			scannedBytes += len(raw)
			kept := true
			if in.Filter != nil {
				ok, aerr := in.Filter.Eval(it)
				if aerr != nil {
					return aerr
				}
				kept = ok
			}
			if kept {
				out.Items = append(out.Items, projectIndex(idx, t, it))
				out.Count++
			}
			if (in.Limit > 0 && out.ScannedCount >= in.Limit) || scannedBytes >= pageSizeBytes {
				out.LastEvaluatedKey = lastKey(t, idx, it)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func segmentOf(key []byte, total int) int {
	h := fnv.New32a()
	h.Write(key)
	return int(h.Sum32()) % total
}
