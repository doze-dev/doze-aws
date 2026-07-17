# lambda-handler fixture

Source for the real, invokable "provided.al2" Lambda function that
`e2e/tests/lambda.spec.ts` points doze-aws's Lambda emulator at. It's a
minimal AWS Lambda Runtime Interface Client — see `main.go`'s doc comment.

This directory has its own `go.mod` (a standalone module, not part of the
root `github.com/doze-dev/doze-aws` module) so building it can never affect —
or be affected by — the root module's build.

## Build artifact policy

`main.go` and `go.mod` are the only committed files here. The compiled
`bootstrap` binary (and the `invocations.log` it writes at runtime) are build
output and MUST NOT be committed. `lambda.spec.ts` builds the binary itself
(in a `beforeAll`) into `e2e/.tmp/lambda-fixture/bootstrap` — `e2e/.tmp/` is
already gitignored by the repo root `.gitignore`, so this directory stays
free of build artifacts and the Lambda function's "code path" (`e2e/.tmp/lambda-fixture`)
never collides with the source directory.

To build by hand: `go build -o /path/to/output/bootstrap .` from this
directory.
