package dozeaws_test

// What a stack costs before anything has asked it for anything.
//
// # The claim
//
// doze-aws used to create sixteen bbolt databases at startup. Each creation is
// a file plus a schema stamp, and every writable bbolt transaction commits a
// meta page and fsyncs, so that was three disk flushes per service — about
// 13 ms each, 220 ms in-process and ~790 ms for the binary, on the first run in
// a project. It did that whether the developer used one service or all of them.
//
// internal/lazybolt changed the rule to "a database that does not exist is
// created on first use". So a stack that nobody has spoken to should now leave
// nothing behind but the instance stamp — and the point of this file is that
// the claim is an assertion rather than a sentence in a document.
//
// # Why here and not in the lightness budget
//
// The budget's per-service DataDirBytes catches this too, from the other side:
// with nothing created its ceiling sits near zero, so a service that goes back
// to opening at boot blows a 4 KiB ceiling with a 131,072-byte file. That is a
// good backstop and it is not a statement of intent — it would read as a size
// regression rather than as "laziness was lost". This says the thing directly,
// and it also pins the half a budget cannot see: that first use creates the one
// database the caller asked for, and not the other fifteen.

import (
	"context"
	"io/fs"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
	"github.com/doze-dev/doze-aws/internal/lightness"
)

// written lists the files under dir, by base name, sorted.
func written(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, filepath.Base(p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// databases is the subset of written that is a bbolt file.
func databases(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, name := range written(t, dir) {
		if strings.HasSuffix(name, ".bolt") {
			out = append(out, name)
		}
	}
	return out
}

// eagerFiles is everything a seventeen-service stack is allowed to write before
// anything has asked it for anything, by base name.
//
// A closed list rather than a byte ceiling, because the question it answers is
// "was this deliberate" and a ceiling answers "is this big". Lambda's three
// runtime shims were 19.9 KB and sat comfortably under every budget in the
// tree; what made them worth deferring is that a stack which never invokes a
// function has no use for them, and no ceiling can express that.
//
// Adding a name here is the decision to write something at startup. The two
// keys are here because they are keys: an absent one is not equivalent to an
// empty one the way an absent database is, so generating them on demand would
// be a different change with a different argument.
var eagerFiles = []string{
	// Written as the directory is created, because its whole job is to be
	// there before git looks. Deferring it to first use would mean the one
	// boot that matters — the first, in somebody's repository — is the boot
	// without it. See ignoreSelf in instance.go.
	".gitignore",
	"instance.json",      // the account and region this data belongs to
	"secretsmanager.key", // 32 bytes, generated once per region
	"ssm.key",            // likewise
}

func TestAnUntouchedStackCreatesNoDatabases(t *testing.T) {
	dir := t.TempDir()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if got := databases(t, dir); len(got) != 0 {
		t.Errorf("booting every service created %d database(s): %v\n"+
			"  Each one is a file and two fsyncs nobody asked for, and sixteen of "+
			"them were the whole of cold start.\n"+
			"  A service that must read at startup should use lazybolt's "+
			"ViewIfExists, which answers \"nothing\" for a database that is not "+
			"there instead of creating one to find out.", len(got), got)
	}

	// The wider claim, which the database check alone does not make: nothing
	// ELSE is written either.
	got := written(t, dir)
	if added, removed := lightness.Diff(eagerFiles, got); len(added) > 0 || len(removed) > 0 {
		if len(added) > 0 {
			t.Errorf("booting every service wrote %v, which eagerFiles does not allow.\n"+
				"  Something is being created at startup that nobody has asked for. "+
				"Defer it to first use,\n  or add it to eagerFiles with the reason it "+
				"has to be written before anyone asks.", added)
		}
		if len(removed) > 0 {
			t.Errorf("eagerFiles expects %v, which boot no longer writes.\n"+
				"  If that is the point of the change, delete the entries — a list "+
				"of things that are\n  not written cannot catch anything.", removed)
		}
	}
}

func TestUsingOneServiceCreatesOnlyItsOwnDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("binds a socket and makes a real SDK call")
	}
	dir := t.TempDir()
	st, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: dir, Logf: dozetest.Quiet(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(st.Handler())
	defer ts.Close()

	sqsc := awssqs.NewFromConfig(aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err := sqsc.CreateQueue(context.Background(),
		&awssqs.CreateQueueInput{QueueName: aws.String("lazy")}); err != nil {
		t.Fatal(err)
	}

	got := databases(t, dir)
	if len(got) != 1 || got[0] != "sqs.bolt" {
		t.Errorf("one CreateQueue against a seventeen-service stack created %v, "+
			"want exactly [sqs.bolt].\n"+
			"  The data directory is meant to say which services are in use — a "+
			"developer who touches SQS should pay for SQS.", got)
	}
}
