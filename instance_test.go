package dozeaws

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

func quiet(string, ...any) {}

// The account id is documented as "effectively frozen" because ARNs embed it
// and are stored INSIDE other resources — an EventBridge target, a Lambda
// event source mapping, an IAM policy resource. Documented is not enforced:
// starting against the same data with a different account orphaned every one
// of those references silently, at fire time.
//
// The stamp is what makes it enforceable, and it works in the deployment where
// there is no config file to consult at all — as a module inside doze, which
// passes a data directory and nothing else.
func TestAChangedAccountIsRefused(t *testing.T) {
	dir := t.TempDir()
	first := awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"}

	if err := stampInstance(dir, first, quiet); err != nil {
		t.Fatalf("first run must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, InstanceFile)); err != nil {
		t.Fatalf("no stamp written: %v", err)
	}
	// The same identity again is the ordinary restart.
	if err := stampInstance(dir, first, quiet); err != nil {
		t.Fatalf("restarting with the same identity must succeed: %v", err)
	}

	other := awsident.Identity{Region: "ap-south-1", AccountID: "000000000000"}
	err := stampInstance(dir, other, quiet)
	if err == nil {
		t.Fatal("a different account against existing data must be refused")
	}
	var changed *ErrAccountChanged
	if !errors.As(err, &changed) {
		t.Fatalf("want ErrAccountChanged, got %T: %v", err, err)
	}
	// The message has to carry both ways forward, or it is a complaint.
	for _, want := range []string{"811690671382", "000000000000", "--account-id", "--data-dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is missing %q:\n%v", want, err)
		}
	}
}

// A changed default REGION is not the same problem and must not be refused.
// Data is already per-region folders and several coexist; changing the default
// only moves where unqualified requests land, and the old region's resources
// are still there under their own folder.
func TestAChangedRegionIsReportedNotRefused(t *testing.T) {
	dir := t.TempDir()
	if err := stampInstance(dir, awsident.Identity{Region: "ap-south-1", AccountID: "811690671382"}, quiet); err != nil {
		t.Fatal(err)
	}

	var said []string
	logf := func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }
	if err := stampInstance(dir, awsident.Identity{Region: "eu-west-1", AccountID: "811690671382"}, logf); err != nil {
		t.Fatalf("a changed default region must not be fatal: %v", err)
	}
	joined := strings.Join(said, "\n")
	if !strings.Contains(joined, "ap-south-1") || !strings.Contains(joined, "eu-west-1") {
		t.Errorf("the region change was not reported:\n%s", joined)
	}
}

// Failing to write is never fatal. A data directory nobody can write to is a
// problem bbolt will describe far better, and refusing to start over a
// bookkeeping file would be the wrong trade.
func TestAnUnwritableDataDirIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) }) //nolint:errcheck

	var said []string
	logf := func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }
	if err := stampInstance(dir, awsident.Identity{}, logf); err != nil {
		t.Fatalf("a read-only data dir must not stop startup: %v", err)
	}
	if len(said) == 0 {
		t.Error("failing to record the identity should at least say so")
	}
}

// An in-memory stack has no data to describe.
func TestNoDataDirIsNoStamp(t *testing.T) {
	if err := stampInstance("", awsident.Identity{}, quiet); err != nil {
		t.Fatalf("an empty data dir must be a no-op: %v", err)
	}
}
