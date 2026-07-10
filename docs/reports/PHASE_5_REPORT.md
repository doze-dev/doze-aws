# Phase 5 report — DynamoDB from scratch

Date: 2026-07-11

## Scope delivered

The largest single build in the roadmap, landed as four independently-tested
leaf packages plus the service:

- **internal/ddb/item**: full AttributeValue model with DynamoDB's semantics —
  numbers are normalized arbitrary-precision decimals (sign/digits/exponent,
  never floats) with numeric Compare/Add/Sub, 38-digit and range enforcement;
  sets reject duplicates and compare order-independently; wire JSON round-trip;
  documented 400 KB size accounting. Fuzz: parse→format→re-parse is
  value-identical, Compare antisymmetry, (a+b)−b == a.
- **internal/ddb/keyenc**: order-preserving byte encodings for S/N/B key values
  (escape+terminator for strings/bytes; sign-class + biased-exponent +
  complemented-negative digits for numbers) and composite pk+sk keys. The
  load-bearing property `CompareValues(a,b) == bytes.Compare(enc(a),enc(b))`
  survived 1.7M fuzz executions.
- **internal/ddb/expr**: one lexer, recursive-descent parsers for the shared
  condition/filter grammar (comparators, BETWEEN, IN, AND/OR/NOT, parens,
  attribute_exists/_not_exists/attribute_type/begins_with/contains/size),
  update expressions (SET arithmetic/list_append/if_not_exists, REMOVE with
  list indexes, ADD number/set-union, DELETE set-subtraction, clause and
  path-overlap validation), restricted key-condition shapes, and projections.
  Document paths get/set/remove nested values; updates apply to deep copies;
  unused `#name`/`:value` references raise ValidationException like AWS.
  Table-driven tests against AWS's documented examples; all parsers fuzzed.
- **internal/ddb/store**: tables in bbolt; items at `keyenc(pk[,sk])`; GSI/LSI
  entries (index key ++ length-prefixed primary key → primary key) maintained
  in the same transaction as every write, sparse semantics, synchronous
  backfill on UpdateTable-create; Query walks cursors forward/backward with
  Limit-counts-pre-filter and 1 MB page semantics; Scan with FNV-hashed
  segments; conditional writes with ReturnValuesOnConditionCheckFailure;
  TransactWrite = one bbolt Update (evaluate all conditions → CancellationReasons
  or apply all) with token idempotency; TTL lazy + sweeper.
- **dynamodb service**: AWS JSON 1.0 handlers for the full surface (34 ops
  functional/cosmetic, 28 honest stubs with named reasons), wired into the
  Stack (8/10 services live).

## Test evidence

- **aws-sdk-go-v2 contract tests**: CRUD with expression updates (arithmetic,
  list_append+if_not_exists) and every ReturnValues mode; conditional-write
  failure codes; unused-value rejection; Query with sort-key ranges + filters
  (count vs scanned-count semantics verified), descending order, paging to
  exhaustion; GSI queries including consistency across key-attribute updates;
  Scan with filters; batches; TransactWriteItems success + cancellation with
  per-item reasons (and proof the canceled write never landed);
  TransactGetItems; table lifecycle; TTL round-trip; PartiQL stub honesty.
- **aws-sdk-go v1 contract tests**: table create, typed item round-trip (N/SS),
  expression query, coded error envelopes.
- **TTL feature test** with an injected clock: lazy read filtering plus
  physical sweep, TTL-less items untouched.
- Everything `-race` clean; leaf packages carry their own fuzz targets.

## Notes

- The whole v2 contract suite passed on its first run against the finished
  pipeline — the payoff of building item/keyenc/expr as pure, heavily-tested
  leaf packages before any HTTP existed.
- Index entries are key references, not projected copies: reads chase the
  reference and apply the projection, which makes index consistency free and
  costs microseconds locally.
