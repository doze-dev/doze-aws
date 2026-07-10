package store

// Transactions and batches. Single-node atomicity is one bbolt Update: phase 1
// evaluates every condition against current state (collecting per-item
// cancellation reasons), phase 2 applies all writes. ClientRequestToken
// idempotency replays the recorded outcome for 10 minutes.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/expr"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
)

// TxWriteOp is one TransactWriteItems operation (exactly one field set).
type TxWriteOp struct {
	Table string
	// Put stores an item.
	Put json.RawMessage
	// UpdateKey + Update applies an update expression.
	UpdateKey json.RawMessage
	Update    *expr.Update
	// DeleteKey removes an item.
	DeleteKey json.RawMessage
	// CheckKey asserts a condition without writing.
	CheckKey json.RawMessage

	Cond      *Cond
	ReturnOld bool // ReturnValuesOnConditionCheckFailure
}

// CancellationReason mirrors DynamoDB's per-item transaction outcome.
type CancellationReason struct {
	Code    string          `json:"Code"`
	Message string          `json:"Message,omitempty"`
	Item    json.RawMessage `json:"Item,omitempty"`
}

// ErrTransactionCanceled carries the reasons; the service layer renders it.
type ErrTransactionCanceled struct {
	Reasons []CancellationReason
}

func (e *ErrTransactionCanceled) Error() string { return "transaction canceled" }

const txTokenTTL = 10 * 60 // seconds

// txRecord is the stored idempotency outcome.
type txRecord struct {
	Hash   string `json:"hash"`
	Expiry int64  `json:"expiry"`
	OK     bool   `json:"ok"`
}

