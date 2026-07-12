# The stack file

`stack.yaml` is the declarative half of doze-aws: a file your team commits that
describes the queues, tables, buckets, functions, and wiring your app expects,
so `doze-aws apply` (or just starting the server with the file present) stands
the whole stack up. It exists so "clone the repo, run doze-aws" is the entire
onboarding story.

Three properties shape everything below:

- **Named, not ARN'd.** Resources are named by map key and reference each other
  by name. Inside one local account and region, names are unambiguous, and the
  file reads like the console.
- **Convergent.** Apply creates what's missing and cheaply updates what exists.
  It never deletes anything, and it never overwrites a value a human may have
  changed live (secrets and parameters keep their live value unless you say
  `force: true`).
- **Honest.** The schema covers what the emulator genuinely implements — every
  field below changes observable behavior locally. Cloud-only knobs (IAM
  policies, provisioned throughput, KMS multi-region…) are deliberately absent
  rather than silently swallowed.

## Using it

```sh
doze-aws apply                 # converge ./stack.yaml
doze-aws apply infra.yaml      # or a named file
doze-aws --stack infra.yaml    # serve, applying the file at boot
doze-aws export > stack.yaml   # write the running stack as a file
```

Apply talks to the running server if one is listening, and opens the data
directory directly if not. Every resource line in the report is marked
`+` created, `~` updated, or `=` already in place.

`export` is the inverse: click a stack together (console or CLI), export it,
commit the file. Secret and SecureString values are intentionally left blank
in exports.

## Variables

`${env:NAME}` expands from the environment; `${var:name}` expands from the
top-level `vars` block, overridable per-run:

```yaml
vars:
  bucket: uploads-${env:USER}

buckets:
  ${var:bucket}: {}
```

```sh
doze-aws apply --var bucket=uploads-ci
```

## Queues (SQS)

```yaml
queues:
  orders.fifo:
    fifo: true            # FIFO queue; the name must end in .fifo
    content_dedup: true   # content-based deduplication
    dlq: auto             # "auto" creates orders-dlq.fifo, or name a declared queue
    max_receives: 4       # receives before a message moves to the DLQ (default 3)
    visibility: 60        # visibility timeout, seconds
    delay: 5              # delivery delay, seconds
    retention: 86400      # message retention, seconds
    receive_wait: 10      # default long-poll wait, seconds
    max_size: 65536       # maximum message size, bytes
    tags: {team: shop}
  audit: {}               # plain standard queue
```

FIFO-ness is create-time-only; everything else converges on re-apply.

## Topics (SNS)

```yaml
topics:
  order-events:
    tags: {team: shop}
    subscriptions:
      - queue: audit                  # exactly one of queue / lambda / http
        raw: true                     # raw message delivery (no JSON envelope)
        filter: {kind: [click]}       # filter policy, inline YAML or a JSON string
      - lambda: resize
      - http: http://localhost:9090/hook
```

Queue and lambda subscriptions are auto-confirmed; http(s) endpoints get the
real SubscriptionConfirmation handshake.

## Buckets (S3)

```yaml
buckets:
  uploads:
    versioning: true
    object_lock: true         # implies versioning
    tags: {team: shop}
    cors:
      - origins: ["https://app.local"]   # required
        methods: [GET, PUT]              # required
        headers: ["*"]
        expose: [ETag]
        max_age: 3600
    lifecycle:
      - prefix: tmp/
        expire_days: 7          # expire current versions
        noncurrent_days: 3      # expire noncurrent versions
        abort_uploads_days: 2   # abort stale multipart uploads
    website:
      index: index.html
      error: 404.html
    notify:
      - events: ["s3:ObjectCreated:*"]   # the default if omitted
        prefix: incoming/
        suffix: .jpg
        queue: audit            # exactly one of queue / topic / lambda
```

CORS preflights, lifecycle expiry (janitor-driven), and website index/error
serving are all real locally. Notifications wire after functions and topics,
so forward references within the file always resolve.

## Tables (DynamoDB)

