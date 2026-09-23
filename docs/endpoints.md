# Endpoints — where doze-aws answers

There are two ways to reach an instance, and deliberately only two.

| | | |
|---|---|---|
| **`aws.<instance>.doze`** | the default | the first run offers to set `.doze` up — one prompt, one sudo, once per machine |
| **`--listen host:port`** | explicit opt-in | for containers, CI, anywhere without DNS |

They are **exclusive**. With `--listen`, no `.doze` name is claimed at all: one
instance, one address, one answer to "where is it".

## Why a name

AWS puts the service and the region in the hostname, and doze-aws mints the
URLs it hands back from the host a request arrived on. So reached at a name, it
reports AWS's own shape with the suffix swapped and nothing else changed:

```
https://sqs.ap-south-1.amazonaws.com/811690671382/orders.fifo          AWS
http://sqs.ap-south-1.aws.harbour.doze/811690671382/orders.fifo        doze-aws

https://x70an6eshc.execute-api.ap-south-1.amazonaws.com/prod/          AWS
http://x70an6eshc.execute-api.ap-south-1.aws.harbour.doze/prod/        doze-aws

https://receipts.s3.eu-west-1.amazonaws.com/jan.pdf                    AWS
http://receipts.s3.eu-west-1.aws.harbour.doze/jan.pdf                  doze-aws
```

Reached at an address, the best it can report is that address. One address
cannot be seventeen services in as many regions, and every workaround for that
is a path shape AWS does not use.

## Instances are named

`harbour` above is the **instance name**. It defaults to the project directory,
so two projects on one machine never contend:

```
~/code/harbour $ doze-aws     →  aws.harbour.doze   →  127.0.0.17:4566
~/code/atlas   $ doze-aws     →  aws.atlas.doze     →  127.0.0.45:4566
```

Each gets its own loopback address from `doze-names`, so both bind the same
port and neither notices the other. Set it explicitly with `--name`, or the
`name` key in `doze-aws.toml`.

Everything beneath the name resolves to the same instance, which is what makes
the AWS-shaped hostnames work without registering each one:

```
sqs.ap-south-1.aws.harbour.doze                      → 127.0.0.17
receipts.s3.eu-west-1.aws.harbour.doze               → 127.0.0.17
x70an6eshc.execute-api.ap-south-1.aws.harbour.doze   → 127.0.0.17
sqs.ap-south-1.aws.nosuchinstance.doze               → NXDOMAIN
```

That last line is the property worth keeping: a typo in an **instance** name
still fails as a name that does not exist. A typo in a service or region label
reaches the owning instance and is answered there — unavoidable, since those
labels are minted at runtime and could never be pre-registered.

There is **no machine-wide `aws.doze`**. It existed as a shorthand for whichever
instance started first, which meant a URL under it pointed somewhere different
depending on boot order — unwritable-down, and failing by reaching the *wrong*
stack rather than by not resolving. If you want a short name, name the instance
something short.

### The `sync-` twin

Step Functions' `StartSyncExecution` and `TestState` are the two AWS operations
with a host prefix: every SDK sends them to `sync-<endpoint host>`. That host is
a *sibling* of the instance name rather than a descendant, so it is claimed
alongside it — `sync-aws.harbour.doze`. Under `--listen` there is no name, and a
client needs `disableHostPrefix`.

## The port is an internal detail

You do not type one. The name is served port-less through a shared `:80` front
door that routes by `Host` header, and the registry records whatever port the
instance actually bound.

It prefers 4566 on its own address, and takes any free port if something else
holds it. That is rare — each instance has its own address — but it is no longer
fatal, because the port is not something anyone depends on.

## `--listen`, and what it costs

```sh
doze-aws --listen 127.0.0.1:4566
```

For a sibling container reaching this one over a compose network, for CI, for a
machine where `dns-setup` cannot run. It replaces the name rather than adding to
it, so:

- No `.doze` name is claimed and nothing is registered.
- URLs come back in their **path** form: `http://127.0.0.1:4566/000000000000/orders`,
  `…/_aws/execute-api/{id}/{stage}/…`, `…/_aws/lambda-url/{id}/…`.
- SDK virtual-hosted S3 addressing needs `UsePathStyle`, since there is no
  hostname space to address buckets under.
- Step Functions' sync operations need `disableHostPrefix`.

`--suffix` still applies, and that is the containerised-behind-a-proxy case: a
proxy owns `aws.demo.doze` and forwards to doze-aws on an address, so doze-aws
needs telling which suffix to mint under. That is the one place the two
mechanisms meet.

## Moving between them costs nothing

Every user-facing URL is minted from the `Host` a request arrived on, not from a
stored string. So the same queue reports a `.doze` URL when asked through the
name and a `127.0.0.1` URL when asked through an address, and nothing stored has
to change. What you created is addressed by name and account inside the data; the
URL is a view of it.

## URL shapes, and how one address tells them apart

AWS separates its services by hostname. doze-aws supports that — the shapes at
the top of this page — but it cannot *rely* on it, for a reason that has nothing
to do with DNS setup: **`AWS_ENDPOINT_URL` is a fixed string.** The SDKs do not
template a region or a service into it, so a client configured with one endpoint
sends every service there whatever its hostname could have been. Under `--listen`
there is no hostname at all.

So `internal/gateway` tells requests apart by what is left, and hostname routing
is *additional* rather than load-bearing.

### What identifies a request

