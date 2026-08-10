# Endpoints — the contract

Where doze-aws answers, and which of those addresses are **promised** rather
than merely current. This page exists because an endpoint ends up in `.env`
files, CI configs and Terraform providers — the one thing that is expensive to
change after people depend on it.

## The promise

**`http://127.0.0.1:4566` is permanent.**

It is the default bind, and the port is LocalStack's on purpose, so an existing
project can point at doze-aws by changing nothing but the process that serves
it. It will not move, it will not be removed, and it will never require the
`doze` CLI to be installed.

Everything else on this page is **additive**. Names are a convenience layered on
top; none of them replaces the address, and none of them is needed to use
doze-aws.

If you want one rule to follow: set `AWS_ENDPOINT_URL` once, in one place, and
you are insulated from all of this.

## The addresses

| Address | Available when | Status |
|---|---|---|
| `http://127.0.0.1:4566` | always — standalone or embedded | **stable** |
| `http://aws.<stack>.doze` | under the `doze` CLI with `defaults { domains = true }` | **stable** (doze ≥ v0.1.3) |
| `http://aws.doze` | standalone, after `doze-aws dns-setup` | **available** (macOS) |

The middle one is per-stack, so several projects can run their own local AWS at
once without colliding — verified end to end: the name resolves through doze's
resolver, the shared `:80` ingress routes it by Host header, and AWS API calls
answer through it. It needs `doze dns-setup` once, so the OS routes `.doze`.

> Earlier doze releases printed the right name only when the config path was
> absolute; run from the project directory, `doze env` emitted
> `aws.default.doze`, which resolved to NXDOMAIN. Fixed in v0.1.3 — if
> `doze env` shows `default` where your directory has another name, upgrade.

The last one is a stable alias for "the local AWS on this machine," so a
single-stack setup and a standalone process can share one name.

`doze-kafka` follows the same pattern (`kafka.doze`).

## Standalone names

`doze-aws` on its own now serves `.doze` itself — no doze CLI required. It
claims `aws.doze`, and if nothing else is already serving the zone it answers
DNS for it, including names other doze binaries registered.

```sh
doze-aws dns-setup      # once per machine; any doze binary can run it
doze-aws
export AWS_ENDPOINT_URL=http://aws.doze
```

No port, and the same URL a doze stack serves. Port 80 comes from a **shared
front door**: one wildcard listener, held by whichever doze binary got there
first, routing by Host header to whichever one registered the name. macOS
refuses a privileged port on a *specific* address and allows it only on the
wildcard, which is why it cannot come from the name's own address.

The front door preserves the Host, so a queue created through `aws.doze`
reports an `aws.doze` URL rather than the address it was proxied to.

`.doze` is a **peer** zone rather than one binary's: whichever of `doze`,
`doze-aws` or `doze-kafka` starts first serves it for all of them, and if that
one exits another takes over. Install order does not matter and none of them is
a prerequisite for the others.

Without `dns-setup` the name is claimed but not served — doze-aws logs one line
saying so and carries on. The configured address is unaffected, which is the
whole point of it being a contract:

```
zone: name claimed but not served  name=aws.doze addr=127.0.0.2:80
  err="bind: can't assign requested address"
  hint="run `doze-aws dns-setup` once to alias the loopback pool"
```

**Platform caveat:** `.doze` resolution is macOS-only today. macOS routes a
whole TLD via `/etc/resolver/doze`; Linux has no equivalent, and doze does not
yet configure `systemd-resolved` or dnsmasq for you — `dns-setup` on Linux
reports success while only handling loopback aliases. On Linux, use
`127.0.0.1:4566`, which is exactly why it is permanent.

## Why moving between them costs nothing

This is the part worth understanding, because it is what makes the promise cheap
to keep.

doze-aws builds the URLs it hands back **from the Host header of the request
that asked**, not from a value fixed at creation time:

```go
// sqs/actions.go
func queueURL(host, name string) string {
	return "http://" + host + "/" + awsident.AccountID + "/" + name
}
```

So a queue created through `127.0.0.1:4566` reports a `127.0.0.1:4566` URL, and
the *same queue* reports an `aws.doze` URL when asked through that name. There
is one store underneath; the address is just how you arrived.

The practical consequence: **switching endpoints does not strand the resources
you already created.** The usual migration failure — stored URLs pointing at an
address that no longer answers — does not apply here.

## Starting standalone, adopting the CLI later

The intended path, and the one that must not hurt:

1. **Start standalone.** `doze-aws` on `127.0.0.1:4566`. No doze CLI, no DNS
   setup, no configuration.
2. **Later, adopt the CLI.** Declare an `aws` block in `doze.hcl`. Your data
   directory carries over; your existing endpoint keeps working.
3. **Optionally use the names.** `aws.<stack>.doze`, or `aws.doze`.

Nothing in step 2 or 3 obliges you to change what you wrote in step 1.

## How the names resolve

`.doze` is served by a small DNS server built into the doze daemon, listening on
127.0.0.1:5323, with the OS routed to it by a one-time drop-in:

```
/etc/resolver/doze     nameserver 127.0.0.1 / port 5323     (macOS, once, needs sudo)
```

Two properties worth knowing, because they explain the behaviour you will see:

- **Each name resolves to its own address**, not to a shared one. That is what
  lets every service answer on its canonical port — every Postgres on 5432 —
  rather than on hand-picked high ports.
- **A name that is not registered is NXDOMAIN.** The resolver answers only for
  what is actually running on this machine, so a typo fails as a name that does
  not exist rather than as a connection to the wrong thing.

Whichever daemon binds 5323 first answers for every stack on the machine, so
the names keep working when any individual stack goes down.

## Reading this page as a promise

- `127.0.0.1:4566` — will not change. Depend on it.
- `aws.<stack>.doze` — will not change while `domains` is enabled.
- `aws.doze` — stable on macOS once `dns-setup` has run. Not yet on Linux; see
  the platform caveat above, and prefer `127.0.0.1:4566` in anything that has
  to run on both.
