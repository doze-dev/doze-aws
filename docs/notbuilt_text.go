package docs

import "flag"

var updateRegister = flag.Bool("register.update", false,
	"rewrite docs/not-built.md from docs/notbuilt.go and the ledgers")

// registerPreamble and registerFooter are the hand-written half of
// docs/not-built.md. The tables between them are generated from the verdicts
// and the ledgers, so the list cannot drift from the code while the argument
// stays something a person wrote.
const registerPreamble = `# What doze-aws does not build, and why

Every absence on this page is deliberate, and every one is also an S-tier row in
[docs/api-support/](api-support/). A test reconciles the two in both directions,
so this page cannot fall behind the code without the build failing: a new
refusal with no verdict fails, and a verdict for something that has since been
implemented fails too.

**The distinction that matters is declined versus not yet**, and they are kept
in separate sections because they are different promises. A declined operation
is one where building it would mean pretending — emulating DNS that does not
resolve, an identity provider that does not exist, an account that is not there.
A deferred one is just work nobody has done, and it says what the work is.

Nothing here is a judgement about the operation. Most of these are good APIs
doing exactly what they should on AWS; they have nothing to act on when AWS is a
binary on your laptop.

## What it does not do, at all

For the person reviewing whether this is safe to run:

- **No outbound connections of its own.** It serves requests and calls its own
  services. It does not phone home, check for updates, or report usage — the
  only ` + "`https://`" + ` anywhere in ` + "`cmd/doze-aws`" + ` is a URL in the help text,
  and a test keeps it that way.
- **No account, no sign-up, no token.** Nothing to register, nothing to expire.
- **No telemetry and no analytics**, in the binary or the console.
- **Nothing outside the data directory you name.** Delete it and the state is
  gone; there is no other store, cache or profile.
- **No TLS.** It serves plaintext HTTP/1.1 on a loopback address unless you
  hand it one with ` + "`--listen`" + `, so there is no certificate to manage and
  nothing to trust into a system store. An SDK takes the endpoint as given;
  point it at ` + "`http://`" + ` and it works. Reaching it from beyond the machine is
  a job for whatever already terminates TLS for you.
- **No container image, and no Dockerfile.** Not an oversight: "no Docker" is
  half the argument for this project, and a static binary with no runtime
  dependencies does not need one. If your team already runs everything under
  compose, [getting-started.md](getting-started.md) has the two-line
  ` + "`Dockerfile`" + ` and the compose service to write yourself.
- **Apache 2.0**, and it runs fully offline.

## Declined

`

const registerFooter = `
---

## How to read a row

The reason in each row is the one in that service's ledger — this page shows the
category and points at the single source rather than copying eighty-one reasons
into a second place that could then disagree with the first.

If something here is blocking you, it is worth raising. "Declined" is a judgement
about what can be honestly emulated on one machine, not a refusal to discuss it,
and two of the arguments — *nothing here for it to do* and *something local
already covers it* — stop being true the moment a local counterpart exists.
`