In order. The first rule that matches wins.

| | Rule | Example |
|---|---|---|
| 1 | `X-Amz-Target` prefix | `AmazonSQS.SendMessage` |
| 2 | SigV4 credential scope | `.../us-east-1/dynamodb/aws4_request` |
| 3 | Lambda control-plane path | `/2015-03-31/functions/…` |
| 4 | API Gateway control-plane path | `/restapis`, `/v2/apis` |
| 5 | A deployed API | `/_aws/execute-api/{id}/…` or `{id}.execute-api.…` |
| 6 | A function URL | `/_aws/lambda-url/{id}/…` or `{id}.lambda-url.…` |
| 7 | Smithy RPC v2 path | `/service/{Service}/operation/{Op}` |
| 8 | Query-protocol `Action` | `?Action=SendMessage` |
| 9 | A queue URL | `/000000000000/orders` |
| 10 | otherwise **S3** | `/my-bucket/key.txt` |

Rules 1, 2 and 8 cover every SDK call: an SDK signs, and the scope names the
service. The rest exist for requests that carry no signature — a URL copied out
of the console and opened in a browser, a webhook, a `curl`.

### Two conventions worth knowing

**`/_aws/{plane}/{id}/…`** is how a data plane AWS addresses by hostname is
reached without one. A deployed API is `/_aws/execute-api/{id}/{stage}/{path}`;
a function URL is `/_aws/lambda-url/{id}/{path}`. Both also accept the AWS host
shape for a client that can set `Host`, which is what makes a rewritten hostname
work without a rewritten path.

**S3 is the fallback**, because the host and path shapes S3 clients produce are
too varied to enumerate. That is a deliberate trade with one consequence worth
stating plainly: a shape no rule above claims is not merely unrouted, it is
answered by S3. That is why a queue URL needed rule 9 — until it had one,
opening `/000000000000/orders` answered `NoSuchBucket`, naming neither the
service asked for nor the mistake.

### Why the queue URL is a special case

`/{account}/{queue}` is AWS's own shape, and `GetQueueUrl` has to return
something the SDKs will accept and call back into — so it cannot be changed to
something unambiguous. It is also indistinguishable from S3 path-style
`/{bucket}/{key}`.

The account id resolves it: rule 9 claims **this instance's** account and
nothing else, so with the default account `/000000000001/orders` is still an S3
request. The cost is that a bucket named exactly like the account id would be
shadowed, which is a price worth paying.

LocalStack meets the same wall and offers five strategies for it — two put the
service in the hostname, one uses a `/queue/<region>/<account>/<queue>` path
prefix, and the one that looks like a bare `/{account}/{queue}` is the mode they
label legacy and warn causes conflicts. Reading the account id is how that shape
is kept without the conflict.

### What can be trusted

`internal/gateway/published_urls_test.go` walks every URL shape doze-aws hands a
user — queue URLs, invoke URLs, function URLs, S3 objects — and asserts each
routes to the service that issued it **unsigned**, exactly as a browser would
send it. If you add a URL shape a user can copy, add it there: the signed path
is never in doubt, and the unsigned one is where this goes wrong.

## Setting up `.doze`

**Usually you do not.** `doze-aws` checks on every start and, the first time it
finds `.doze` unresolvable, handles it according to who is asking:

| | |
|---|---|
| a terminal | offers it once — `Set it up now? [Y/n]`, default yes, one sudo |
| root (a container) | installs it, because there is nobody to ask |
| neither (CI, `doze-aws &`) | changes **nothing**, prints the ways forward, exits |

That last row is the important one. `sudo` with no terminal waits for a
password that will never arrive, and a server that hangs at boot is worse than
one that errors — CI would sit there until the job timed out with no
explanation. Passwordless sudo is the other trap: it would succeed silently and
rewrite a build machine's DNS as a side effect of starting a test fixture.

The explicit commands are for when you want it done deliberately:

```sh
doze-aws dns-setup           # one sudo, idempotent — for CI or a scripted install
doze-aws dns-setup --print   # print the script instead, to run yourself
doze-aws dns-setup --check   # does .doze resolve? non-zero if not
doze-aws doctor              # what is missing, and who holds which name
```

It aliases a loopback pool and points the resolver at a high port — macOS via
`/etc/resolver/doze`, Linux via `systemd-resolved` or an `/etc/hosts` block.

The zone is served by whichever doze binary is running — `doze`, `doze-aws` or
`doze-kafka` take turns, so a machine with only doze-aws installed still
resolves `.doze`, and doze-kafka's names work when doze-aws is the one running.

## What is promised

- **`aws.<instance>.doze`** and the AWS-shaped hostnames beneath it. This is the
  contract now.
- **`--listen host:port`** serves exactly what you ask for, on every platform,
  with no DNS.
- **Every URL is minted from the request's `Host`**, so moving between the two
  strands nothing.

**Not promised**, and never a default: `http://127.0.0.1:4566` as the address
doze-aws binds on its own, and `http://aws.doze` as a machine-wide name. An
early version of this page said the first was permanent. It was the LocalStack
drop-in, and a shared address cannot carry what a name carries — which is the
rest of this page. Both are still reachable, deliberately: the first with
`doze-aws --listen 127.0.0.1:4566`, which restores that behaviour exactly, and
the second by naming an instance.

See [COMPATIBILITY.md](COMPATIBILITY.md) for what the other surfaces promise.
