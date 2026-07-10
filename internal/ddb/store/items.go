package store

// Item write/read operations: put, get, update, delete — each maintaining
// every index in the same bbolt transaction, with optional condition
// expressions evaluated against the current item state.

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/expr"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// ErrConditionFailed is raised when a ConditionExpression evaluates false.
func errConditionFailed(old item.Item, returnOld bool) *awshttp.APIError {
	e := awshttp.Errf(400, "ConditionalCheckFailedException", "The conditional request failed")
	if returnOld && old != nil {
		e.Item = item.ItemJSON(old)
	}
	return e
}

// Cond wraps an optional parsed condition.
type Cond struct {
	Expr *expr.Cond
	Env  *expr.Env
	// ReturnOld controls ReturnValuesOnConditionCheckFailure.
	ReturnOld bool
}

// check evaluates the condition against the current item (nil = absent).
func (c *Cond) check(cur item.Item) *awshttp.APIError {
	if c == nil || c.Expr == nil {
		return nil
	}
	target := cur
	if target == nil {
		target = item.Item{}
	}
	ok, aerr := c.Expr.Eval(target)
	if aerr != nil {
		return aerr
	}
	if !ok {
		return errConditionFailed(cur, c.ReturnOld)
	}
	return nil
}

// writeItem stores an item and refreshes its index entries within tx.
func (s *Store) writeItem(tx *bolt.Tx, t *Table, key []byte, old, new item.Item) error {
	db, err := tx.CreateBucketIfNotExists(dataBucket(t.Name))
	if err != nil {
		return err
	}
	if new == nil {
		if err := db.Delete(key); err != nil {
			return err
		}
	} else {
		if err := db.Put(key, item.ItemJSON(new)); err != nil {
			return err
		}
	}
	// Index maintenance: remove old entries whose keys changed, add new ones.
	for i := range t.Indexes {
		idx := &t.Indexes[i]
		xb, err := tx.CreateBucketIfNotExists(indexBucket(t.Name, idx.Name))
		if err != nil {
			return err
		}
		var oldKey, newKey []byte
		var oldOK, newOK bool
		if old != nil {
			oldKey, oldOK, err = indexKeyFor(idx, old, key)
			if err != nil {
				return err
			}
		}
		if new != nil {
			newKey, newOK, err = indexKeyFor(idx, new, key)
			if err != nil {
				return err
			}
		}
		if oldOK && (!newOK || string(oldKey) != string(newKey)) {
			if err := xb.Delete(oldKey); err != nil {
				return err
			}
		}
		if newOK {
			if err := xb.Put(newKey, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadItem fetches the current item for a key within tx (nil when absent or
// TTL-expired).
func (s *Store) loadItem(tx *bolt.Tx, t *Table, key []byte) (item.Item, *awshttp.APIError) {
	db := tx.Bucket(dataBucket(t.Name))
	if db == nil {
		return nil, nil
	}
	raw := db.Get(key)
	if raw == nil {
		return nil, nil
	}
	it, aerr := item.ItemFromJSON(raw)
	if aerr != nil {
		return nil, aerr
	}
	if s.expired(t, it) {
		return nil, nil
	}
	return it, nil
}

// PutItem stores an item, returning the previous one (for ReturnValues).
func (s *Store) PutItem(table string, rawItem json.RawMessage, cond *Cond) (old item.Item, err error) {
	it, aerr := item.ItemFromJSON(rawItem)
	if aerr != nil {
		return nil, aerr
	}
	if size := item.Size(it); size > item.MaxItemSize {
		return nil, awshttp.Errf(400, "ValidationException", "item size %d exceeds the %d byte limit", size, item.MaxItemSize)
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		t, err := getTable(tx, table)
		if err != nil {
			return err
		}
		key, aerr := primaryKey(t, it)
		if aerr != nil {
			return aerr
		}
		cur, aerr := s.loadItem(tx, t, key)
		if aerr != nil {
			return aerr
		}
		if aerr := cond.check(cur); aerr != nil {
			return aerr
		}
		old = cur
		return s.writeItem(tx, t, key, cur, it)
	})
	return old, err
}

// GetItem fetches an item by key.
func (s *Store) GetItem(table string, rawKey json.RawMessage) (item.Item, error) {
	var out item.Item
	err := s.db.View(func(tx *bolt.Tx) error {
		t, err := getTable(tx, table)
		if err != nil {
			return err
		}
		key, _, aerr := s.KeyFromWire(t, rawKey)
		if aerr != nil {
			return aerr
		}
		it, aerr := s.loadItem(tx, t, key)
		if aerr != nil {
			return aerr
		}
		out = it
		return nil
	})
	return out, err
}

// DeleteItem removes an item, returning the previous one.
func (s *Store) DeleteItem(table string, rawKey json.RawMessage, cond *Cond, _ string) (old item.Item, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		t, err := getTable(tx, table)
		if err != nil {
			return err
		}
		key, _, aerr := s.KeyFromWire(t, rawKey)
		if aerr != nil {
			return aerr
		}
		cur, aerr := s.loadItem(tx, t, key)
		if aerr != nil {
			return aerr
		}
		if aerr := cond.check(cur); aerr != nil {
			return aerr
		}
		old = cur
		if cur == nil {
			return nil
		}
		return s.writeItem(tx, t, key, cur, nil)
	})
	return old, err
}

// UpdateItem applies an update expression, returning (old, new) items.
func (s *Store) UpdateItem(table string, rawKey json.RawMessage, upd *expr.Update, cond *Cond) (old, new item.Item, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		t, err := getTable(tx, table)
		if err != nil {
			return err
		}
		key, keyAttrs, aerr := s.KeyFromWire(t, rawKey)
		if aerr != nil {
			return aerr
		}
		cur, aerr := s.loadItem(tx, t, key)
		if aerr != nil {
			return aerr
		}
		if aerr := cond.check(cur); aerr != nil {
			return aerr
		}
		old = cur
		base := cur
		if base == nil {
			// Updating a missing item creates it from its key attributes.
			base = item.Item{}
			for k, v := range keyAttrs {
				base[k] = v
			}
		}
		next := base
		if upd != nil {
			next, aerr = upd.Apply(base)
			if aerr != nil {
				return aerr
			}
		}
		// Key attributes are immutable.
		for _, kp := range []*KeyPart{&t.Hash, t.Range} {
			if kp == nil {
				continue
			}
			if !item.Equal(next[kp.Name], base[kp.Name]) {
				return awshttp.Errf(400, "ValidationException", "the update expression may not modify the key attribute %s", kp.Name)
			}
		}
		if size := item.Size(next); size > item.MaxItemSize {
			return awshttp.Errf(400, "ValidationException", "updated item size %d exceeds the %d byte limit", size, item.MaxItemSize)
		}
		new = next
		return s.writeItem(tx, t, key, cur, next)
	})
	return old, new, err
}
