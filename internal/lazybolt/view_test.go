package lazybolt_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/doze-dev/doze-aws/internal/lazybolt"
	bolt "go.etcd.io/bbolt"
)

// View is Update's twin: it opens the database on first use, and a failure to
// open is the caller's failure, with the transaction never run. (The mutation
// check found nothing asserting it for View, only for Update.)
func TestViewReportsAFailedOpenAndRunsNothing(t *testing.T) {
	boom := errors.New("schema is from the future")
	db, err := lazybolt.Open(filepath.Join(t.TempDir(), "x.bolt"), nil, func(*bolt.DB) error { return boom })
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	err = db.View(func(*bolt.Tx) error { ran = true; return nil })
	if !errors.Is(err, boom) {
		t.Errorf("View gave %v, want the ensure failure", err)
	}
	if ran {
		t.Error("the transaction ran against a database that failed to open")
	}
	// And after Close it is ErrClosed, not a reopen.
	closed, err := lazybolt.Open(filepath.Join(t.TempDir(), "y.bolt"), nil, noEnsure)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if err := closed.View(func(*bolt.Tx) error { return nil }); !errors.Is(err, lazybolt.ErrClosed) {
		t.Errorf("View after Close gave %v, want ErrClosed", err)
	}
}
