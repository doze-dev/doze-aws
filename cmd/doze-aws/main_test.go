package main_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/doze-dev/doze-aws/awsident"
)

// TestBinaryEndToEnd builds the real doze-aws binary, runs it as a separate
// process, drives it with a real SDK client, and shuts it down with SIGTERM —
// the full production path including flag parsing and signal handling.
func TestBinaryEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-binary E2E in -short mode")
	}

	bin := filepath.Join(t.TempDir(), "doze-aws")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	dataDir := t.TempDir()
	cmd := exec.Command(bin, "--listen", "127.0.0.1:0", "--data-dir", dataDir)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	// The binary logs `msg=listening addr=127.0.0.1:PORT ...` once bound.
	addrRe := regexp.MustCompile(`msg=listening addr=([0-9.:]+)`)
	addrCh := make(chan string, 1)
	var logTail strings.Builder
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			logTail.WriteString(line)
			logTail.WriteByte('\n')
			if m := addrRe.FindStringSubmatch(line); m != nil {
				select {
				case addrCh <- m[1]:
				default:
				}
			}
		}
		// sc.Err() is deliberately unchecked: the pipe closing on process exit
		// is the normal way this loop ends.
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(10 * time.Second):
		t.Fatalf("binary never logged its listen address; log so far:\n%s", logTail.String())
	}

	client := awssts.New(awssts.Options{
		Region:       awsident.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(awsident.AccessKeyID, awsident.SecretAccessKey, ""),
		BaseEndpoint: aws.String("http://" + addr),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := client.GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity against the real binary: %v", err)
	}
	if aws.ToString(out.Account) != awsident.AccountID {
		t.Errorf("Account = %q", aws.ToString(out.Account))
	}

	// Graceful shutdown: SIGTERM must exit 0 after draining.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("binary exited non-zero after SIGTERM: %v\nlog:\n%s", err, logTail.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("binary did not exit within 10s of SIGTERM")
	}
	if !strings.Contains(logTail.String(), "shutting down") {
		t.Errorf("expected graceful-shutdown log line; log:\n%s", logTail.String())
	}
}

// TestBinaryConfigPrint round-trips `config print` output back through --config.
func TestBinaryConfigPrint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-binary test in -short mode")
	}

	bin := filepath.Join(t.TempDir(), "doze-aws")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "config", "print", "--listen", "127.0.0.1:5678").Output()
	if err != nil {
		t.Fatalf("config print: %v", err)
	}
	if !strings.Contains(string(out), `listen = "127.0.0.1:5678"`) {
		t.Errorf("config print output missing overridden listen:\n%s", out)
	}

	// The printed config must load cleanly as a config file.
	cfgPath := filepath.Join(t.TempDir(), "doze-aws.toml")
	os.WriteFile(cfgPath, out, 0o644)
	out2, err := exec.Command(bin, "config", "print", "--config", cfgPath).Output()
	if err != nil {
		t.Fatalf("config print --config: %v", err)
	}
	if string(out2) != string(out) {
		t.Errorf("config print does not round-trip:\n--- first ---\n%s--- second ---\n%s", out, out2)
	}
}
