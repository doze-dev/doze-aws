package lambda

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A _local_ path is whatever the caller typed, and "/" is a plausible typo
// in a console form. treeHash used to walk it to the end of the disk; now it
// gives up at a bound and says why.
func TestTreeHashRefusesAnEnormousTree(t *testing.T) {
	defer func(n int) { maxTreeEntries = n }(maxTreeEntries)
	maxTreeEntries = 8

	dir := t.TempDir()
	for i := range 20 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := treeHash(dir)
	if err == nil {
		t.Fatalf("a tree of 20 files past a bound of 8 should be refused, got %q", sum)
	}
	if sum != "" {
		t.Errorf("a refused walk must not also return a hash, got %q", sum)
	}
	if !strings.Contains(err.Error(), "8") {
		t.Errorf("the error should name the limit it gave up at, got %v", err)
	}
}

// The bound must not change the answer for a tree under it: the fingerprint
// is what tells a publish that code changed, so it has to stay stable.
func TestTreeHashUnderTheBoundIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := treeHash(dir)
	if err != nil {
		t.Fatalf("a small tree must hash: %v", err)
	}
	if first == "" {
		t.Fatal("empty hash")
	}
	again, err := treeHash(dir)
	if err != nil || again != first {
		t.Errorf("treeHash is not stable: %q then %q (%v)", first, again, err)
	}

	// A changed file changes the fingerprint — otherwise the bound could be
	// hiding a hash that ignores its input.
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Error("editing a file left the fingerprint unchanged")
	}
}

// The fingerprint is of CONTENT, not of metadata.
//
// treeHash used to mix in each file's modification time, which made it wrong in
// both directions: touching a file published a new version of identical code,
// and copying or re-cloning a tree changed every mtime, so the same source
// fingerprinted differently on a different machine. AWS's CodeSha256 is a hash
// of the code and is reproducible; callers assume that.
func TestTreeHashIgnoresModificationTime(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("handler.js", "exports.handler = async () => 'hi'")
	write("package.json", `{"name":"fn"}`)

	before, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Touch every file with a time far in the past, changing no byte. This is
	// what a fresh checkout, a copy, or `touch` does.
	past := time.Now().Add(-72 * time.Hour)
	for _, n := range []string{"handler.js", "package.json"} {
		if err := os.Chtimes(filepath.Join(dir, n), past, past); err != nil {
			t.Fatal(err)
		}
	}
	after, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("touching the files changed the fingerprint:\n  before %s\n  after  %s\n"+
			"identical code must fingerprint identically, or every checkout republishes everything",
			before, after)
	}
}

// The other direction: an edit that preserves size must still be noticed. A
// size-and-mtime fingerprint could miss this; a content one cannot.
func TestTreeHashNoticesASameLengthEdit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "handler.js")
	if err := os.WriteFile(p, []byte("const rate = 5"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Same byte count, different meaning — and written back with the SAME
	// modification time, so nothing but the content differs.
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("const rate = 9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Error("a same-length edit left the fingerprint unchanged — the code changed and a publish would not notice")
	}
}

// Moving content between two files changes the tree, even though the bytes
// concatenated are identical. This is why the path and length are mixed in.
func TestTreeHashNoticesContentMovingBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("alpha"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "b.js"), []byte("beta"), 0o644)  //nolint:errcheck
	before, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("alphabeta"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "b.js"), []byte(""), 0o644)          //nolint:errcheck
	after, err := treeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Error("moving content between files left the fingerprint unchanged")
	}
}
