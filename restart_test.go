package dozeaws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/internal/dozetest"
)

// persistsAcrossRestart is the whole of a durability test: write through a
// service's API, close the stack, open a NEW one over the same data directory,
// and read back through its API.
//
// The stage for a service writes only what is specific to it — the writes, and
// what must come back — and gets the boot, the close, the reopen and the
// cleanup from here. Before this each durability test carried its own sixteen
// lines of boot-and-reopen, which is how one of them came to leave the first
// stack open and test nothing.
//
// W is whatever the write hands the read: the ids a service minted, a
// ciphertext that has to decrypt afterwards.
//
// opts adjust the StackConfig for both boots (the services to run, an IAM
// mode); DataDir and Logf are set here and cannot be overridden by accident.
func persistsAcrossRestart[W any](
	t *testing.T,
	write func(ctx context.Context, url string) W,
	verify func(ctx context.Context, url string, written W),
	opts ...func(*dozeaws.StackConfig),
) {
	t.Helper()
	if testing.Short() {
		t.Skip("boots two Stacks over a shared data dir")
	}
	ctx := context.Background()
	dir := t.TempDir()
	boot := func() (*dozeaws.Stack, *httptest.Server) {
		t.Helper()
		cfg := dozeaws.StackConfig{}
		for _, o := range opts {
			o(&cfg)
		}
		cfg.DataDir, cfg.Logf = dir, dozetest.Logf(t)
		stack, err := dozeaws.NewStack(cfg)
		if err != nil {
			t.Fatalf("boot over %s: %v", dir, err)
		}
		return stack, httptest.NewServer(stack.Handler())
	}

	first, firstTS := boot()
	written := write(ctx, firstTS.URL)
	firstTS.Close()
	if err := first.Close(); err != nil {
		t.Fatalf("closing the first stack: %v", err)
	}

	second, secondTS := boot()
	defer second.Close()
	defer secondTS.Close()
	verify(ctx, secondTS.URL, written)
}
