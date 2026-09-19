package dozeaws_test

// What the thing people download weighs.
//
// # The hole this closes
//
// "One small static binary" is README's second sentence and the whole pitch
// against a 1.88 GB Docker image, and nothing checked it. docs/storage.md
// spends the number — "no more than ~2 MB, against a 20.4 MiB stripped binary"
// is the size criterion the SQLite spike was judged on — so a decision had
// already been made against a figure measured once, by hand, and never again.
// The binary could have doubled with every test in the tree still green.
//
// # Why it could not be gated before
//
// Because it was a local observation. That 20.4 MiB was darwin/arm64; the same
// commit cross-compiled for linux/amd64 is 21.5 MiB. A gate on "the binary"
// would therefore have failed or passed depending on who ran it, and a gate
// like that gets deleted.
//
// So this pins ONE target and builds it the way a release does. The result is
// byte-identical across repeat builds (checked), which means it can be gated
// exactly — no band, no headroom for noise, because there is no noise.
//
// # Why the numbers here are not the numbers in a GitHub release
//
// They are, as of the commit that added -trimpath to .goreleaser.yaml. Before
// that the shipped binary was 57,344 bytes LARGER, because absolute build
// paths were baked into it. If those two ever diverge again this file is
// measuring a build nobody ships, so the flags are recorded in the fixture
// beside the number rather than living only here.

import (
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/internal/lightness"
)

// The canonical build. Changing any of this changes the number, so it is
// recorded in the fixture too — a figure whose build is not written down is a
// figure nobody can reproduce.
const (
	binGOOS    = "linux"
	binGOARCH  = "amd64"
	binVersion = "v0.0.0" // pinned: a real tag's length would move the size
)

var binFlags = []string{"-trimpath", "-buildvcs=false",
	"-ldflags", "-s -w -X main.version=" + binVersion}

func TestTheShippedBinaryFitsItsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles the whole binary")
	}
	want, err := lightness.Load(budgetPath)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "doze-aws")
	args := append([]string{"build"}, binFlags...)
	args = append(args, "-o", path, "./cmd/doze-aws")
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(),
		"GOWORK=off", "CGO_ENABLED=0", "GOOS="+binGOOS, "GOARCH="+binGOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-compiling for %s/%s: %v\n%s", binGOOS, binGOARCH, err, out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	size := info.Size()
	packed := gzipped(t, path)

	target := binGOOS + "/" + binGOARCH
	flags := strings.Join(binFlags, " ")

	if *update {
		want.Portable.Binary = lightness.Binary{
			Target:  target,
			Flags:   flags,
			Bytes:   want.Portable.Binary.Bytes.Record(size, lightness.Bytes),
			Gzipped: want.Portable.Binary.Gzipped.Record(packed, lightness.Bytes),
		}
		if err := lightness.Save(budgetPath, want); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s: %d bytes, %d gzipped", target, size, packed)
		return
	}

	got := want.Portable.Binary
	if got.Bytes.Ceiling == 0 {
		t.Fatal("no binary budget recorded — run `task lightness:update`")
	}
	// The build is part of the measurement. A fixture recorded under different
	// flags is a number for a binary nobody builds, and it would look like a
	// size regression rather than a changed question.
	if got.Target != target || got.Flags != flags {
		t.Errorf("the budget was recorded for a different build:\n"+
			"  recorded: %s  %s\n  measured: %s  %s\n"+
			"  Re-record it with `task lightness:update`, and say in the commit why "+
			"the build changed.",
			got.Target, got.Flags, target, flags)
	}

	t.Logf("%s: %s (%s gzipped), built with %s", target, mib(size), mib(packed), flags)

	if got.Bytes.Over(size) {
		t.Errorf("the binary is %d bytes (%s), over its ceiling of %d (%s).\n"+
			"  \"One small static binary\" is the pitch, and docs/storage.md judges "+
			"dependencies\n  against this number. A new module is the usual cause — "+
			"TestOnlyTheDeclaredModulesAreLinked\n  names it if so, and an embedded "+
			"asset shows up in TestEmbeddedTreesFitTheirBudget.\n"+
			"  If the growth is wanted, `task lightness:update` records it and "+
			"puts it in the diff.",
			size, mib(size), got.Bytes.Ceiling, mib(got.Bytes.Ceiling))
	}
	if got.Gzipped.Over(packed) {
		t.Errorf("the gzipped binary is %d bytes (%s), over its ceiling of %d (%s).\n"+
			"  This is roughly what a download costs.",
			packed, mib(packed), got.Gzipped.Ceiling, mib(got.Gzipped.Ceiling))
	}
}

// gzipped reports what the binary compresses to, using the stdlib so the
// number does not depend on which gzip happens to be installed.
func gzipped(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var counter countingWriter
	zw, err := gzip.NewWriterLevel(&counter, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(zw, f); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return int64(counter)
}

// countingWriter totals what is written without keeping any of it: the
// compressed binary is eight megabytes nobody needs in memory.
type countingWriter int64

func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}
