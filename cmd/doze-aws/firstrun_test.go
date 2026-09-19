package main

// From nothing to a working SDK call, timed.
//
// This is the claim the whole project rests on — "one binary, running in under
// a minute" — and until now nothing measured it. Every other test in the tree
// starts a stack in-process, which skips the two things a new user actually
// waits for: linking a binary and booting it from a cold directory.
//
// It is also the only test that reads the block a person sees on success.
// ready_test.go pins that text, but it pins it against a struct the test filled
// in itself, so it cannot catch the field being populated from the wrong place.
// That is not hypothetical: the first version of this printed
//
//	serving   sqs and s3 in , account 000000000000
//
// because it read Identity.Region — a bare field that is empty until defaulted
// — where the rest of the file uses Identity.RegionName(), which defaults. The
// unit test passed the whole time. Booting the real binary is what found it,
// and asserting on the real binary's output is what keeps it found.

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/doze-dev/doze-aws/awsident"
)

// firstCallCeiling is generous on purpose. The number worth watching is the one
// the test logs; this only fails when something is actually broken — a boot
// that hangs, a port that never answers — rather than when a runner is slow.
const firstCallCeiling = 60 * time.Second

func TestTimeToFirstSuccessfulCall(t *testing.T) {
	if testing.Short() {
		t.Skip("links the binary and boots it")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	addr := freePort(t)

	// --listen rather than a .doze name: the name path needs DNS set up on the
	// machine, which is the one part of the first minute this test cannot
	// honestly own.
	start := time.Now()
	cmd := exec.Command(bin, "--listen", addr, "--data-dir", filepath.Join(dir, "data"),
		"--services", "sts,sqs")
	cmd.Dir = dir
	// Captured SEPARATELY, and that separation is load-bearing rather than
	// tidiness. The first version of this test merged them, so "the banner"
	// was really the banner plus every log line — and the check for the region
	// was satisfied by `regions=us-east-1` in the logfmt output while the
	// banner itself said "in , account ...". It passed with the bug present.
	// Splitting the streams is what makes the assertion about the thing it
	// claims to be about.
	var out, logs lockedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	client := awssts.NewFromConfig(aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}, func(o *awssts.Options) { o.BaseEndpoint = aws.String("http://" + addr) })

	// A real signed SDK call, not a liveness probe. A 200 from a health
	// endpoint would prove the process is up and nothing about whether an AWS
	// client can talk to it, which is the only thing a user cares about.
	var identity *awssts.GetCallerIdentityOutput
	deadline := time.Now().Add(firstCallCeiling)
	for {
		var err error
		identity, err = client.GetCallerIdentity(context.Background(), &awssts.GetCallerIdentityInput{})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no successful SDK call within %s.\nThe binary said:\n%s",
				firstCallCeiling, out.String()+logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	elapsed := time.Since(start)

	if got := aws.ToString(identity.Account); got != awsident.AccountID {
		t.Errorf("GetCallerIdentity returned account %q, want %q", got, awsident.AccountID)
	}
	t.Logf("launch to first successful SDK call: %s", elapsed.Round(time.Millisecond))

	// Wait for the banner: it is printed after the listener is bound, so a
	// successful call does not guarantee it has been flushed yet.
	banner := waitFor(t, &out, "doze-aws is up.")

	// The assertions ready_test.go structurally cannot make, because they are
	// about where main.go reads each field FROM.
	for _, want := range []struct{ substr, why string }{
		{"http://" + addr, "the endpoint has to be the one it actually bound"},
		{"/_console/", "the console is on by default and is the thing worth finding"},
		{awsident.Region, "an empty region here is the bug this test exists for"},
		{awsident.AccountID, "ARNs carry the account, so it is worth stating"},
		{`eval "$(doze-aws env)"`, "the one command that points a shell at it"},
		{"doze-aws doctor", "what to run when it does not work"},
	} {
		if !strings.Contains(banner, want.substr) {
			t.Errorf("the startup block is missing %q — %s\nGot:\n%s", want.substr, want.why, banner)
		}
	}

	// The log lines are a separate contract: main.go says msg=listening is
	// parsed by tooling, so the human block must not have displaced it.
	if !strings.Contains(logs.String(), "msg=listening") {
		t.Errorf("msg=listening is gone from the logs; the e2e suite and any "+
			"wrapping tooling parse it:\n%s", logs.String())
	}
}

// waitFor returns the process output once it contains substr.
func waitFor(t *testing.T, out *lockedBuffer, substr string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s := out.String(); strings.Contains(s, substr) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("never printed %q:\n%s", substr, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "doze-aws")
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the binary: %v\n%s", err, out)
	}
	return bin
}

// freePort asks the OS for one and hands it back. There is a race between
// closing and the binary binding, which is why the port is not reused across
// tests and why a failure to bind shows up as the call timing out with the
// binary's own error in the message.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// lockedBuffer collects the child's output from the reader goroutines os/exec
// starts, while the test reads it. A plain bytes.Buffer here is a data race the
// detector finds immediately.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
