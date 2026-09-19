package dozeaws

// The data directory's own record of what it is.
//
// # Why the data carries its identity rather than the configuration
//
// An instance's account and region are properties of what is STORED, not of
// where you happened to run from. ARNs embed the account, and they are written
// into other resources as plain strings — an EventBridge target, a Lambda
// event source mapping, an IAM policy resource. Start against the same data
// with a different account and every one of those references orphans: not at
// startup, but at fire time, silently.
//
// config.go has documented that the account is "effectively frozen" for a
// while. Documented is not enforced. A stamp beside the data makes it
// enforceable, and it works in the one deployment where there is no
// configuration file to consult at all: as a module inside doze, which passes
// a data directory and nothing else (doze-modules/modules/aws/serve.go).
//
// It also means identity survives the things that move data around — a
// project renamed, a directory moved, a volume remounted somewhere with more
// disk. Those are exactly the cases where a cwd-anchored answer is wrong.
//
// # Not a config file
//
// JSON, not TOML, and written by doze-aws rather than by a person. It records
// what the data WAS created with, so there is something to compare against;
// editing it to change the answer would defeat the point. Preferences live in
// doze-aws.toml, which is a different thing in a different place.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

// InstanceFile is the stamp's name inside the data directory. It sits beside
// the per-region folders and _global; NeedsMigration only looks for service
// directories by name, so a file here is invisible to it.
const InstanceFile = "instance.json"

// instanceStamp is what the data records about itself.
//
// The instance NAME is deliberately absent. A name is a DNS and URL concern —
// it decides what hostnames this instance answers on — and nothing in the data
// depends on it. Under doze there is no doze-aws name at all. Region and
// account are the two that the stored bytes actually depend on.
type instanceStamp struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	Created string `json:"created"`
}

// ErrAccountChanged reports that a data directory was created under a
// different account id than the one now configured.
type ErrAccountChanged struct {
	DataDir string
	Stored  string
	Given   string
}

func (e *ErrAccountChanged) Error() string {
	return fmt.Sprintf(
		"doze-aws: %s holds resources created under account %s, but this instance is configured for %s.\n"+
			"  ARNs are stored INSIDE other resources — an EventBridge target, a Lambda event source\n"+
			"  mapping, an IAM policy — so every cross-resource reference would break at fire time\n"+
			"  rather than now.\n"+
			"  --account-id %s   keep this data\n"+
			"  --data-dir <new>      start fresh under %s",
		e.DataDir, e.Stored, e.Given, e.Stored, e.Given)
}

// stampInstance records this instance's identity beside its data, or checks it
// against what is already recorded.
//
// A mismatch is treated differently per field, because the consequences differ:
//
//	account   refused. Stored ARNs embed it, so continuing orphans references
//	          silently, later, somewhere else.
//	region    reported. Data is already per-region folders and several coexist;
//	          changing the DEFAULT only moves where unqualified requests land,
//	          and the old region's resources are still there under their own
//	          folder.
//
// Failing to WRITE is never fatal. A read-only data directory is somebody
// else's problem to diagnose — bbolt will say so far more clearly — and
// refusing to start over a bookkeeping file would be the wrong trade.
func stampInstance(dataDir string, id awsident.Identity, logf func(string, ...any)) error {
	if dataDir == "" {
		return nil // an in-memory stack has no data to describe
	}
	path := filepath.Join(dataDir, InstanceFile)
	want := instanceStamp{
		Account: id.Account(),
		Region:  id.RegionName(),
		Created: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var got instanceStamp
		if jerr := json.Unmarshal(data, &got); jerr != nil {
			// Unreadable is treated as absent rather than fatal: it is our own
			// bookkeeping, and refusing to start over it helps nobody.
			logf("doze-aws: %s is unreadable (%v); rewriting it", path, jerr)
			break
		}
		if got.Account != "" && got.Account != want.Account {
			return &ErrAccountChanged{DataDir: dataDir, Stored: got.Account, Given: want.Account}
		}
		if got.Region != "" && got.Region != want.Region {
			logf("doze-aws: %s was created with default region %s, now running as %s — "+
				"the old region's resources are still under %s/", dataDir, got.Region, want.Region, got.Region)
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		logf("doze-aws: could not read %s: %v", path, err)
		return nil
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		logf("doze-aws: could not create %s: %v", dataDir, err)
		return nil
	}
	blob, _ := json.MarshalIndent(want, "", "  ")
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		logf("doze-aws: could not record this instance's identity in %s: %v", path, err)
	}
	ignoreSelf(dataDir, logf)
	return nil
}

// ignoreSelf drops a .gitignore into a data directory as it is created, so git
// never offers to commit it.
//
// The default data directory is ./data, inside whatever project you ran
// doze-aws in, and after one console load it holds sixteen bbolt files and two
// encryption keys — two megabytes of local state that belongs to nobody's
// repository. Telling people to add it to .gitignore is a documentation fix
// for a problem the tool can just not have: a `*` here ignores the directory's
// contents and the file itself, which is the standard shape.
//
// Written only when the directory is new and only when there is nothing there
// already, so an existing checkout and anyone who has their own arrangement
// are both left alone. A failure is logged and ignored — a data directory that
// works is worth more than one that is tidy.
func ignoreSelf(dataDir string, logf func(string, ...any)) {
	path := filepath.Join(dataDir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return
	}
	const body = "# doze-aws's local state: service databases, keys and blobs.\n" +
		"# Not yours to commit — delete the directory and it rebuilds.\n*\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		logf("doze-aws: could not write %s: %v", path, err)
	}
}