```yaml
tables:
  sessions:
    key: sessionId:S userId:S   # "pk:TYPE" or "pk:TYPE sk:TYPE"; S, N or B
    ttl: expiresAt              # TTL attribute — items really expire
    deletion_protection: true
    tags: {team: shop}
    gsis:
      by-user:
        key: userId:S createdAt:N
      by-email:
        key: email:S
        projection: INCLUDE     # ALL (default) | KEYS_ONLY | INCLUDE
        include: [displayName]  # non-key attributes, with INCLUDE
    lsis:
      by-created:
        key: createdAt:N        # the sort key; the partition key comes from the table
        projection: KEYS_ONLY
```

The key schema and LSIs are create-time-only. On an existing table apply
converges the rest: missing GSIs are added (with a synchronous backfill), TTL
is enabled, deletion protection toggles, tags merge.

## Functions (Lambda)

```yaml
functions:
  resize:
    runtime: provided.al2     # provided.*/go, python3.x, nodejs*, java*, ruby*, dotnet*
    handler: bootstrap
    code: ./lambda/resize     # local dir or binary, used in place (edit-and-reinvoke)
    command: [./run.sh]       # doze extension: run this instead of the runtime default
    env: {LOG_LEVEL: debug}
    timeout: 30               # seconds (default 3)
    memory: 1024              # stored and echoed; not enforced locally
    retries: 1                # async retry attempts, 0–2 (default 2)
    dlq: {queue: audit}       # where exhausted async invokes land
    on_success: {topic: order-events}   # async destinations:
    on_failure: {queue: audit}          # exactly one of queue / topic / lambda
    tags: {team: shop}
    triggers:                 # SQS event source mappings
      - queue: audit
        batch: 5              # batch size (default 10)
        enabled: false        # park the poller without unwiring it
```

Functions run as real supervised local processes speaking the Lambda Runtime
API — handlers get `AWS_ENDPOINT_URL*` pointing back at doze-aws, so they
reach every sibling service unmodified.

## Rules (EventBridge)

```yaml
rules:
  on-order:
    bus: shop            # custom buses are created on demand (default "default")
    pattern: {source: [shop.orders]}    # inline YAML or a JSON string
    schedule: rate(5 minutes)           # rate(...) fires locally; cron(...) is stored only
    enabled: false                      # store the rule DISABLED
    targets:
      - queue:audit                     # scalar shorthand: queue: / topic: / lambda:
      - lambda: resize
        input_path: $.detail            # deliver a JSONPath slice of the event
      - topic: order-events
        template: '{"msg": <msg>}'      # InputTransformer template
        paths: {msg: $.detail.message}  # <name> → JSONPath
      - queue: audit
        input: {fixed: payload}         # or a fixed literal input
```

A rule needs a pattern, a schedule, or both. Per target, `input`,
`input_path` and `template` are mutually exclusive.

## Keys (KMS)

```yaml
keys:
  app-key:                    # becomes alias/app-key
    rotation: true
    description: app data key
    tags: {team: shop}
  signing:
    spec: ECC_NIST_P256       # default SYMMETRIC_DEFAULT; RSA_*, ECC_NIST_*, HMAC_*
  rsa-crypt:
    spec: RSA_2048
    usage: ENCRYPT_DECRYPT    # override the spec default (RSA/ECC default SIGN_VERIFY)
```

Keys are addressed by alias, so apply is idempotent. All key families carry
real crypto locally.

## Secrets (Secrets Manager)

```yaml
secrets:
  app/config:
    value: '{"apiKey":"local"}'
    description: app config blob
    tags: {team: shop}
  app/blob:
    binary: aGVsbG8=          # base64 SecretBinary, instead of value
  app/rotated:
    value: fresh
    force: true               # overwrite the live value on every apply
```

Without `force`, an existing secret's value is never touched — the file seeds
it once and live edits win from then on.

## Parameters (SSM Parameter Store)

```yaml
parameters:
  /app/db/host: localhost     # scalar shorthand for {value: ...}
  /app/db/password:
    value: ${env:DB_PASSWORD}
    type: SecureString        # String (default) | SecureString | StringList
    description: local pg password
    tags: {team: shop}
    force: true
```

Same rule as secrets: no `force`, no stomping.

## See also

- [cli.md](cli.md) — the `apply` / `export` commands and server flags.
- [api-support/](api-support/) — exactly which operations each service
  implements, and how honestly.
