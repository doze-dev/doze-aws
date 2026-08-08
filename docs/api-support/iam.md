# IAM — API support

Tiers: **F** = functional (real local semantics, SDK-observable behavior
matches AWS) · **C** = cosmetic (accepted and round-tripped, no local effect) ·
**S** = stub (clean error; emulating it locally would be a lie).

All 176 documented IAM operations are accounted for: 91 are handled, 85 answer
a clean refusal naming the reason. Nothing falls through to a bare
`InvalidAction`.

## The three modes

IAM is the one service where full fidelity by default would be actively
hostile — turn enforcement on under an existing test suite and everything fails
at once. So enforcement is a dial:

| Mode | Behaviour | Cost |
|---|---|---|
| `off` *(default)* | Full CRUD; every API works; nothing is ever denied. | None — the middleware is not installed at all |
| `soft` | Every request is evaluated and recorded. Nothing is blocked; would-be denials are logged. | One evaluation per request |
| `enforce` | Denials are real and answer `AccessDenied`. | One evaluation per request |

```sh
doze-aws --iam-mode soft        # observe
doze-aws --iam-mode enforce     # enforce
```

Both LocalStack and moto also default to permissive, for the same reason. What
doze-aws adds is the middle rung being *useful* rather than merely quiet.

## Least-privilege generation

Soft mode records every `(principal, action, resource)` tuple a workload
actually exercised. Two doze extension actions read that back:

| Action | Purpose |
|---|---|
| `DozeAccessLog` | Every recorded decision — principal, action, resource, verdict, count. `Reset=true` clears it. |
| `DozeGeneratePolicy` | Emits a policy document granting exactly what was used. `Principal=<arn>` filters; `ScopeToResources=true` produces one statement per resource. |

Run a test suite in soft mode, ask for the policy, commit it. `UnresolvedResources`
in the response counts the calls whose resource ARN could not be determined, so
a scoped policy never quietly pretends to be narrower than it is.

## Evaluation

The engine implements AWS's ordering exactly: an explicit `Deny` anywhere wins,
then any `Allow` grants, otherwise the request is implicitly denied. It covers
`Action`/`NotAction`, `Resource`/`NotResource`, wildcards (`*` and `?`),
permissions boundaries as a ceiling, and group-inherited policies for users.

Condition operators: `StringEquals`/`NotEquals`/`EqualsIgnoreCase`/`Like`/`NotLike`,
`Numeric*`, `Date*`, `Bool`, `IpAddress`/`NotIpAddress`, `Arn*`, `Null`, the
`...IfExists` suffix, and the `ForAllValues:`/`ForAnyValue:` set quantifiers.
Unknown operators never match rather than being guessed at.

Context keys supplied automatically: `aws:PrincipalArn`, `aws:PrincipalAccount`,
`aws:username`, `aws:SourceIp`, `aws:UserAgent`, `aws:SecureTransport`,
`aws:RequestedRegion`.

### The resource boundary

Enforcement resolves the action from every request exactly — JSON services from
`X-Amz-Target`, Query services from `Action`, S3 and Lambda from method and
path. The **resource** is resolved where it can be read unambiguously (queue,
table, stream, key, secret, parameter, bucket, object, function) and left
**empty** otherwise.

An empty resource matches only `"Resource": "*"` statements. doze-aws will not
invent an ARN to make a scoped policy appear to match — a wrong allow or a wrong
deny is worse than an honest "could not determine". The access log marks these
with `ResourceKnown=false`.

Virtual-hosted-style S3 addressing is deliberately not decoded for resource
extraction, since splitting it correctly needs the configured S3 host.

## Managed policies

AWS-managed policies are **synthesized from their naming convention** rather
than vendored. moto ships AWS's corpus as a multi-megabyte generated file;
doze-aws derives the same documents in a few hundred lines, and covers policies
for services that did not exist when the code was written.

| Name pattern | Document |
|---|---|
| `AdministratorAccess` | `*` on `*` |
| `ReadOnlyAccess` | `Get*`, `List*`, `Describe*`, `BatchGet*` |
| `PowerUserAccess` | `NotAction` excluding `iam:*`, `organizations:*`, `account:*` |
| `Amazon<Service>FullAccess` | `<prefix>:*` |
| `Amazon<Service>ReadOnlyAccess` | the read verbs, prefixed |
| `AWSLambda<Source>ExecutionRole` | the source's read actions plus `logs:*` |