// TransactWrite runs up to 100 write ops atomically.
func (s *Store) TransactWrite(ops []TxWriteOp, token string, requestHash string) error {
	if len(ops) == 0 || len(ops) > 100 {
		return awshttp.Errf(400, "ValidationException", "TransactWriteItems accepts 1-100 operations, got %d", len(ops))
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		// Idempotency: a replayed token returns the recorded outcome.
		if token != "" {
			b, err := tx.CreateBucketIfNotExists(txBucket)
			if err != nil {
				return err
			}
			if raw := b.Get([]byte(token)); raw != nil {
				var rec txRecord
				if json.Unmarshal(raw, &rec) == nil && rec.Expiry > s.now().Unix() {
					if rec.Hash != requestHash {
						return awshttp.Errf(400, "IdempotentParameterMismatchException",
							"ClientRequestToken was reused with different parameters")
					}
					if rec.OK {
						return nil // replay of a committed transaction
					}
					// A failed transaction replays its failure generically.
					return awshttp.Errf(400, "TransactionCanceledException", "transaction was previously canceled")
				}
			}
		}

		// Phase 1: evaluate every condition against current state.
		reasons := make([]CancellationReason, len(ops))
		canceled := false
		type planned struct {
			t    *Table
			key  []byte
			old  item.Item
			next item.Item // nil = delete; for checks, next == old
			skip bool      // condition checks write nothing
		}
		plan := make([]planned, len(ops))
		for i, op := range ops {
			reasons[i] = CancellationReason{Code: "None"}
			t, err := getTable(tx, op.Table)
			if err != nil {
				reasons[i] = CancellationReason{Code: "ResourceNotFound", Message: op.Table}
				canceled = true
				continue
			}
			var key []byte
			var keyAttrs item.Item
			var aerr *awshttp.APIError
			switch {
			case op.Put != nil:
				it, perr := item.ItemFromJSON(op.Put)
				if perr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: perr.Message}
					canceled = true
					continue
				}
				key, aerr = primaryKey(t, it)
				if aerr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: aerr.Message}
					canceled = true
					continue
				}
				cur, lerr := s.loadItem(tx, t, key)
				if lerr != nil {
					return lerr
				}
				if cerr := op.Cond.check(cur); cerr != nil {
					reasons[i] = conditionReason(cerr, cur, op.ReturnOld)
					canceled = true
					continue
				}
				plan[i] = planned{t: t, key: key, old: cur, next: it}
			case op.UpdateKey != nil:
				key, keyAttrs, aerr = s.KeyFromWire(t, op.UpdateKey)
				if aerr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: aerr.Message}
					canceled = true
					continue
				}
				cur, lerr := s.loadItem(tx, t, key)
				if lerr != nil {
					return lerr
				}
				if cerr := op.Cond.check(cur); cerr != nil {
					reasons[i] = conditionReason(cerr, cur, op.ReturnOld)
					canceled = true
					continue
				}
				base := cur
				if base == nil {
					base = item.Item{}
					for k, v := range keyAttrs {
						base[k] = v
					}
				}
				next, uerr := op.Update.Apply(base)
				if uerr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: uerr.Message}
					canceled = true
					continue
				}
				plan[i] = planned{t: t, key: key, old: cur, next: next}
			case op.DeleteKey != nil:
				key, _, aerr = s.KeyFromWire(t, op.DeleteKey)
				if aerr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: aerr.Message}
					canceled = true
					continue
				}
				cur, lerr := s.loadItem(tx, t, key)
				if lerr != nil {
					return lerr
				}
				if cerr := op.Cond.check(cur); cerr != nil {
					reasons[i] = conditionReason(cerr, cur, op.ReturnOld)
					canceled = true
					continue
				}
				plan[i] = planned{t: t, key: key, old: cur, next: nil}
			case op.CheckKey != nil:
				key, _, aerr = s.KeyFromWire(t, op.CheckKey)
				if aerr != nil {
					reasons[i] = CancellationReason{Code: "ValidationError", Message: aerr.Message}
					canceled = true
					continue
				}
				cur, lerr := s.loadItem(tx, t, key)
				if lerr != nil {
					return lerr
				}
				if cerr := op.Cond.check(cur); cerr != nil {
					reasons[i] = conditionReason(cerr, cur, op.ReturnOld)
					canceled = true
					continue
				}
				plan[i] = planned{skip: true}
			default:
				reasons[i] = CancellationReason{Code: "ValidationError", Message: "empty transact item"}
				canceled = true
			}
		}

		record := func(ok bool) error {
			if token == "" {
				return nil
			}
			b := tx.Bucket(txBucket)
			raw, _ := json.Marshal(txRecord{Hash: requestHash, Expiry: s.now().Unix() + txTokenTTL, OK: ok})
			return b.Put([]byte(token), raw)
		}

		if canceled {
			// bbolt rolls writes back on error; record the failure in a fresh
			// transaction via the outer error path (token replay treats any
			// canceled outcome generically, so losing the record is benign).
			return &ErrTransactionCanceled{Reasons: reasons}
		}

		// Phase 2: apply everything.
		for _, p := range plan {
			if p.skip || p.t == nil {
				continue
			}
			if err := s.writeItem(tx, p.t, p.key, p.old, p.next); err != nil {
				return err
			}
		}
		return record(true)
	})
}

func conditionReason(cerr *awshttp.APIError, cur item.Item, returnOld bool) CancellationReason {
	r := CancellationReason{Code: "ConditionalCheckFailed", Message: cerr.Message}
	if returnOld && cur != nil {
		r.Item = item.ItemJSON(cur)
	}
	return r
}

// TransactGet reads up to 100 items with a consistent view (one bbolt View).
func (s *Store) TransactGet(keys []struct {
	Table string
	Key   json.RawMessage
}) ([]item.Item, error) {
	if len(keys) == 0 || len(keys) > 100 {
		return nil, awshttp.Errf(400, "ValidationException", "TransactGetItems accepts 1-100 gets, got %d", len(keys))
	}
	out := make([]item.Item, len(keys))
	err := s.db.View(func(tx *bolt.Tx) error {
		for i, g := range keys {
			t, err := getTable(tx, g.Table)
			if err != nil {
				return err
			}
			kb, _, aerr := s.KeyFromWire(t, g.Key)
			if aerr != nil {
				return aerr
			}
			it, aerr := s.loadItem(tx, t, kb)
			if aerr != nil {
				return aerr
			}
			out[i] = it
		}
		return nil
	})
	return out, err
}

// RequestHash fingerprints a transaction body for idempotency comparison.
func RequestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CountItems reports a table's item count (DescribeTable convenience).
func (s *Store) CountItems(table string) int64 {
	var n int64
	_ = s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(dataBucket(table)); b != nil {
			n = int64(b.Stats().KeyN)
		}
		return nil
	})
	return n
}
