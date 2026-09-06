# Lambda, locally

How a function runs on doze-aws, how to get one in, and how to see what it
did. The operation-by-operation ledger is
[api-support/lambda.md](api-support/lambda.md).

## How a function runs

A function is a **process on your machine**, started by doze-aws with the
same environment and the same wire protocol AWS gives it: the Lambda Runtime
API. doze-aws listens on a loopback port, sets `AWS_LAMBDA_RUNTIME_API`, and
the process long-polls it for invocations and posts responses back. That is
exactly what a `provided.*` bootstrap does, and what the official runtime
interface clients do inside AWS's containers. There is no Docker, no image
to pull, and a cold start is the time your interpreter takes to import your
handler.

For Python, Node and Ruby, doze-aws ships its own small Runtime API client
per language, embedded in the binary and written out under
`<data-dir>/shims` at start. So a `python3.12` function needs a `python3` on
`PATH` and nothing else — no `awslambdaric`, no `npx`. Point at a specific
interpreter when the one on `PATH` is not the one you mean:

```toml
[lambda]
# idle-timeout = "10m"   # how long a warm process waits for the next invoke
# quiet = false          # stop echoing function output to doze-aws's log

[lambda.runtimes]
python = "/opt/homebrew/bin/python3.12"
nodejs = "/Users/me/.nvm/versions/node/v20.11.0/bin/node"
```

Up to five processes per function serve concurrent invocations; an idle
one exits after the idle timeout. Go and any other compiled language run as
`provided.al2023` with a `bootstrap` binary. Java and .NET run too, with the
packaging AWS itself needs — the runtimes table in the ledger says exactly
what. For anything else, `Command` on the function (a doze extension)
replaces the launch line:

```sh
# the CLI refuses a member it does not know, so send it directly
curl -X PUT http://127.0.0.1:4566/2015-03-31/functions/f/configuration \
  -d '{"Command": ["deno", "run", "-A", "bootstrap.ts"]}'
```

## Getting a function in

Any of these, unmodified:

- **CDK**: `cdk deploy` against the doze-aws endpoint. Assets are staged in
  the local S3 and fetched from there; a `Version`, an `Alias`, a
  `FunctionUrl` and a `LayerVersion` in the app all become real —
  [cloudformation.md](cloudformation.md).
- **SAM**: `sam deploy`, with `AutoPublishAlias`, `FunctionUrlConfig` and
  `AWS::Serverless::LayerVersion` honoured.
- **The CLI or an SDK**: `create-function` with a zip.
- **In place, no upload**: point the code at a directory and edit it between
  invokes.

```sh
aws lambda create-function --function-name hello \
  --runtime python3.12 --handler app.handler \
  --role arn:aws:iam::000000000000:role/any \
  --code S3Bucket=_local_,S3Key=$PWD/src
```

The `_local_` bucket is the doze extension: `S3Key` is an absolute path to
the directory (or, for `provided.*`, the binary) and the function runs from
it. A warm process keeps the module it imported, so after an edit either
wait out the idle timeout or touch the configuration:

```sh
aws lambda update-function-configuration --function-name hello --timeout 10
```

In a CloudFormation template the same spelling is
`Code: {S3Bucket: _local_, S3Key: /abs/path}`.

## Seeing what it did

Three ways, all reading the same store.

**The terminal.** Every line a function prints is echoed to doze-aws's log
as it happens, prefixed with the function's name:

```
lambda[hello] START RequestId: 4b0c… Version: $LATEST
lambda[hello] handling order 7
lambda[hello] END RequestId: 4b0c…
lambda[hello] REPORT RequestId: 4b0c…	Duration: 1.20 ms	…
```

`[lambda].quiet = true` (or `-lambda-quiet`) turns the echo off; the other
two ways keep working.

**CloudWatch Logs.** Output lands under `/aws/lambda/<name>`, one stream per
process, each line stamped with the request id of the invocation that
printed it. The tools that read the cloud read this:

