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
func Ensure(db *bolt.DB, service string, current uint32) error {
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

func encode(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}
