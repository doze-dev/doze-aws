package schemaver

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func openDB(t *testing.T) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "t.bolt"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnsureStampsAndAccepts(t *testing.T) {
	db := openDB(t)
	// Fresh DB: stamps Current and succeeds.
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	// Second open at the same version: accepted.
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatalf("re-Ensure: %v", err)
	}
}

func TestEnsureRejectsNewerSchema(t *testing.T) {
	db := openDB(t)
	// Simulate a DB written by a newer binary.
	err := db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists(metaBucket)
		var v [4]byte
		binary.BigEndian.PutUint32(v[:], Current+1)
		return b.Put(versionKey, v[:])
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(db, "svc", Current); err == nil {
		t.Fatal("Ensure should refuse a newer on-disk schema")
	}
}

func TestEnsureTreatsUnversionedAsCurrent(t *testing.T) {
	db := openDB(t)
	// Pre-versioning DB with data but no _schema bucket.
	if err := db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("data"))
		return b.Put([]byte("k"), []byte("v"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatalf("unversioned DB should be accepted and stamped: %v", err)
	}
	// The data must survive, and the version is now stamped.
	_ = db.View(func(tx *bolt.Tx) error {
		if got := tx.Bucket([]byte("data")).Get([]byte("k")); string(got) != "v" {
			t.Fatalf("data lost: %q", got)
		}
		if raw := tx.Bucket(metaBucket).Get(versionKey); binary.BigEndian.Uint32(raw) != Current {
			t.Fatal("version not stamped")
		}
		return nil
	})
}
