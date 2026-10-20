package schemaver

import (
	"fmt"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// A version record that is not four bytes is not a version: it is a damaged
// file, and the message says so rather than reading half a number.
func TestEnsureNamesACorruptVersionRecord(t *testing.T) {
	for _, raw := range [][]byte{{}, {1}, {0, 0, 0}, {0, 0, 0, 0, 1}} {
		db := openDB(t)
		if err := db.Update(func(tx *bolt.Tx) error {
			b, _ := tx.CreateBucketIfNotExists(metaBucket)
			return b.Put(versionKey, raw)
		}); err != nil {
			t.Fatal(err)
		}
		err := Ensure(db, "svc", Current)
		if err == nil || err.Error() != "svc: corrupt schema-version record" {
			t.Errorf("a %d-byte record: got %v", len(raw), err)
		}
	}
}

// Data from an older schema with no migration to bring it forward is refused,
// and the message says which versions: Current is 1 today, so the case is made
// by asking for a later one.
func TestEnsureRefusesAnOlderSchemaWithNoMigration(t *testing.T) {
	db := openDB(t)
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatal(err)
	}
	err := Ensure(db, "svc", Current+1)
	want := fmt.Sprintf("svc: on-disk schema v%d predates v%d and no migration is available", Current, Current+1)
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}
