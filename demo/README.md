# The Harbour demo stack

Seeds a running doze-aws with a realistic workload, using the AWS SDKs, so the
console has something worth photographing.

Harbour is a fictional grocery delivery company in central Scotland. Everything
here is invented but shaped like the real thing: Edinburgh and Glasgow
postcodes, supermarket prices, SKUs with the department prefix a buyer would
use. A console full of `test-bucket-1` and `{"foo":"bar"}` teaches nobody what
the tool is for.

No real people, addresses or card numbers. Phone numbers are in Ofcom's
`07700 900xxx` drama range and every domain is `example.com`.

## Running it

```sh
doze-aws --data-dir /tmp/harbour          # in another terminal

cd demo
bun install
bun seed.ts                               # paced, so you can watch it fill up
```

| | |
|---|---|
| `bun seed.ts` | the whole stack, paced for watching |
| `bun seed.ts --fast` | the same end state in about three seconds |
| `bun seed.ts --trade 10` | seed, then trade for ten minutes so things move |
| `bun seed.ts --only compute` | one section, while you iterate on a screenshot |
| `bun seed.ts --no-colour` | plain output, for pasting into a document |

Re-running is safe. Anything that already exists is reported as `= already
there` and stepped over, so you can re-seed before a fresh set of screenshots
without tearing the stack down.

## What you get

Sixteen services, populated with things that relate to each other rather than
sitting in isolation:

- **S3** — product imagery in department folders, a versioned receipts bucket, a
  public marketing site, lifecycle rules and CORS
- **DynamoDB** — the catalogue, the customer book, orders with a GSI and a
  stream, and baskets that expire on a TTL
- **SQS** — the checkout queue with a dead letter queue behind it, a FIFO
  dispatch lane grouped per van, and a poison message waiting to be redriven
- **SNS** — order events fanned out to the queue behind a numeric filter policy
- **EventBridge** — a domain bus, five rules including a cron schedule, an
  archive, and events that match two rules at once
- **Lambda** — four functions in four languages (below)
- **Step Functions** — one workflow that runs all four, with a Choice, a Wait
  and a retry policy; three executions including a rejection
- **Kinesis** — a clickstream with a registered fan-out consumer
- **API Gateway** — a REST API with a Lambda proxy and a MOCK integration, an
  API key and a usage plan; plus an HTTP API
- **CloudWatch** — three hours of backdated business metrics shaped like a
  trading day, three alarms, one of them red
- **CloudWatch Logs** — the pipeline's log groups, retention, and a metric
  filter turning `ERROR payment declined` into a metric
- **IAM, KMS, SSM, Secrets Manager** — roles and policies, a rotating key with
  encryption context, a parameter tree across prod and staging, and a secret
  with a previous version still readable
- **CloudFormation** — a loyalty stack deployed from a template, with
  parameters, outputs and an export

## The four-language Lambda pipeline

`functions/` holds four real handlers, and the order-fulfilment workflow runs
them in sequence. The languages are not a contrivance — each step is the kind of
work its language actually gets picked for.

| Function | Runtime | Why that language |
|---|---|---|
| `order-validator` | Node.js 22 (ESM) | lives next to the storefront, shares its validation rules |
| `price-calculator` | Go on `provided.al2023` | money in integer pence, no float anywhere near a total |
| `stock-forecaster` | Python | Holt linear trend forecasting, stdlib only |
| `dispatch-notifier` | Ruby | the customer-facing prose, where it has always lived |

They run as **real supervised processes** speaking the Lambda Runtime API — no
Docker, no image pull. The Go one is built to a `bootstrap` binary and speaks
the protocol directly with nothing but the standard library; run
`go build -o bootstrap .` in its directory if you change it.

The machine needs `node`, `python3` and `ruby` on `PATH` for three of them. A
missing interpreter is a `Runtime.LaunchError` on first invoke, not a timeout,
and the other three still work.

## Two things worth knowing

**The region is not yours.** doze-aws stamps `us-east-1` and account
`000000000000` onto every ARN it mints, whatever your client is configured
with. An ARN built with a different region names a resource that does not
exist — and the services that take an ARN rather than a name (an SQS redrive
policy, an EventBridge target, a Lambda event source) accept it quietly and
then never fire. `lib/aws.ts` pins the region for this reason.

**Kinesis needs the HTTP/1.1 handler.** Its JS SDK defaults to HTTP/2, which
doze-aws does not serve, and the failure is a bare `Protocol error` from
`node:http2` before a request ever leaves the client. `lib/aws.ts` hands that
one client a `NodeHttpHandler`. Every other client works with no configuration
beyond the endpoint.

## Taking screenshots

Run `bun seed.ts` without `--fast` and watch — the pauses are the point. Each
section prints where to look:

```
   → /_console/sqs  the peek panel, redrive policy and the FIFO lane
```

For anything that has to be *moving* — the Traffic feed scrolling, the live log
tail, a queue depth going up and down, an alarm crossing into ALARM — use
`--trade`. It places an order every four seconds across six services, starts a
workflow every third order, and fails a delivery every fifth, so the feed is not
uniformly green.
