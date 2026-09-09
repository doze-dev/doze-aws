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

## Resource policies

Seven services carry a policy on the resource itself — a bucket policy, a
queue policy, a topic policy, a function's permissions (`AddPermission`), a
key policy, a secret's resource policy, a stream's resource policy — and
under `soft` and `enforce` each one is evaluated **by the service that owns
the resource**, on every request that names it. With IAM `off` the policies
are stored and returned and nothing consults them, which is what every
local emulator did before and what the default still does.

The rule is AWS's same-account rule:

- an explicit `Deny` in the identity policies or the resource policy denies;
- an `Allow` in either grants — a user with no identity policy at all gets
  in when the resource policy names it, and a user the identity policies
  allow gets in when the resource policy says nothing about it;
- otherwise the request is denied.

Naming the **account** (`arn:aws:iam::000000000000:root`, or the bare
account id — what SQS and SNS `AddPermission` write) admits the root
credentials and delegates to the identity policies of the account's users:
it does not, by itself, let an ungranted user in. Naming a user or role ARN
grants that identity directly. `NotPrincipal`, `*`, glob patterns on the ARN
and `Service` principals match as on AWS.

**KMS is the exception**, as it is on AWS: the key policy gates the identity
policies. An identity policy counts only while the key policy contains an
`Allow` for the account root covering the action — the default key policy's
one statement (`"Enable IAM policies"`). A key policy that names nobody in
the account locks everyone out, root and `PutKeyPolicy` included. That is
the real lockout AWS warns about, reproduced deliberately.

### Service principals

A service calling a sibling on behalf of a resource — S3 delivering a
notification to Lambda or SNS, SNS fanning out to SQS or Lambda, EventBridge
invoking a target, Logs shipping to Kinesis or Lambda, API Gateway invoking
an integration, Secrets Manager invoking a rotation function — calls as
that service's principal (`s3.amazonaws.com`, `sns.amazonaws.com`, …) with
`aws:SourceArn` set to the resource it acts for. A service principal has no
identity policy, so only the target's resource policy can admit it.

**Under `enforce`, service-to-service triggers therefore need permissions
exactly as they do on AWS.** An S3 notification does not invoke a function
until `AddPermission` grants `s3.amazonaws.com` for the bucket; an SNS
subscription does not reach a queue until the queue policy allows
`sns.amazonaws.com` for the topic. A stack that worked with IAM off will
lose its triggers when switched to `enforce`, and the soft-mode log says
which permission is missing before that happens. Lambda destinations and
event-source-mapping polls are the exception: they run under the function's
execution role on AWS, which doze-aws does not model, and pass.

### The handoff

The gateway middleware evaluates the identity policies and cannot evaluate
resource policies — it never sees a peer call, it resolves resources
loosely, and it can render only one error shape. The two halves meet
through request headers, a doze extension a client cannot forge (whatever a
client sends under `X-Doze-*` is stripped first):

| Header | Written by | Meaning |
|---|---|---|
| `X-Doze-Iam-Mode` | middleware | `soft` or `enforce` |
| `X-Doze-Principal` | middleware, or the peer transport | the caller: a user or role ARN, the account root, or a service principal |
| `X-Doze-Identity` | middleware | the identity verdict: `allowed`, `implicitDeny`, `root`, `service` |
| `X-Doze-Source-Arn` | peer transport | the resource a service call is made for |
| `X-Doze-Resource-Decision`, `-Matched-By` | the service, on the response | the combined verdict and the statement that decided, for the access log |

An implicit deny by the identity policies on a request naming a resource of
one of the seven services is not answered by the middleware; the service
finishes the question. An explicit deny, or an implicit deny on a request
naming no resource (`ListQueues`, `CreateKey`), is final at the middleware.
The access log records both halves: `Source=identity` for the middleware's
verdict, `Source=resource` for the service's, which is the one that counts.

### Not covered

Cross-account principals (there is one account; `aws:SourceAccount` is
always the local one, and `aws:SourceOwner` is never supplied, so SNS's
default topic policy, which conditions on it, delegates to identity policies
as it does on AWS), VPC endpoint policies, S3 access point policies, and
Lambda layer permissions (stored, not evaluated — a
layer is fetched by the function that names it, in the same account).

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

## Input validation

Separate from the tiers above. A tier says the operation is implemented; this
says whether doze-aws **refuses what IAM refuses**.

**702/702 model-derived constraints enforced across 89 of the 93 dispatched
operations, with `knownGaps` empty.** Removing the constraint table makes 319 of
those 702 cases slip through, so it is doing work the hand-written checks were
not.

Of the four with no cases, `GetAccountSummary` and `GetAccountPasswordPolicy`
take no constrained input, and `DozeAccessLog` and `DozeGeneratePolicy` are
doze-aws's own additions — they are not in AWS's model, so the model has nothing
to say about them.

Generated with `dzaudit cases iam`, committed to `testdata/cases_iam.json`, and
replayed case by case in `rejection_parity_test.go` from a baseline the test
first proves the service accepts. IAM speaks the Query protocol, so the harness
builds the nested shape the model's paths describe and flattens it into form
keys on the way out.

### Nearly every operation needs its own resource

This is the widest audit here, and almost all of the harness is `prepare`. IAM
is a graph of named things that reference each other, and most of its operations
either create a name that must not already exist or consume one that must:

- A group `DeleteGroup` deletes is a group the next `DeleteGroup` case cannot.
- `UpdateUser` and `UpdateGroup` **rename**, so the old name is gone afterwards.
- A user is capped at two access keys and a managed policy at five versions, so
  `CreateAccessKey` and `CreatePolicyVersion` exhaust a shared fixture within a
  handful of cases.
- An instance profile holds at most one role.
- Every `Untag*` needs a tag to be there, and takes it.

Operations also run in alphabetical order, which is nothing like the order that
would make them work. So rather than one fixture that erodes as the suite runs,
each mutating case builds and addresses its own resource — and where the case is
*about* the field `prepare` would set, `prepare` leaves it alone, or the harness
would overwrite the violating value with a valid one and the case would prove
nothing.

Two names are read back from the response rather than recomputed in the test:
the access key id, and the role name `CreateServiceLinkedRole` generates. A test
that re-derives the service's own naming rule cannot catch that rule being
wrong.
