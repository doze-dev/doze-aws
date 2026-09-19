# SSM — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

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

## Differences from AWS

- **Parameter Store only.** The fleet-management half of SSM — Documents,
  Automation, Run Command, Sessions, patching, inventory, maintenance windows
  — answers `UnsupportedOperationException` by name. All of it manages managed
  instances, and there are none.
- **SecureString is encrypted with a local key**, generated once into the data
  directory rather than held by KMS. The value is genuinely encrypted at rest
  and genuinely not protected from anything with read access to that
  directory.
- **`DescribeParameters` filters on Name and Type.** Other filter keys are
  accepted and ignored rather than refused, because a filter this does not
  implement returning everything is a smaller surprise than a call that fails.

## Verified against

- **aws-sdk-go-v2** (`sdk_test.go`): versions and labels, SecureString
  round-trips including the overwrite that must keep the type, GetParametersByPath
  and DescribeParameters, delete and tags, and the fleet operations answering
  honestly rather than silently.
- **aws-sdk-go v1** (`sdkv1_test.go`): the parameter round trip through the
  older client.
- **Model-derived rejection parity** (`rejection_parity_test.go`), scoped to
  the dispatched operations, plus the measurement of what the constraint table
  is worth — see below.

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
