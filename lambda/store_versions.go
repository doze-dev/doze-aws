package lambda

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

// Published versions: a frozen copy of the function record per number,
// with its own code directory, so an alias that points at version 3 runs
// version 3 after $LATEST has moved on.

var versionsBucket = []byte("versions")

func versionKey(name string, n int) []byte {
	return []byte(fmt.Sprintf("%s\x00%010d", name, n))
}

// PutVersion stores a frozen record under its number.
func (s *Store) PutVersion(f *Function) error {
	n, err := strconv.Atoi(f.Version)
	if err != nil {
		return fmt.Errorf("version %q is not a number", f.Version)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(versionsBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(f)
		return b.Put(versionKey(f.Name, n), raw)
	})
}

// GetVersion loads one frozen version, or the function-not-found error.
func (s *Store) GetVersion(name string, n int) (*Function, error) {
	var out *Function
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(versionsBucket)
		if b == nil {
			return errFuncNotFound(name + ":" + strconv.Itoa(n))
		}
		raw := b.Get(versionKey(name, n))
		if raw == nil {
			return errFuncNotFound(name + ":" + strconv.Itoa(n))
		}
		var f Function
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		out = &f
		return nil
	})
	return out, err
}

// ListVersions answers a function's versions, oldest first.
func (s *Store) ListVersions(name string) ([]*Function, error) {
	var out []*Function
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(versionsBucket)
		if b == nil {
			return nil
		}
		prefix := []byte(name + "\x00")
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var f Function
			if err := json.Unmarshal(v, &f); err != nil {
				return err
			}
			out = append(out, &f)
		}
		return nil
	})
	return out, err
}

// DeleteVersion removes one version's record.
func (s *Store) DeleteVersion(name string, n int) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(versionsBucket)
		if b == nil {
			return nil
		}
		return b.Delete(versionKey(name, n))
	})
}

// DeleteVersions removes every version of a function.
func (s *Store) DeleteVersions(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(versionsBucket)
		if b == nil {
			return nil
		}
		prefix := []byte(name + "\x00")
		c := b.Cursor()
		var keys [][]byte
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}
