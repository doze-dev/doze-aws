# SSM — API support

Tiers: **F** = functional · **C** = cosmetic round-trip · **S** = honest stub.

doze-aws implements the Parameter Store slice of SSM. SecureString values are
genuinely encrypted at rest with a per-data-dir AES-256-GCM key the service
manages itself; the KMS KeyId is recorded and returned cosmetically, so SSM
works with or without the kms service enabled.

| Operation | Tier | Notes |
|---|---|---|
| PutParameter | F | String/StringList/SecureString, Overwrite semantics, version bump, tags, Tier accepted (cosmetic), policies stored with **Expiration enforced by janitor** |
| GetParameter | F | `name`, `name:version`, `name:label`, ARN form; WithDecryption |
| GetParameters | F | found + InvalidParameters split |
| GetParametersByPath | F | hierarchy walk, Recursive flag |
| GetParameterHistory | F | all versions with labels |
| DeleteParameter / DeleteParameters | F | |
| DescribeParameters | F | Name (Equals/BeginsWith) and Type filters; other filter keys ignored |
| LabelParameterVersion / UnlabelParameterVersion | F | a label names at most one version (moves on re-label) |
| AddTagsToResource / RemoveTagsFromResource / ListTagsForResource | F | ResourceType Parameter only |
| Documents, Automation, Run Command, Sessions, fleet/instances, associations, patching, inventory, compliance, maintenance windows, OpsCenter, resource data sync, service settings | S | need managed instances / agent infrastructure that does not exist locally; each answers UnsupportedOperationException |

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what SSM refuses**.

**100/100 model-derived constraints enforced across all 13 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 75 of
those 100 cases slip through, so it is doing work the hand-written checks were
not.

Generated with `dzaudit cases ssm`, scoped to the dispatched operations, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts.

SSM has the largest model of any service here — 1,638 cases across 152
operations — and the ratio is the point. The other 1,538 fall on fleet
management: documents, Run Command, sessions, patching, inventory, OpsCenter,
maintenance windows. Those need managed instances that do not exist locally,
they answer `UnsupportedOperationException` before reading their input, and
there is nothing there to validate. Counting them as unaudited would overstate
the gap; counting them as audited would be a lie. They are neither.

### Every case gets its own thing to consume

Six of the thirteen operations destroy what they name. `DeleteParameter`,
`DeleteParameters`, `UnlabelParameterVersion` and `RemoveTagsFromResource` all
remove the resource the *next* case would have used, and a label may only sit on
one version at a time, so a second `LabelParameterVersion` moves rather than
adds. Operations run in alphabetical order, which is not the order that would
make them work, so each case creates its own parameter, label or tag first
rather than relying on the fixture surviving.
