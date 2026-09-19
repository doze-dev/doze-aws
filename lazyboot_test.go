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
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// databases lists the bbolt files under dir, by base name.
func databases(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".bolt") {
			return err
		}
		out = append(out, filepath.Base(p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
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
