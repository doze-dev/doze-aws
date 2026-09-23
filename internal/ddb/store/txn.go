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
// txPlanned is one operation's decided outcome, worked out in phase 1 and
// applied in phase 2. next == nil is a delete; skip means a condition check,
// which writes nothing.
type txPlanned struct {
	t    *Table
	key  []byte
	old  item.Item
	next item.Item
	skip bool
}

// TransactWrite applies every operation or none, in three phases: replay a
// recorded outcome if this token has been seen, decide what each operation
// would do against current state, then write.
//
// The phases are separate because phase 1 must not write. Every condition is
// evaluated against the state as it was before the transaction, so a plan
// built while applying would see its own earlier writes and a conditional
// update could pass against a value only this transaction had put there.
func (s *Store) TransactWrite(ops []TxWriteOp, token string, requestHash string) error {
	if len(ops) == 0 || len(ops) > 100 {
		return awshttp.Errf(400, "ValidationException", "TransactWriteItems accepts 1-100 operations, got %d", len(ops))
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		replayed, err := s.txReplay(tx, token, requestHash)
		if err != nil || replayed {
			return err
		}

		plan, reasons, canceled, err := s.txPlan(tx, ops)
		if err != nil {
			return err
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
		return s.txRecord(tx, token, requestHash, true)
	})
}

// txReplay answers a repeated ClientRequestToken from the record the first
// attempt left, so a retried transaction does not apply twice. replayed means
// the caller is done: the outcome has already been decided.
func (s *Store) txReplay(tx *bolt.Tx, token, requestHash string) (replayed bool, err error) {
	if token == "" {
		return false, nil
	}
	b, err := tx.CreateBucketIfNotExists(txBucket)
	if err != nil {
		return false, err
	}
	raw := b.Get([]byte(token))
	if raw == nil {
		return false, nil
	}
	var rec txRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Expiry <= s.now().Unix() {
		return false, nil
	}
	if rec.Hash != requestHash {
		return false, awshttp.Errf(400, "IdempotentParameterMismatchException",
			"ClientRequestToken was reused with different parameters")
	}
	if rec.OK {
		return true, nil // replay of a committed transaction
	}
	// A failed transaction replays its failure generically.
	return false, awshttp.Errf(400, "TransactionCanceledException", "transaction was previously canceled")
}

// txRecord remembers the outcome under the request's token, for txReplay.
func (s *Store) txRecord(tx *bolt.Tx, token, requestHash string, ok bool) error {
	if token == "" {
		return nil
	}
	b := tx.Bucket(txBucket)
	raw, _ := json.Marshal(txRecord{Hash: requestHash, Expiry: s.now().Unix() + txTokenTTL, OK: ok})
	return b.Put([]byte(token), raw)
}

// txPlan decides every operation against current state, writing nothing.
//
// canceled reports that at least one operation failed its own condition or
// validation, which cancels the whole transaction with a reason per item. A
// returned error is different: it aborts the request outright, which is what
// DynamoDB does for a malformed transaction rather than a rejected one.
func (s *Store) txPlan(tx *bolt.Tx, ops []TxWriteOp) (plan []txPlanned, reasons []CancellationReason, canceled bool, err error) {
	plan = make([]txPlanned, len(ops))
	reasons = make([]CancellationReason, len(ops))
	// A transaction may not touch the same item twice — real DynamoDB
	// rejects this outright with a ValidationException (not a per-item
	// cancellation), because sequential apply with phase-1 "old" snapshots
	// would corrupt index maintenance.
	seen := map[string]bool{}
	for i, op := range ops {
		reasons[i] = CancellationReason{Code: "None"}
		t, terr := s.getTable(tx, op.Table)
		if terr != nil {
			reasons[i] = CancellationReason{Code: "ResourceNotFound", Message: op.Table}
			canceled = true
			continue
		}

		var (
			p      txPlanned
			key    []byte
			reason *CancellationReason
			perr   error
		)
		switch {
		case op.Put != nil:
			p, key, reason, perr = s.planPut(tx, t, op)
		case op.UpdateKey != nil:
			p, key, reason, perr = s.planUpdate(tx, t, op)
		case op.DeleteKey != nil:
			p, key, reason, perr = s.planDelete(tx, t, op)
		case op.CheckKey != nil:
			p, key, reason, perr = s.planCheck(tx, t, op)
		default:
			reasons[i] = CancellationReason{Code: "ValidationError", Message: "empty transact item"}
			canceled = true
			continue
		}
		if perr != nil {
			return nil, nil, false, perr
		}
		if reason != nil {
			reasons[i] = *reason
			canceled = true
			continue
		}
		plan[i] = p

		// Structural validation: two operations on the same item abort the
		// whole request (a hard ValidationException, not a cancellation).
		if len(key) > 0 {
			dk := op.Table + "\x00" + string(key)
			if seen[dk] {
				return nil, nil, false, awshttp.Errf(400, "ValidationException",
					"Transaction request cannot include multiple operations on one item")
			}
			seen[dk] = true
		}
	}
	return plan, reasons, canceled, nil
}

