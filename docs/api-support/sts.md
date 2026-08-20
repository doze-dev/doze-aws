# STS — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

| Operation | Tier | Notes |
|---|---|---|
| GetCallerIdentity | F | fixed local identity (account 000000000000, user/test) |
| AssumeRole | F | validates RoleArn/RoleSessionName/DurationSeconds; mints fresh ASIA-prefixed credentials |
| AssumeRoleWithWebIdentity | F | JWT accepted unverified; sub/aud/iss reflected into the response |
| AssumeRoleWithSAML | F | assertion accepted unverified; NameID/Issuer reflected into the response |
| AssumeRoot | F | mints short-lived credentials (≤900s) |
| GetSessionToken | F | |
| GetFederationToken | F | |
| GetAccessKeyInfo | F | every key maps to the one local account |
| DecodeAuthorizationMessage | S | doze-aws never produces encoded authorization messages, so there is nothing to decode |

All operations are served over the STS Query/XML protocol at the shared
endpoint, and accept SigV2, SigV4, or no signature at all.

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what STS refuses**.

**108/108 model-derived constraints enforced across 7 of the 8 dispatched
operations, with `knownGaps` empty.** Removing the table lets several through —
`GetSessionToken`'s `TokenCode` pattern and `SerialNumber` length among them —
so the table is doing work the hand-written checks were not.

Generated rather than hand-derived: `dzaudit cases sts` emits a violating value
per constrained input, `testdata/cases_sts.json` commits them, and
`rejection_parity_test.go` replays every one from a baseline it first proves the
service accepts.

`DecodeAuthorizationMessage`'s 3 cases cannot be audited: it is an honest stub —
doze-aws never produces encoded authorization messages — so it refuses a valid
request too, and replaying a mutation against it proves nothing.

### The Query protocol

STS is the first Query-protocol service audited, and it needed one piece of
machinery the awsJson services did not. Query flattens nesting into the key
(`Tags.member.1.Key`) where JSON nests it in the body, so the form is rebuilt
into the shape the constraint paths describe before the walk
(`modelcheck.FromQuery`). The test harness performs the same translation in
reverse, which means the two are checked against each other by every case that
reaches a nested path.

Two smaller differences, both real:

- The error **code** differs by protocol family. Query services answer
  `ValidationError`; the awsJson ones answer `ValidationException`. Same message,
  different envelope, and clients branch on it.
- Every value in a form is a string, so a `@range` constraint reads a numeric
  string — the model's own trait is what says a member is a number. Without
  that, every range constraint on a Query service would pass silently.
