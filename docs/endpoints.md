# Endpoints — where doze-aws answers

`http://127.0.0.1:4566`, unless `--listen` says otherwise. Nothing to set up on
the machine, no sudo, nothing written outside the data directory.

```sh
doze-aws                            # http://127.0.0.1:4566
doze-aws --listen 127.0.0.1:4577    # another address: a second instance, a free port
doze-aws --listen 0.0.0.0:4566      # reachable from a sibling container
```

`eval "$(doze-aws env)"` points a shell's AWS SDK at it. `doze-aws doctor`
says where this configuration answers and whether something does.

## AWS-shaped hostnames

AWS puts the service and the region in the hostname, and doze-aws mints the
URLs it hands back from the host a request arrived on. Reached at an address,
those URLs use path shapes:

```
http://127.0.0.1:4566/000000000000/orders                  a queue URL
http://127.0.0.1:4566/_aws/execute-api/{id}/{stage}/…      a deployed API
http://127.0.0.1:4566/_aws/lambda-url/{id}/…               a function URL
```

so an SDK needs two settings it would not need against AWS:

- **S3**: path-style addressing (`UsePathStyle`, `s3_force_path_style`,
  `forcePathStyle`), since there is no hostname space to address buckets under.
- **Step Functions** `StartSyncExecution` and `TestState`: the host prefix off
  (`disableHostPrefix`), since every SDK otherwise sends them to
  `sync-<endpoint host>`.

Reached at a **name**, the URLs come back in AWS's own shape with the suffix
swapped, and neither setting is needed:

```
https://sqs.ap-south-1.amazonaws.com/811690671382/orders.fifo     AWS
http://sqs.ap-south-1.aws.shop.doze/811690671382/orders.fifo      doze-aws, named aws.shop.doze

https://receipts.s3.eu-west-1.amazonaws.com/jan.pdf               AWS
http://receipts.s3.eu-west-1.aws.shop.doze/jan.pdf                doze-aws, named aws.shop.doze
```

An SDK given the name itself as its endpoint, `http://aws.shop.doze`, puts the
bucket in front of it: `receipts.aws.shop.doze/jan.pdf`. That is the bucket
`receipts` too.

There are two ways to give it one:

- **Run it under [doze](https://github.com/doze-dev/doze)**, as the `aws`
  module. doze gives the instance a name, `aws.<stack>.doze` for an instance
  called `aws`, and every name under it and `sync-aws.<stack>.doze` reach it;
  nothing more to configure. Names are doze's: the standalone binary takes no
  part in them.
- **`--suffix`**, when something else owns a name and forwards to doze-aws on
  an address: a proxy, a container network's DNS. `--suffix aws.demo.test`
  tells doze-aws to recognise and mint hostnames under it; making
  `*.aws.demo.test` reach it is the proxy's job.

## Moving between them costs nothing

Every user-facing URL is minted from the `Host` a request arrived on, not from a
stored string. So the same queue reports a name's URL when asked through a name
and a `127.0.0.1` URL when asked through an address, and nothing stored has to
change. What you created is addressed by name and account inside the data; the
URL is a view of it.

## URL shapes, and how one address tells them apart

AWS separates its services by hostname. doze-aws supports that — the shapes at
the top of this page — but it cannot *rely* on it, for a reason that has nothing
to do with DNS setup: **`AWS_ENDPOINT_URL` is a fixed string.** The SDKs do not
template a region or a service into it, so a client configured with one endpoint
sends every service there whatever its hostname could have been. At an address
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

## What is promised

- **`127.0.0.1:4566`** is where doze-aws answers unless `--listen` says
  otherwise, on every platform, with no machine setup.
- **`--listen host:port`** serves exactly what you ask for.
- **Every URL is minted from the request's `Host`**, so a name in front of
  doze-aws (doze's, or a proxy's with `--suffix`) gets AWS-shaped URLs and an
  address gets path-shaped ones, from the same data.

Before 2026-11, the standalone binary claimed `aws.<instance>.doze` itself and
asked for a one-time machine setup; that is gone, because it shared machine
state (DNS, ports 80 and 443, a registry in `~/.doze`) with doze and doze-kafka,
and names belong to doze.

See [COMPATIBILITY.md](COMPATIBILITY.md) for what the other surfaces promise.
