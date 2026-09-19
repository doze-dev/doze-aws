package lazybolt_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"
)

func noEnsure(*bolt.DB) error { return nil }

// The claim the package makes, in one test: a database that is not there costs
// nothing until something asks, and a database that IS there opens immediately.
func TestAbsentIsDeferredAndPresentIsNot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bolt")

	db, err := lazybolt.Open(path, nil, noEnsure)
	if err != nil {
		t.Fatal(err)
	}
	if db.Opened() {
		t.Error("an absent database was opened by Open")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open created the file before anything used it: %v", err)
	}

	if err := db.View(func(*bolt.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !db.Opened() {
		t.Error("a use did not open the database")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file was not created by the first use: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := lazybolt.Open(path, nil, noEnsure)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if !again.Opened() {
		t.Error("an existing database was not opened eagerly — errors and " +
			"migrations would have moved to the first request, which is the " +
			"trade this package exists to avoid")
	}
}

// ensure runs once per open, and its failure fails the caller rather than
// leaving a half-prepared database behind.
func TestEnsureRunsOnceAndItsFailureIsTheCallersFailure(t *testing.T) {
	dir := t.TempDir()

	var runs int
	db, err := lazybolt.Open(filepath.Join(dir, "ok.bolt"), nil, func(*bolt.DB) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := db.Update(func(*bolt.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if runs != 1 {
		t.Errorf("ensure ran %d times, want once", runs)
	}

	boom := errors.New("schema is from the future")
	bad, err := lazybolt.Open(filepath.Join(dir, "bad.bolt"), nil, func(*bolt.DB) error {
		return boom
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := bad.Update(func(*bolt.Tx) error { return nil }); !errors.Is(err, boom) {
		t.Errorf("a failing ensure gave %v, want the ensure error", err)
	}
	if bad.Opened() {
		t.Error("a database whose ensure failed is being reported as open")
	}
}

// Without the closed flag a straggler would not fail — it would re-create the
// database after shutdown and leak the handle.
func TestUseAfterCloseFailsInsteadOfReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bolt")
	db, err := lazybolt.Open(path, nil, noEnsure)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
	if err := db.Update(func(*bolt.Tx) error { return nil }); !errors.Is(err, lazybolt.ErrClosed) {
		t.Errorf("Update after Close gave %v, want ErrClosed", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("a use after Close created the database")
	}
}

// bbolt takes an exclusive lock on the file, so a second creator would block
// rather than fail. Only one goroutine may reach bolt.Open.
func TestConcurrentFirstUseOpensOnce(t *testing.T) {
	var opens int
	var mu sync.Mutex
	db, err := lazybolt.Open(filepath.Join(t.TempDir(), "x.bolt"), nil, func(*bolt.DB) error {
		mu.Lock()
		opens++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.Update(func(*bolt.Tx) error { return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if opens != 1 {
		t.Errorf("%d goroutines opened the database %d times, want once", 16, opens)
	}
}

// A path that cannot be statted is not absence, and must not be mistaken for it.
func TestAnUnreadablePathFailsFromOpen(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lazybolt.Open(filepath.Join(file, "x.bolt"), nil, noEnsure); err == nil {
		t.Error("a database under a non-directory was accepted by Open")
	}
}
