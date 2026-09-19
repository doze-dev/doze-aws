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

// txid reports the id of the last committed transaction. bbolt advances it on
// every writable transaction and never on a read-only one, so comparing it
// across a call is an exact answer to "did that write".
func txid(t *testing.T, db *bolt.DB) uint64 {
	t.Helper()
	var id uint64
	if err := db.View(func(tx *bolt.Tx) error {
		id = uint64(tx.ID())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// Ensure on a database that is already at the current version must not open a
// writable transaction, because bbolt commits a meta page and FSYNCS on every
// one of those whether or not anything changed. Sixteen services each paying a
// disk flush at startup to be told nothing had changed was 85% of warm boot.
//
// This is asserted here, exactly, rather than through the boot timing it used
// to show up in. Lazy opening removed that signal: no database is opened at
// startup any more, so a reintroduced flush would not move boot at all — it
// would move the first request to each service, where no band is watching.
func TestEnsureDoesNotWriteWhenTheVersionIsAlreadyCurrent(t *testing.T) {
	db := openDB(t)
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	before := txid(t, db)
	for range 3 {
		if err := Ensure(db, "svc", Current); err != nil {
			t.Fatalf("re-Ensure: %v", err)
		}
	}
	if after := txid(t, db); after != before {
		t.Errorf("Ensure took %d writable transaction(s) on an up-to-date database "+
			"(txid %d -> %d), want none.\n"+
			"  Every one of those is an fsync, paid by each service the first time it "+
			"is used.\n  Ensure must read the stored version and return before taking "+
			"db.Update.", after-before, before, after)
	}

	// The write path still runs where it has to: a version that is not current
	// must be stamped, or the read fast path has swallowed the migration.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metaBucket).Delete(versionKey)
	}); err != nil {
		t.Fatal(err)
	}
	before = txid(t, db)
	if err := Ensure(db, "svc", Current); err != nil {
		t.Fatalf("Ensure on an unversioned database: %v", err)
	}
	if txid(t, db) == before {
		t.Error("Ensure wrote nothing to an unversioned database — the read fast " +
			"path is skipping the stamp, not just the no-op")
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