A name matching no pattern answers `NoSuchEntity` rather than being invented, so
a template referencing a policy doze-aws cannot model fails loudly.

Customer-managed policies are stored properly, with up to five versions and the
same default-version and delete-guard rules as AWS.

## Operation support

| Operation | Tier | Notes |
|---|---|---|
| CreateUser / GetUser / UpdateUser / DeleteUser / ListUsers | F | paths, tags, rename; delete refuses while policies or keys remain |
| TagUser / UntagUser / ListUserTags | F | |
| CreateGroup / GetGroup / UpdateGroup / DeleteGroup / ListGroups | F | GetGroup returns members |
| AddUserToGroup / RemoveUserFromGroup / ListGroupsForUser | F | group policies are inherited during evaluation |
| CreateRole / GetRole / UpdateRole / UpdateRoleDescription / DeleteRole / ListRoles | F | trust policy validated at create time |
| UpdateAssumeRolePolicy | F | |
| TagRole / UntagRole / ListRoleTags | F | |
| CreateServiceLinkedRole / DeleteServiceLinkedRole | F | synthesized under `/aws-service-role/<service>/` |
| CreatePolicy / GetPolicy / DeletePolicy / ListPolicies | F | delete refuses while attached; `OnlyAttached` honoured |
| CreatePolicyVersion / GetPolicyVersion / DeletePolicyVersion / ListPolicyVersions | F | five-version ceiling; the default version cannot be deleted |
| SetDefaultPolicyVersion | F | |
| TagPolicy / UntagPolicy / ListPolicyTags | F | |
| Attach/Detach {User,Group,Role}Policy | F | idempotent; attachment counts tracked |
| ListAttached{User,Group,Role}Policies | F | |
| ListEntitiesForPolicy | F | `EntityFilter` honoured |
| Put/Get/Delete/List {User,Group,Role}Policy | F | inline policies, validated on write |
| Put/Delete {User,Role}PermissionsBoundary | F | enforced as a ceiling during evaluation |
| CreateAccessKey / ListAccessKeys / UpdateAccessKey / DeleteAccessKey | F | the key id is how enforcement resolves a principal |
| GetAccessKeyLastUsed | F | real data — the middleware records it per request |
| CreateInstanceProfile / GetInstanceProfile / DeleteInstanceProfile / ListInstanceProfiles | F | |
| AddRoleToInstanceProfile / RemoveRoleFromInstanceProfile / ListInstanceProfilesForRole | F | one role per profile, as in AWS |
| TagInstanceProfile / UntagInstanceProfile / ListInstanceProfileTags | F | |
| CreateAccountAlias / DeleteAccountAlias / ListAccountAliases | F | |
| GetAccountSummary | F | live counts |
| GetAccountAuthorizationDetails | F | full principal dump with inline and attached policies |
| SimulateCustomPolicy / SimulatePrincipalPolicy | F | the same engine enforcement uses, so they cannot disagree |
| GetContextKeysForCustomPolicy / GetContextKeysForPrincipalPolicy | F | real analysis of the referenced condition keys |
| GetAccountPasswordPolicy | F | answers `NoSuchEntity`, as AWS does for an account that never set one |
| DozeAccessLog / DozeGeneratePolicy | — | doze extensions; see above |
| MFA devices (8 operations) | S | there is no MFA fleet locally |
| SAML and OIDC providers (17 operations) | S | federation needs a real identity provider |
| Server, signing and SSH credentials (12 operations) | S | certificate material is cloud infrastructure |
| Login profiles and password policy (7 operations) | S | there is no console sign-in locally |
| Credential and access reports (5 operations) | S | derived from CloudTrail history |
| Organizations and delegation (16 operations) | S | there is no Organizations locally |
| Service-specific credentials (5 operations) | S | for CodeCommit and Keyspaces, which doze-aws does not serve |

## See also

- [../cloudformation.md](../cloudformation.md) — deploying with the AWS CLI, SAM, CDK or Serverless.
- [cli.md](../cli.md) — the `--iam-mode` flag.