// planPut decides a Put: the item must parse, name a whole key, fit 400 KB,
// and satisfy any condition on the value already there.
func (s *Store) planPut(tx *bolt.Tx, t *Table, op TxWriteOp) (txPlanned, []byte, *CancellationReason, error) {
	it, perr := item.ItemFromJSON(op.Put)
	if perr != nil {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: perr.Message}, nil
	}
	key, aerr := primaryKey(t, it)
	if aerr != nil {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: aerr.Message}, nil
	}
	if size := item.Size(it); size > item.MaxItemSize {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: "item size exceeds the 400 KB limit"}, nil
	}
	cur, lerr := s.loadItem(tx, t, key)
	if lerr != nil {
		return txPlanned{}, nil, nil, lerr
	}
	if cerr := op.Cond.check(cur); cerr != nil {
		r := conditionReason(cerr, cur, op.ReturnOld)
		return txPlanned{}, nil, &r, nil
	}
	return txPlanned{t: t, key: key, old: cur, next: it}, key, nil, nil
}

// planUpdate decides an update expression. The key attributes are immutable
// and the result must fit 400 KB — the same guards the non-transactional
// UpdateItem enforces.
func (s *Store) planUpdate(tx *bolt.Tx, t *Table, op TxWriteOp) (txPlanned, []byte, *CancellationReason, error) {
	key, keyAttrs, aerr := s.KeyFromWire(t, op.UpdateKey)
	if aerr != nil {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: aerr.Message}, nil
	}
	cur, lerr := s.loadItem(tx, t, key)
	if lerr != nil {
		return txPlanned{}, nil, nil, lerr
	}
	if cerr := op.Cond.check(cur); cerr != nil {
		r := conditionReason(cerr, cur, op.ReturnOld)
		return txPlanned{}, nil, &r, nil
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
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: uerr.Message}, nil
	}
	for _, kp := range []*KeyPart{&t.Hash, t.Range} {
		if kp == nil {
			continue
		}
		if !item.Equal(next[kp.Name], base[kp.Name]) {
			return txPlanned{}, nil, &CancellationReason{Code: "ValidationError",
				Message: "the update expression may not modify the key attribute " + kp.Name}, nil
		}
	}
	if size := item.Size(next); size > item.MaxItemSize {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: "updated item size exceeds the 400 KB limit"}, nil
	}
	return txPlanned{t: t, key: key, old: cur, next: next}, key, nil, nil
}

// planDelete decides a Delete: next stays nil, which is what phase 2 reads as
// a removal.
func (s *Store) planDelete(tx *bolt.Tx, t *Table, op TxWriteOp) (txPlanned, []byte, *CancellationReason, error) {
	key, _, aerr := s.KeyFromWire(t, op.DeleteKey)
	if aerr != nil {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: aerr.Message}, nil
	}
	cur, lerr := s.loadItem(tx, t, key)
	if lerr != nil {
		return txPlanned{}, nil, nil, lerr
	}
	if cerr := op.Cond.check(cur); cerr != nil {
		r := conditionReason(cerr, cur, op.ReturnOld)
		return txPlanned{}, nil, &r, nil
	}
	return txPlanned{t: t, key: key, old: cur, next: nil}, key, nil, nil
}

// planCheck decides a ConditionCheck, which writes nothing but still counts
// as touching the item — two operations on one key is refused whether or not
// either of them writes.
func (s *Store) planCheck(tx *bolt.Tx, t *Table, op TxWriteOp) (txPlanned, []byte, *CancellationReason, error) {
	key, _, aerr := s.KeyFromWire(t, op.CheckKey)
	if aerr != nil {
		return txPlanned{}, nil, &CancellationReason{Code: "ValidationError", Message: aerr.Message}, nil
	}
	cur, lerr := s.loadItem(tx, t, key)
	if lerr != nil {
		return txPlanned{}, nil, nil, lerr
	}
	if cerr := op.Cond.check(cur); cerr != nil {
		r := conditionReason(cerr, cur, op.ReturnOld)
		return txPlanned{}, nil, &r, nil
	}
	return txPlanned{skip: true}, key, nil, nil
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
			t, err := s.getTable(tx, g.Table)
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
