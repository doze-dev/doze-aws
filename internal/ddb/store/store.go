// Package store is the DynamoDB storage engine: tables and items in bbolt,
// GSI/LSI entries maintained in the same transaction as every write, range
// queries via order-preserving key encodings (keyenc), single-node
// transactions with real atomicity, and TTL enforcement.
//
// bbolt layout:
//
//	tables            name -> Table JSON
//	d:<table>         keyenc(pk[,sk]) -> item wire JSON
//	x:<table>/<index> keyenc(ipk[,isk]) ++ keyenc(pk[,sk]) -> primary key bytes
//	tx:               ClientRequestToken -> {hash, expiry} (idempotency)
//
// Index entries are key references (no projected copies): reads chase the
// reference to the base item and apply the index projection — microseconds
// locally, and index consistency is free.
package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
	"github.com/doze-dev/doze-aws/internal/ddb/keyenc"
)

var (
	tablesBucket = []byte("tables")
	txBucket     = []byte("tx")
)

func dataBucket(table string) []byte { return []byte("d:" + table) }
func indexBucket(table, index string) []byte {
	return []byte("x:" + table + "/" + index)
}

// KeyPart names one key attribute.
type KeyPart struct {
	Name string `json:"name"`
	Type string `json:"type"` // S | N | B
}

// Index describes a GSI or LSI.
type Index struct {
	Name        string   `json:"name"`
	Hash        KeyPart  `json:"hash"`
	Range       *KeyPart `json:"range,omitempty"`
	Projection  string   `json:"projection"` // ALL | KEYS_ONLY | INCLUDE
	NonKeyAttrs []string `json:"non_key_attrs,omitempty"`
	Local       bool     `json:"local"`
}

// Table is a table definition.
type Table struct {
	Name    string   `json:"name"`
	Hash    KeyPart  `json:"hash"`
	Range   *KeyPart `json:"range,omitempty"`
	Indexes []Index  `json:"indexes,omitempty"`
	Created int64    `json:"created"`

	TTLAttribute string `json:"ttl_attribute,omitempty"`
	TTLEnabled   bool   `json:"ttl_enabled,omitempty"`

	Tags map[string]string `json:"tags,omitempty"`

	// Cosmetic round-trips.
	BillingMode        string `json:"billing_mode,omitempty"`
	DeletionProtection bool   `json:"deletion_protection,omitempty"`
	StreamSpec         string `json:"stream_spec,omitempty"` // stored, inert (streams post-1.0)
	ItemCount          int64  `json:"item_count"`
}

// ARN returns the table ARN.
func (t *Table) ARN() string { return awsident.ARN("dynamodb", "table/"+t.Name) }

// FindIndex locates an index by name.
func (t *Table) FindIndex(name string) *Index {
	for i := range t.Indexes {
		if t.Indexes[i].Name == name {
			return &t.Indexes[i]
		}
	}
	return nil
}

// Store is the bbolt-backed DynamoDB engine.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

// New wraps an open bbolt DB.
func New(db *bolt.DB) *Store { return &Store{db: db, clock: time.Now} }

// SetClock overrides the clock (tests).
func (s *Store) SetClock(fn func() time.Time) { s.clock = fn }

func (s *Store) now() time.Time { return s.clock() }

// DB exposes the handle for Close.
func (s *Store) DB() *bolt.DB { return s.db }

func errTableNotFound(name string) *awshttp.APIError {
	return awshttp.Errf(400, "ResourceNotFoundException", "Requested resource not found: Table: %s not found", name)
}

// ---- tables ----

// CreateTable registers a table.
func (s *Store) CreateTable(t Table) (*Table, error) {
	if t.Name == "" {
		return nil, awshttp.Errf(400, "ValidationException", "TableName is required")
	}
	t.Created = s.now().Unix()
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(tablesBucket)
		if err != nil {
			return err
		}
		if b.Get([]byte(t.Name)) != nil {
			return awshttp.Errf(400, "ResourceInUseException", "Table already exists: %s", t.Name)
		}
		raw, _ := json.Marshal(t)
		return b.Put([]byte(t.Name), raw)
	})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTable loads a table definition.
