// Package lazybolt opens a bbolt database when it is first used rather than
// when it is constructed — but only when there is no database there yet.
//
// # The measurement this exists for
//
// A writable bbolt transaction commits a meta page and fsyncs, and on a file
// that does not exist yet every one of those costs milliseconds. Creating a
// database, stamping its schema version and creating its buckets is three
// flushes, about 13 ms on a laptop SSD, and doze-aws did that sixteen times
// before it would answer anything. That is the whole of cold start: ~225 ms
// in-process, ~790 ms for the binary, on the first run in a project.
//
// Opening a database that ALREADY exists costs 27 µs. Sixteen of them cost
// 441 µs. There is nothing there worth deferring.
//
// # So the rule is the file, not the service
//
//	the database exists  -> open it now
//	it does not          -> create it on first use
//
// This is what makes laziness safe rather than a trade. Everything a caller
// would lose by deferring — an unreadable file, a failed flock, a schema
// migration, a permissions problem — is a property of a database that is
// ALREADY THERE, and those still surface from the constructor exactly as they
// did before. A file that does not exist has no schema to migrate and no
// corruption to find. Deferring it defers nothing but the cost.
//
// It also means the data directory answers the question by itself: the files
// that exist are the services in use, so a developer who touches SQS and S3
// opens two databases at startup forever after, not sixteen.
//
// What DOES move is the failure to create a database — a disk that filled
// after startup, say. That is not a regression in practice because
// stampInstance already writes instance.json into the same directory before
// any service is constructed, so an unwritable or missing data directory still
// fails at boot with the message it always had. See instance.go.
//
// # Why a type and not a flag
//
// The method set here is exactly the four methods the sixteen stores use, with
// bbolt's signatures unchanged. That is deliberate: it makes adopting this a
// change to a field's TYPE and to nothing else, so the ~270 call sites that say
// s.db.Update(func(tx *bolt.Tx) error { ... }) keep compiling and keep reading
// the same. A transaction is still a *bolt.Tx and this package does not wrap it.
package lazybolt

import (
	"errors"
	"io/fs"
	"os"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// ErrClosed is returned by a use after Close.
//
// Its own error rather than bolt.ErrDatabaseNotOpen because the reopen it
// prevents is the failure mode specific to this type: without the closed flag,
// a straggler goroutine calling Update after shutdown would not fail, it would
// silently CREATE the database again and leak the handle.
var ErrClosed = errors.New("lazybolt: database is closed")

// DB is a bbolt database that may not be open yet.
type DB struct {
	path   string
	opts   *bolt.Options
	ensure func(*bolt.DB) error

	mu     sync.RWMutex
	db     *bolt.DB
	closed bool
}

// Open prepares a database at path.
//
// If a file is already there it is opened now and ensure runs now, so errors
// and migrations stay at startup. If it is not, both are deferred to the first
// Update, View or Sync.
//
// ensure runs exactly once per successful open, inside Open or inside the first
// use, and a failure there closes the handle and fails the call. It is where
// schema stamping and one-time bucket creation belong.
func Open(path string, opts *bolt.Options, ensure func(*bolt.DB) error) (*DB, error) {
	d := &DB{path: path, opts: opts, ensure: ensure}
	switch _, err := os.Stat(path); {
	case err == nil:
		if _, err := d.handle(); err != nil {
			return nil, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		// Something other than absence — a bad path component, a permission
		// problem on the directory. Report it from the constructor, where it
		// would have come from before.
		return nil, err
	}
	return d, nil
}

// handle returns the open database, opening it if this is the first use.
func (d *DB) handle() (*bolt.DB, error) {
	d.mu.RLock()
	db, closed := d.db, d.closed
	d.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}
	if db != nil {
		return db, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// Re-checked under the write lock: several goroutines can arrive here at
	// once, and only the first may create the file.
	if d.closed {
		return nil, ErrClosed
	}
	if d.db != nil {
		return d.db, nil
	}
	opened, err := bolt.Open(d.path, 0o600, d.opts)
	if err != nil {
		return nil, err
	}
	if d.ensure != nil {
		if err := d.ensure(opened); err != nil {
			opened.Close()
			return nil, err
		}
	}
	d.db = opened
	return opened, nil
}

// Update runs fn in a writable transaction, opening the database first if it
// has not been opened yet.
func (d *DB) Update(fn func(*bolt.Tx) error) error {
	db, err := d.handle()
	if err != nil {
		return err
	}
	return db.Update(fn)
}

// View runs fn in a read-only transaction.
//
// A read opens the database too, and therefore creates it. That is not
// avoidable — bbolt cannot read a file that is not there — and it is why the
// console's fan-out across every service materialises them all. The cost is the
// same one that used to be paid at startup, moved to the moment something
// actually asked.
func (d *DB) View(fn func(*bolt.Tx) error) error {
	db, err := d.handle()
	if err != nil {
		return err
	}
	return db.View(fn)
}

// ViewIfExists runs fn only if the database is already open, and reports
// whether it ran. It never creates one.
//
// This is for the reads a service does at startup to pick up where a previous
// run left off — Lambda's enabled event source mappings, Step Functions'
// RUNNING executions. A database that does not exist cannot hold a previous
// run, so the honest answer is "nothing", and taking it through View would
// create all sixteen files at boot for no result: exactly what this package
// exists to stop.
//
// It is deliberately not what ordinary reads use. fn does not run when the
// database is absent, so a caller that fills a slice inside fn gets a nil one
// rather than an empty one — a difference that reaches the wire as null
// instead of []. That is fine where the caller is looping over work to resume
// and wrong as a blanket rule, so ordinary View still opens.
func (d *DB) ViewIfExists(fn func(*bolt.Tx) error) (bool, error) {
	d.mu.RLock()
	db, closed := d.db, d.closed
	d.mu.RUnlock()
	if closed {
		return false, ErrClosed
	}
	if db == nil {
		return false, nil
	}
	return true, db.View(fn)
}

// Sync flushes the file, for the stores opened with NoSync.
func (d *DB) Sync() error {
	db, err := d.handle()
	if err != nil {
		return err
	}
	return db.Sync()
}

// Close closes the database if it was ever opened, and makes every later use
// fail rather than reopen. Closing twice is not an error, because several
// services call it from both a stop path and an error path.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	db := d.db
	d.db = nil
	if db == nil {
		return nil
	}
	return db.Close()
}

// Path is where the database is, or will be.
func (d *DB) Path() string { return d.path }

// Opened reports whether the database has been created or opened yet. For
// tests and for the budget: it is the difference this package exists to make.
func (d *DB) Opened() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.db != nil
}