```sh
aws --endpoint-url http://127.0.0.1:4566 logs tail /aws/lambda/hello --follow
AWS_ENDPOINT_URL=http://127.0.0.1:4566 sam logs -n hello --tail
aws logs filter-log-events --log-group-name /aws/lambda/hello \
  --filter-pattern '{ $.level = "ERROR" }'
```

Retention is a day by default, or the group's `RetentionInDays` — see
[api-support/logs.md](api-support/logs.md).

**The console.** A function's **Logs** tab is a live tail across its
streams, with a request-id chip that narrows to one invocation and a filter
box that takes the same patterns. An invocation from the test panel links
straight to its lines, and every function call on the traffic wire — from
SQS, SNS, S3, EventBridge, API Gateway, Step Functions, a function URL —
carries its log tail in the drawer.

## Runtimes: what to know per language

| | |
|---|---|
| **Python** | `handler.py` with `def handler(event, context)`. Nested `a/b/mod.fn` works. `print` and `logging` both land in the logs; `AWS_LAMBDA_LOG_FORMAT=JSON` in the function's environment switches `logging` to JSON records the way it does on AWS. |
| **Node** | CommonJS or ESM — `.mjs`, `.cjs`, or `"type": "module"` in `package.json`; `async` handlers or the callback form. `node_modules` in the code directory is used as is. |
| **Ruby** | `def handler(event:, context:)` in the file the handler names. |
| **Go / Rust / anything compiled** | runtime `provided.al2023`, a `bootstrap` binary in the code directory built for the host (not for Linux, unless the host is). `aws-lambda-go` speaks the Runtime API already. |
| **Java** | the package must carry `aws-lambda-java-runtime-interface-client`; its native HTTP layer is Linux-only, so on macOS set `Command` to a launcher of your own. |
| **.NET** | publish as a self-hosting executable (`Amazon.Lambda.RuntimeSupport`, `LambdaBootstrap` in `Main`). |

## Versions, aliases and URLs

`publish-version` freezes the code — a copy, so a `_local_` directory can
keep changing under `$LATEST` — and the configuration. An alias points at a
version; invoking `hello:live` or `hello:2` runs that snapshot, and the
response says which in `X-Amz-Executed-Version`. Publishing an unchanged
function answers the version it already has, so a deploy tool that
publishes on every run does not pile up copies.

A function URL is served. Create one and `curl` it:

```sh
aws lambda create-function-url-config --function-name hello --auth-type NONE
# "FunctionUrl": "http://127.0.0.1:4566/_aws/lambda-url/<id>/"
curl -X POST http://127.0.0.1:4566/_aws/lambda-url/<id>/orders/7 -d '{"n":1}'
```

The request arrives as the payload-format-2.0 event (`rawPath`,
`queryStringParameters`, `headers`, `cookies`, `body`); the function returns
either a `{statusCode, headers, body}` object or a bare value, and the
gateway answers the way AWS does. The `https://<id>.lambda-url.us-east-1.on.aws/`
form routes too, for a client that can set `Host`.

## Layers

A layer is unpacked when published and its directories go onto the search
paths the runtime reads — `python/` on `PYTHONPATH`, `nodejs/node_modules`
on `NODE_PATH`, `ruby/lib` on `RUBYLIB`, `bin/` on `PATH`, `lib/` on the
library path — so `import helper` and a tool in `bin/` work. What does not
work is a literal `/opt/...` path in code; a local process has no `/opt`.
`_local_` works for layers as well: point `Content` at a directory laid out
like an unpacked layer.

## What is different from AWS

- The interpreter is the host's. `python3.12` runs whatever `python3` is.
- No memory limit, no `/tmp` limit, no execution role; IAM is not enforced,
  and an `AWS_IAM` function URL is served unsigned.
- Layers are on the search paths, not at `/opt`.
- Alias routing weights are accepted and not applied.
- A `bootstrap` binary is built for the host, not for Amazon Linux.