func (s *Store) GetTable(name string) (*Table, error) {
	var out *Table
	err := s.db.View(func(tx *bolt.Tx) error {
		t, err := getTable(tx, name)
		if err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

func getTable(tx *bolt.Tx, name string) (*Table, error) {
	b := tx.Bucket(tablesBucket)
	if b == nil {
		return nil, errTableNotFound(name)
	}
	raw := b.Get([]byte(name))
	if raw == nil {
		return nil, errTableNotFound(name)
	}
	var t Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTable applies fn to a table definition. Adding a GSI triggers a
// synchronous backfill (local data volumes make this instant-ish).
func (s *Store) UpdateTable(name string, fn func(*Table) error) (*Table, error) {
	var out *Table
	err := s.db.Update(func(tx *bolt.Tx) error {
		t, err := getTable(tx, name)
		if err != nil {
			return err
		}
		// Snapshot the index set by name so we can tell exactly which indexes
		// fn added, removed, or redefined — a positional count breaks when a
		// delete shifts the slice and never reclaims the removed index's bucket.
		before := make(map[string]Index, len(t.Indexes))
		for i := range t.Indexes {
			before[t.Indexes[i].Name] = t.Indexes[i]
		}
		if err := fn(t); err != nil {
			return err
		}
		raw, _ := json.Marshal(t)
		if err := tx.Bucket(tablesBucket).Put([]byte(name), raw); err != nil {
			return err
		}
		after := make(map[string]bool, len(t.Indexes))
		for i := range t.Indexes {
			after[t.Indexes[i].Name] = true
			prev, existed := before[t.Indexes[i].Name]
			// Backfill an index that is new, or was redefined under the same
			// name (dropping stale entries encoded under the old key schema).
			if !existed || !sameIndexSchema(prev, t.Indexes[i]) {
				_ = tx.DeleteBucket(indexBucket(name, t.Indexes[i].Name))
				if err := s.backfillIndex(tx, t, &t.Indexes[i]); err != nil {
					return err
				}
			}
		}
		// Reclaim buckets of indexes fn removed.
		for nm := range before {
			if !after[nm] {
				_ = tx.DeleteBucket(indexBucket(name, nm))
			}
		}
		out = t
		return nil
	})
	return out, err
}

// sameIndexSchema reports whether two indexes of the same name share a key
// schema, so an UpdateTable that leaves an index untouched skips a needless
// (and stale-clearing) rebuild.
func sameIndexSchema(a, b Index) bool {
	if a.Hash != b.Hash || a.Projection != b.Projection {
		return false
	}
	if (a.Range == nil) != (b.Range == nil) {
		return false
	}
	if a.Range != nil && *a.Range != *b.Range {
		return false
	}
	return true
}

// backfillIndex scans the base table and writes entries for one new index.
func (s *Store) backfillIndex(tx *bolt.Tx, t *Table, idx *Index) error {
	db := tx.Bucket(dataBucket(t.Name))
	if db == nil {
		return nil
	}
	xb, err := tx.CreateBucketIfNotExists(indexBucket(t.Name, idx.Name))
	if err != nil {
		return err
	}
	return db.ForEach(func(k, raw []byte) error {
		it, aerr := item.ItemFromJSON(raw)
		if aerr != nil {
			return aerr
		}
		xkey, ok, err := indexKeyFor(idx, it, k)
		if err != nil || !ok {
			return err
		}
		return xb.Put(xkey, k)
	})
}

// DeleteTable removes a table and its data.
func (s *Store) DeleteTable(name string) (*Table, error) {
	var out *Table
	err := s.db.Update(func(tx *bolt.Tx) error {
		t, err := getTable(tx, name)
		if err != nil {
			return err
		}
		if t.DeletionProtection {
			return awshttp.Errf(400, "ValidationException", "table %s has deletion protection enabled", name)
		}
		out = t
		_ = tx.Bucket(tablesBucket).Delete([]byte(name))
		_ = tx.DeleteBucket(dataBucket(name))
		for _, idx := range t.Indexes {
			_ = tx.DeleteBucket(indexBucket(name, idx.Name))
		}
		return nil
	})
	return out, err
}

// ListTables returns table names in order.
func (s *Store) ListTables() ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(tablesBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			out = append(out, string(k))
			return nil
		})
	})
	sort.Strings(out)
	return out, err
}

// ---- keys ----

