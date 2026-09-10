package lambda

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
