// Package schemaver stamps and verifies a persistence schema version inside each
// service's bbolt database. Every store writes plain JSON structs into bbolt
// buckets; without a version marker, loading data written by a different binary
// could silently corrupt (a renamed or semantically-changed field deserializes
// into the wrong shape). Ensure records the current version on first use and,
// on later opens, refuses a database written by a newer schema rather than
// misread it.
package schemaver

import (
	"encoding/binary"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// Current is the schema version every store writes today. Bump it (and add a
// migration in Ensure) when a persisted struct changes incompatibly.
const Current uint32 = 1

var (
	metaBucket = []byte("_schema")
	versionKey = []byte("version")
)

// Ensure verifies (or, on a fresh/pre-versioning database, stamps) the schema
// version. service names the store for error messages. An unversioned database
// is treated as Current and stamped in place (existing data is v1). A database
// written by a newer schema is rejected — refusing to load is safer than
// silently misinterpreting fields.
// Ensure reads before it writes, which is the whole performance story of
// starting doze-aws.
//
// It used to open with db.Update unconditionally. bbolt commits a meta page and
// fsyncs on every writable transaction whether or not anything changed, so
// every service paid a disk flush at startup to be told its schema version was
// already right. Measured on one service: 5.92ms of a 6.98ms warm boot — 85% —
// and with sixteen stateful services that is the bulk of the startup time.
//
// The read path takes no write transaction at all. The write path is unchanged
// and still runs for a fresh database, an unversioned one, or an error.
func Ensure(db *bolt.DB, service string, current uint32) error {
	// The common case by far: an existing database at the current version.
	// A View transaction takes no lock a writer would wait on and never
	// touches the disk.
	if ok, err := isCurrent(db, current); err != nil || ok {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(metaBucket)
		if err != nil {
			return err
		}
		raw := b.Get(versionKey)
		if raw == nil {
			return b.Put(versionKey, encode(current))
		}
		if len(raw) != 4 {
			return fmt.Errorf("%s: corrupt schema-version record", service)
		}
		stored := binary.BigEndian.Uint32(raw)
		switch {
		case stored == current:
			return nil
		case stored > current:
			return fmt.Errorf("%s: data was written by a newer doze-aws (on-disk schema v%d > supported v%d); upgrade doze-aws or use a fresh data dir", service, stored, current)
		default:
			// No downgrade migrations exist yet; when they do, run them here and
			// re-stamp instead of erroring.
			return fmt.Errorf("%s: on-disk schema v%d predates v%d and no migration is available", service, stored, current)
		}
	})
}

// isCurrent reports whether the database already carries exactly the version
// asked for. Anything else — absent, wrong length, older, newer — is false, so
// the write path below decides what it means. This only answers the one
// question that lets startup skip a disk flush; it never interprets.
func isCurrent(db *bolt.DB, current uint32) (bool, error) {
	var ok bool
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(metaBucket)
		if b == nil {
			return nil
		}
		raw := b.Get(versionKey)
		ok = len(raw) == 4 && binary.BigEndian.Uint32(raw) == current
		return nil
	})
	return ok, err
}

func encode(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}
