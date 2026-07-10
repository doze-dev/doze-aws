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