// primaryKey extracts and encodes an item's primary key per the table schema.
func primaryKey(t *Table, it item.Item) ([]byte, *awshttp.APIError) {
	pk, ok := it[t.Hash.Name]
	if !ok {
		return nil, awshttp.Errf(400, "ValidationException", "missing the key %s in the item", t.Hash.Name)
	}
	if string(pk.Type) != t.Hash.Type {
		return nil, awshttp.Errf(400, "ValidationException",
			"key %s expects type %s, got %s", t.Hash.Name, t.Hash.Type, pk.Type)
	}
	var skPtr *item.Value
	if t.Range != nil {
		sk, ok := it[t.Range.Name]
		if !ok {
			return nil, awshttp.Errf(400, "ValidationException", "missing the key %s in the item", t.Range.Name)
		}
		if string(sk.Type) != t.Range.Type {
			return nil, awshttp.Errf(400, "ValidationException",
				"key %s expects type %s, got %s", t.Range.Name, t.Range.Type, sk.Type)
		}
		skPtr = &sk
	}
	kb, err := keyenc.Composite(pk, skPtr)
	if err != nil {
		return nil, awshttp.Errf(400, "ValidationException", "%v", err)
	}
	return kb, nil
}

// indexKeyFor computes an item's entry key in an index (ok=false for sparse
// misses: absent or mistyped index key attributes).
func indexKeyFor(idx *Index, it item.Item, primary []byte) ([]byte, bool, error) {
	pk, ok := it[idx.Hash.Name]
	if !ok || string(pk.Type) != idx.Hash.Type {
		return nil, false, nil
	}
	var skPtr *item.Value
	if idx.Range != nil {
		sk, ok := it[idx.Range.Name]
		if !ok || string(sk.Type) != idx.Range.Type {
			return nil, false, nil
		}
		skPtr = &sk
	}
	kb, err := keyenc.Composite(pk, skPtr)
	if err != nil {
		return nil, false, err
	}
	// Suffix with the primary key for uniqueness (many items may share index keys).
	out := make([]byte, 0, len(kb)+len(primary)+4)
	out = append(out, kb...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(primary)))
	out = append(out, primary...)
	return out, true, nil
}

// keyFromWire decodes a request's Key map and encodes it.
func (s *Store) KeyFromWire(t *Table, raw json.RawMessage) ([]byte, item.Item, *awshttp.APIError) {
	key, aerr := item.ItemFromJSON(raw)
	if aerr != nil {
		return nil, nil, aerr
	}
	want := 1
	if t.Range != nil {
		want = 2
	}
	if len(key) != want {
		return nil, nil, awshttp.Errf(400, "ValidationException", "the provided key element does not match the schema")
	}
	kb, aerr := primaryKey(t, key)
	if aerr != nil {
		return nil, nil, aerr
	}
	return kb, key, nil
}

// ---- TTL ----

// expired reports whether an item is past its TTL.
func (s *Store) expired(t *Table, it item.Item) bool {
	if !t.TTLEnabled || t.TTLAttribute == "" {
		return false
	}
	v, ok := it[t.TTLAttribute]
	if !ok || v.Type != item.TypeN {
		return false
	}
	epoch, aerr := item.ParseDecimal(fmt.Sprintf("%d", s.now().Unix()))
	if aerr != nil {
		return false
	}
	return item.Compare(v.N, epoch) <= 0
}

// SweepTTL removes expired items across every TTL-enabled table, through the
// normal delete path so indexes stay consistent.
func (s *Store) SweepTTL() {
	names, err := s.ListTables()
	if err != nil {
		return
	}
	for _, name := range names {
		t, err := s.GetTable(name)
		if err != nil || !t.TTLEnabled {
			continue
		}
		var doomed []json.RawMessage
		_ = s.db.View(func(tx *bolt.Tx) error {
			db := tx.Bucket(dataBucket(name))
			if db == nil {
				return nil
			}
			return db.ForEach(func(_, raw []byte) error {
				it, aerr := item.ItemFromJSON(raw)
				if aerr != nil {
					return nil
				}
				if s.expired(t, it) {
					doomed = append(doomed, keyOf(t, it))
				}
				return nil
			})
		})
		for _, keyRaw := range doomed {
			_, _ = s.DeleteItem(name, keyRaw, nil, "NONE")
		}
	}
}

// keyOf renders an item's key attributes back to wire form.
func keyOf(t *Table, it item.Item) json.RawMessage {
	key := item.Item{t.Hash.Name: it[t.Hash.Name]}
	if t.Range != nil {
		key[t.Range.Name] = it[t.Range.Name]
	}
	return item.ItemJSON(key)
}
