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
