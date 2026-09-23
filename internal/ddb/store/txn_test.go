package store

// What TransactWriteItems does, pinned before it was refactored.
//
// The package had two tests and neither touched transactions: the coverage came
// from dynamodb/sdk_test.go three levels up, which exercises the happy path,
// the duplicate-item rejection and the key-mutation guard. Everything else in
// here — token replay, each operation kind's condition failure, the size
// limits, the arity check — was reachable only through code nobody was
// checking directly.
//
// These are characterisation tests: they record what the store does today so a
// restructuring of TransactWrite has something to be measured against. Where a
// case encodes a DynamoDB rule rather than an implementation detail, the
// comment says which.

import (
	"encoding/json"
	"strings"
	"testing"
)

func txStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if _, err := s.CreateTable(Table{Name: "t", Hash: KeyPart{Name: "pk", Type: "S"}}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return s
}

func put(table, pk string, extra string) TxWriteOp {
	body := `{"pk":{"S":"` + pk + `"}` + extra + `}`
	return TxWriteOp{Table: table, Put: json.RawMessage(body)}
}

// reasons pulls the per-item outcomes out of a cancellation, or fails.
func reasons(t *testing.T, err error) []CancellationReason {
	t.Helper()
	var c *ErrTransactionCanceled
	if err == nil {
		t.Fatal("want a cancellation, got nil")
	}
	if !asCanceled(err, &c) {
		t.Fatalf("want *ErrTransactionCanceled, got %T: %v", err, err)
	}
	return c.Reasons
}

func asCanceled(err error, out **ErrTransactionCanceled) bool {
	c, ok := err.(*ErrTransactionCanceled)
	if ok {
		*out = c
	}
	return ok
}

// TransactWriteItems takes 1 to 100 operations. Zero and 101 are refused
// before anything is read, which is DynamoDB's own arity rule.
func TestTransactWriteArity(t *testing.T) {
	s := txStore(t)
	if err := s.TransactWrite(nil, "", ""); err == nil {
		t.Error("zero operations should be refused")
	}
	many := make([]TxWriteOp, 101)
	for i := range many {
		many[i] = put("t", string(rune('a'+i%26))+string(rune('a'+i/26)), "")
	}
	if err := s.TransactWrite(many, "", ""); err == nil {
		t.Error("101 operations should be refused")
	}
}

// Every operation kind commits together, and a check writes nothing.
func TestTransactWriteAppliesEveryKind(t *testing.T) {
	s := txStore(t)
	if _, err := s.PutItem("t", []byte(`{"pk":{"S":"gone"}}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutItem("t", []byte(`{"pk":{"S":"checked"}}`), nil); err != nil {
		t.Fatal(err)
	}
	ops := []TxWriteOp{
		put("t", "made", `,"n":{"N":"1"}`),
		{Table: "t", DeleteKey: json.RawMessage(`{"pk":{"S":"gone"}}`)},
		{Table: "t", CheckKey: json.RawMessage(`{"pk":{"S":"checked"}}`)},
	}
	if err := s.TransactWrite(ops, "", ""); err != nil {
		t.Fatalf("TransactWrite: %v", err)
	}
	if got, _ := s.GetItem("t", []byte(`{"pk":{"S":"made"}}`)); got == nil {
		t.Error("the Put did not commit")
	}
	if got, _ := s.GetItem("t", []byte(`{"pk":{"S":"gone"}}`)); got != nil {
		t.Error("the Delete did not commit")
	}
	if got, _ := s.GetItem("t", []byte(`{"pk":{"S":"checked"}}`)); got == nil {
		t.Error("a CheckKey must not remove the item it checks")
	}
}

// A missing table cancels that item rather than failing the request, and the
// whole transaction rolls back.
func TestTransactWriteUnknownTableCancels(t *testing.T) {
	s := txStore(t)
	err := s.TransactWrite([]TxWriteOp{put("t", "a", ""), put("nope", "b", "")}, "", "")
	rs := reasons(t, err)
	if len(rs) != 2 || rs[0].Code != "None" || rs[1].Code != "ResourceNotFound" {
		t.Fatalf("reasons = %+v", rs)
	}
	if got, _ := s.GetItem("t", []byte(`{"pk":{"S":"a"}}`)); got != nil {
		t.Error("a canceled transaction must write nothing")
	}
}

// Two operations on one item is a hard ValidationException, not a per-item
// cancellation: sequential apply against phase-1 snapshots would corrupt
// index maintenance.
func TestTransactWriteRejectsTheSameItemTwice(t *testing.T) {
	s := txStore(t)
	err := s.TransactWrite([]TxWriteOp{put("t", "dup", ""), put("t", "dup", "")}, "", "")
	if err == nil {
		t.Fatal("want a refusal")
	}
	var c *ErrTransactionCanceled
	if asCanceled(err, &c) {
		t.Fatalf("want a ValidationException, got a cancellation: %+v", c.Reasons)
	}
	if !strings.Contains(err.Error(), "multiple operations on one item") {
		t.Errorf("error = %q", err)
	}
}

// An empty transact item names no operation at all.
func TestTransactWriteRejectsAnEmptyItem(t *testing.T) {
	s := txStore(t)
	rs := reasons(t, s.TransactWrite([]TxWriteOp{{Table: "t"}}, "", ""))
	if len(rs) != 1 || rs[0].Code != "ValidationError" {
		t.Fatalf("reasons = %+v", rs)
	}
}

// A replayed ClientRequestToken returns the recorded outcome rather than
// applying the writes a second time, and the same token with different
// parameters is a mismatch.
func TestTransactWriteTokenReplay(t *testing.T) {
	s := txStore(t)
	ops := []TxWriteOp{put("t", "once", `,"n":{"N":"1"}`)}
	if err := s.TransactWrite(ops, "tok", "hash-a"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Replay with the same hash is a no-op that succeeds, even though the Put
	// would now be a second write of the same key.
	if err := s.TransactWrite(ops, "tok", "hash-a"); err != nil {
		t.Errorf("replay should succeed: %v", err)
	}
	err := s.TransactWrite(ops, "tok", "hash-b")
	if err == nil || !strings.Contains(err.Error(), "IdempotentParameterMismatch") {
		t.Errorf("a reused token with new parameters should mismatch, got %v", err)
	}
}

// An oversized item is refused per-item, before anything is written.
func TestTransactWriteRefusesAnOversizedItem(t *testing.T) {
	s := txStore(t)
	rs := reasons(t, s.TransactWrite([]TxWriteOp{
		put("t", "big", `,"blob":{"S":"`+strings.Repeat("x", 410*1024)+`"}`),
	}, "", ""))
	if len(rs) != 1 || rs[0].Code != "ValidationError" || !strings.Contains(rs[0].Message, "400 KB") {
		t.Fatalf("reasons = %+v", rs)
	}
}
