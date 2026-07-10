package store

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "ddb.bolt"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestStoreItemLifecycle(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateTable(Table{Name: "t", Hash: KeyPart{Name: "pk", Type: "S"}}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Duplicate CreateTable is an error.
	if _, err := s.CreateTable(Table{Name: "t", Hash: KeyPart{Name: "pk", Type: "S"}}); err == nil {
		t.Fatal("duplicate CreateTable should error")
	}

	// Put then Get.
	if _, err := s.PutItem("t", []byte(`{"pk":{"S":"a"},"n":{"N":"1"}}`), nil); err != nil {
		t.Fatalf("PutItem: %v", err)
	}
	got, err := s.GetItem("t", []byte(`{"pk":{"S":"a"}}`))
	if err != nil || got == nil {
		t.Fatalf("GetItem = %v err=%v", got, err)
	}
	if _, ok := got["n"]; !ok {
		t.Fatalf("stored item missing attribute: %+v", got)
	}

	// Missing key returns nil.
	miss, err := s.GetItem("t", []byte(`{"pk":{"S":"nope"}}`))
	if err != nil || miss != nil {
		t.Fatalf("GetItem(missing) = %v err=%v", miss, err)
	}

	if n := s.CountItems("t"); n != 1 {
		t.Fatalf("CountItems = %d, want 1", n)
	}

	// Delete then confirm gone.
	if _, err := s.DeleteItem("t", []byte(`{"pk":{"S":"a"}}`), nil, ""); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if n := s.CountItems("t"); n != 0 {
		t.Fatalf("CountItems after delete = %d, want 0", n)
	}
}

func TestStoreTableLifecycle(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"a", "b"} {
		if _, err := s.CreateTable(Table{Name: name, Hash: KeyPart{Name: "pk", Type: "S"}}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := s.ListTables()
	if err != nil || len(names) != 2 {
		t.Fatalf("ListTables = %v err=%v", names, err)
	}
	if _, err := s.GetTable("a"); err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if _, err := s.GetTable("missing"); err == nil {
		t.Fatal("GetTable(missing) should error")
	}
	if _, err := s.DeleteTable("a"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	names, _ = s.ListTables()
	if len(names) != 1 || names[0] != "b" {
		t.Fatalf("after delete ListTables = %v", names)
	}
}
