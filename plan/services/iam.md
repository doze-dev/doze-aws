# IAM and STS

Stage 15. Size L. Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds IAM's and STS's own.

## 1. Summary

Three things share this stage because none of them is finished without the
others.

- **IAM** (`iam/`): users, groups, roles, managed and inline policies, access
  keys, instance profiles, the account, the simulator, and two doze extensions
  (the access log and the policy generator). One bbolt store, Query protocol.
- **STS** (`sts/`): nine dispatched operations, stateless, Query protocol.
- **The guard and the evaluator** (`internal/iamguard`, `internal/iampolicy`,
  and the middleware in `dozeaws.go`): what turns a request into an
  (action, resource, principal) and a decision, in soft and enforce.

Peers: every service is guarded by it. Seven services (S3, SQS, SNS, Lambda,
KMS, Secrets Manager, Kinesis) run the guard on their own resource policies.
CloudFormation and `provision` create IAM resources. Lambda and Step Functions
store role ARNs they never evaluate. STS today knows nothing about IAM, which
is the stage's largest finding.

**Estimate:** L, five or more focused days. **Actual:** _fill in when done._
The estimate is likely low: see "what should change" at the end of section 11.

## 2. Contract

### 2.1 IAM

The model (`iam/testdata/ops_iam.json`) lists **180** operations. 91 are
dispatched (`iam/actions.go`, `handlers`), 85 are refused by name
(`iam/iam.go`, `stubActions`, `UnsupportedOperationException`), and 4 reach
neither table and answer `InvalidAction`. Two more dispatched actions are doze
extensions and are not in the model.

| Operations | Count | Now | Target | Note |
|---|---|---|---|---|
| CreateUser, GetUser, UpdateUser, DeleteUser, ListUsers | 5 | F | F | Rename: IAM-4, IAM-5. Delete rules: IAM-12. Paging: IAM-16. |
| TagUser, UntagUser, ListUserTags | 3 | F | F | The 50-tag limit is not established. |
| CreateGroup, GetGroup, UpdateGroup, DeleteGroup, ListGroups | 5 | F | F | Rename: IAM-4, IAM-6. |
| AddUserToGroup, RemoveUserFromGroup, ListGroupsForUser | 3 | F | F | |
| CreateRole, GetRole, UpdateRole, UpdateRoleDescription, DeleteRole, ListRoles | 6 | F | F | `MaxSessionDuration` is stored and read by nothing (per-setting table). |
| UpdateAssumeRolePolicy | 1 | F | F | Validated and stored. Consulted by nothing today (IAM-2). |
| TagRole, UntagRole, ListRoleTags | 3 | F | F | |
| CreateServiceLinkedRole, DeleteServiceLinkedRole | 2 | F | F | IAM-19, IAM-20. |
| CreatePolicy, GetPolicy, DeletePolicy, ListPolicies | 4 | F | F | |
| CreatePolicyVersion, GetPolicyVersion, DeletePolicyVersion, ListPolicyVersions, SetDefaultPolicyVersion | 5 | F | F | Five-version ceiling and default-version guard hold (run). |
| TagPolicy, UntagPolicy, ListPolicyTags | 3 | F | F | |
| Attach/Detach {User, Group, Role}Policy | 6 | F | F | |
| ListAttached{User, Group, Role}Policies, ListEntitiesForPolicy | 4 | F | F | |
| Put/Get/Delete/List {User, Group, Role}Policy | 12 | F | F | Put replaces a same-named policy, as AWS's Put does. |
| Put/Delete {User, Role}PermissionsBoundary | 4 | F | F | Applied at request time, not in the simulator (IAM-14). |
| CreateAccessKey, ListAccessKeys, UpdateAccessKey, DeleteAccessKey | 4 | F | F | IAM-3, IAM-9, IAM-10. |
| GetAccessKeyLastUsed | 1 | F | F | Real: the middleware writes it. |
| CreateInstanceProfile, GetInstanceProfile, DeleteInstanceProfile, ListInstanceProfiles | 4 | F | F | IAM-11. |
| AddRoleToInstanceProfile, RemoveRoleFromInstanceProfile, ListInstanceProfilesForRole | 3 | F | F | One role per profile holds (run). |
| TagInstanceProfile, UntagInstanceProfile, ListInstanceProfileTags | 3 | F | F | |
| CreateAccountAlias, DeleteAccountAlias, ListAccountAliases | 3 | F | F | IAM-23. |
| GetAccountSummary | 1 | F | F | Which keys of the summary map are filled is not established. |
| GetAccountAuthorizationDetails | 1 | F | F | `Filter` and paging are not established. |
| GetAccountPasswordPolicy | 1 | F | F | Always `NoSuchEntity`: nothing can set one. |
| SimulateCustomPolicy, SimulatePrincipalPolicy | 2 | F | F | IAM-13, IAM-14, IAM-15. |
| GetContextKeysForCustomPolicy, GetContextKeysForPrincipalPolicy | 2 | F | F | |
| MFA devices and their tags | 11 | S | S | "there is no MFA fleet locally" |
| SAML and OIDC providers, their tags, outbound federation | 21 | S | S | |
| Server, signing and SSH certificates and their tags | 17 | S | S | |
| Login profiles, ChangePassword, Update/DeleteAccountPasswordPolicy | 7 | S | S | |
| Credential report, service-last-accessed (3), ListPoliciesGrantingServiceAccess | 6 | S | S | |
| Organizations features, access reports, delegation | 15 | S | S | |
| Service-specific credentials | 5 | S | S | |
| SetSecurityTokenServicePreferences, GetServiceLinkedRoleDeletionStatus, GetHumanReadableSummary | 3 | S | S | Not in any ledger row today (IAM-37). |
| AcquireRole, GetAccountProperties, GetRoleTemplateVersion, PutAccountProperties | 4 | absent (`InvalidAction`) | S | Listed in `iam/coverage_model_test.go` as `Unreached`. The protocol allows nothing absent (IAM-38). |
| DozeAccessLog, DozeGeneratePolicy | 2 | extension | extension | Not AWS operations. IAM-7, IAM-8. |

91 + 85 + 4 = 180. The ledger's seven stub groups add up to 70, not 85.

### 2.2 STS

The model (`sts/testdata/ops_sts.json`) lists **11** operations. Nine are
dispatched (`sts/sts.go`, `handlers`).

| Operation | Now | Target | Note |
|---|---|---|---|
| GetCallerIdentity | F | F | Answers `user/test` for every caller, a user's key included. Target: the caller the key resolves to (IAM-1). |
| AssumeRole | F | F | Mints for any well-formed ARN. Target: the role exists and its trust policy admits the caller (IAM-2), and the credentials resolve to the role (IAM-1). |
| AssumeRoleWithWebIdentity | F | F or C | Token unverified, claims echoed. Whether the role must exist is a decision (section 10). |
| AssumeRoleWithSAML | F | F or C | Same. |
| AssumeRoot | F | C | Mints. Nothing treats the result differently from any other key. |
| GetSessionToken | F | F | Target: the session resolves to the calling user. |
| GetFederationToken | F | C or F | Whether a federated session is modelled is a decision. |
| GetAccessKeyInfo | F | F | Every key maps to the one account. |
| DecodeAuthorizationMessage | S | S | In `not-built.md`. |
| GetDelegatedAccessToken, GetWebIdentityToken | absent (`InvalidAction`) | S | `Unreached` in `sts/coverage_model_test.go` (IAM-38). |

### 2.3 The guard and the evaluator

"The operation is F" says nothing about what is evaluated. These tables do.

**Which requests are evaluated** (`internal/iamguard/resolve.go`,
`actionPrefixes` and `resourceField`; `iam/authorize.go`,
`ResourcePolicyServices`).

| Service | Action resolved | Resource resolved | Resource policy evaluated | Target |
|---|---|---|---|---|
| S3 | yes (own resolver) | bucket, object | yes | F |
| Lambda | yes (own resolver) | function | yes (permissions) | F |
| SQS | yes | queue | yes | F |
| SNS | yes | topic, subscription | yes | F |
| KMS | yes | key | yes; gates identity policies | F |
| Secrets Manager | yes | secret | yes | F |
| Kinesis | yes | stream | yes | F |
| DynamoDB | yes | table | no (none modelled) | F |
| Parameter Store | yes | parameter | n/a | F |
| EventBridge | yes | `Name`, `EventBusName` | no | F; which ARN `Name` becomes per operation is not established |
| CloudWatch | yes | first alarm of a batch; none for metrics | n/a | F, as documented |
| CloudWatch Logs | yes | none: always empty | no | not established whether that is intended |
| Step Functions | yes | machine, activity, execution | n/a | F |
| IAM, STS | yes | none | n/a | F |
| CloudFormation | **no** | no | n/a | decide (IAM-18) |
| API Gateway | **no** | no | n/a | decide (IAM-18) |

**Who the caller is** (`iam/authorize.go`, `principalFor`).

| Credential on the request | Resolves to | Target |
|---|---|---|
| A user's access key id, Active | that user | F |
| A user's access key id, Inactive | that user (IAM-3) | refused |
| A key id minted by STS for a role | the account root (IAM-1) | the role session |
| A key id from GetSessionToken or GetFederationToken | the account root | decide |
| `test`, any unknown key id, or no signature | the account root, admitted | F, a listed difference |
| A service principal on a peer call | the service; only a resource policy can admit it | F |
| The console | bypasses the middleware (peer transport); never evaluated or recorded for identity | F, to be written in the ledger |

The signature is never verified. That is a listed difference and stays one.

**Policy elements** (`internal/iampolicy/policy.go`, `principal.go`, `conditions.go`).

| Element | Now | Note |
|---|---|---|
| Effect, Action, NotAction, Resource, NotResource, `*` and `?` | F | Action compares without case; ARNs with case. |
| Explicit Deny wins; any Allow grants; otherwise implicit deny | F | Tested (`enforce_test.go`, `evaluate_test.go`). |
| Principal, NotPrincipal (resource policies) | F | `NotPrincipal` with Allow matches nobody. |
| Same-account rule; account principal delegates | F | `iamguard.combine`. |
| KMS key-policy gate | F | |
| Group policies inherited by users | F | Broken by a group rename (IAM-6). |
| Permissions boundary | F at request time | Not in the simulator (IAM-14). |
| Policy `Version` | not checked | `2020-01-01` is accepted (IAM-21). |
| Policy `Id`, statement `Sid` uniqueness | not established | |
| Policy variables (`${aws:username}`) | not established | |
| Session policies, `PolicyArns` on AssumeRole | accepted by validation, ignored | Target C, said so. |
| Trust policy | validated, stored | Evaluated by nothing (IAM-2). |
| Size limits (6 144 managed, 2 048 user inline, 10 240 role inline) | not established | Model length is 131 072. |

**Condition operators.** Implemented: `StringEquals`, `StringNotEquals`,
`StringEqualsIgnoreCase`, `StringNotEqualsIgnoreCase`, `StringLike`,
`StringNotLike`, the six `Numeric*`, the six `Date*`, `Bool`, `IpAddress`,
`NotIpAddress`, `ArnEquals`, `ArnNotEquals`, `ArnLike`, `ArnNotLike`, `Null`,
the `...IfExists` suffix, `ForAllValues:` and `ForAnyValue:`. An absent key
satisfies a negated operator. An unknown operator never matches. `BinaryEquals`
is not implemented; whether that is intended is not established.

**Condition keys supplied.**

| Key | Identity policies (`iam/authorize.go`, `contextFor`) | Resource policies (`iamguard/guard.go`, `Check`) | Simulator |
|---|---|---|---|
| `aws:PrincipalArn`, `aws:PrincipalAccount` | yes | yes | no |
| `aws:RequestedRegion`, `aws:SecureTransport`, `aws:SourceIp`, `aws:UserAgent`, `aws:username` | yes | **no** (IAM-17) | no |
| `aws:SourceAccount` | no | yes, always the local account | no |
| `aws:SourceArn` | no | yes, on a peer call | no |
| `aws:CurrentTime`, `aws:EpochTime`, `aws:PrincipalTag/*`, `aws:ResourceTag/*`, `aws:RequestTag/*`, `aws:TagKeys`, `aws:userid`, `aws:PrincipalType` | no | no | no |
| Service keys (`s3:prefix`, `sqs:...`, `kms:ViaService`) | no | not established per service | no |
| `ContextEntries` passed to Simulate | n/a | n/a | yes, and nothing else |

A statement conditioned on a key that is not supplied can never match with a
positive operator and always matches with a negated one. The ledger and the
console must list exactly the supplied set.

## 3. Where it stands

| Layer | What exists |
|---|---|
| Package tests, `iam/` | 40 tests in 9 files: SDK v2 lifecycle (12), the three modes (6), resource policies (4 + 5 + 4), spoofing (4), conformance fixes (2), rejection parity (2), model coverage (1) |
| Package tests, `sts/` | 14 tests in 5 files: SDK v2 (7 + 1), SDK v1 (4), rejection parity (1), model coverage (1) |
| `internal/iamguard` | 25 tests: guard (4), resolve (8), reauthorize (6), chunked peek (4), edges (3) |
| `internal/iampolicy` | 24 tests: policy (10), conditions (5), evaluate (4), principal (3), public (2) |
| Root package | `iam_rest_actions_test.go` (1), `resource_policy_integration_test.go` (2), `identity_test.go` (3) |
| Console Go tests | `iam_mutations_test.go` (3), `access_policy_test.go` (6), `policy_editor_test.go` (2) |
| Parity | Ledger: 702 of 702 IAM model constraints across 89 of 93 dispatched operations; 108 of 108 for STS across 7 of 8; `knownGaps` empty in both |
| Conformance | `conformance/tests/test_iam.py`: 5 scenarios. `test_sts.py`: 2. All 7 **unverified**: `conformance/snapshots/` is empty |
| Browser | `e2e/tests/iam.spec.ts`: 14 tests in 8 groups (console, STS, policy builder, role, user, policy, profile, account and log) |
| Mutation | No per-package baseline (foundation B3) |

What no test holds today: that STS credentials are the role, that an inactive
key is refused, any rename, any delete-conflict beyond attached policies and
keys, the two-key limit, the contents of a generated policy, or the count in
the access log. Every P0 below went through a green suite.

## 4. Findings

Evidence `run` means reproduced on 2026-10-10 against a binary built from
`cmd/doze-aws`, in the mode named. `read` findings are reproduced before they
are fixed. Line numbers drift; the symbol does not.

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| IAM-1 | P0 | Credentials minted by STS are not honoured. Only a user's access key resolves to a principal; an `ASIA` key is unknown, so the call is the account root. Under enforce a role with no policies listed queues, and `GetCallerIdentity` answered `user/test`. An app tested "as its role" is tested as root, and the missing permission surfaces at deploy. | `iam/authorize.go` `principalFor`; `sts/actions.go` `mintCredentials`, `getCallerIdentity` | run (enforce) | doc: STS API reference, AssumeRole and GetCallerIdentity | `iam/enforce_test.go`: assume a role with no policies, sign with the result, expect `AccessDenied`; `sts/sdk_test.go`: `GetCallerIdentity` returns the assumed-role ARN |
| IAM-2 | P0 | `AssumeRole` reads neither the role nor its trust policy. It mints for a role that does not exist, and for one whose trust policy names only `lambda.amazonaws.com`. | `sts/actions.go` `assumeRole`; `sts/sts.go` (no store, no peer) | run | doc: AssumeRole errors; `AccessDenied` for an untrusted caller | SDK test: a missing role, and a role that does not trust the caller, both refused |
| IAM-3 | P0 | A deactivated access key still authenticates and is judged by its user's policies. The comment above the lookup says "An inactive key authorizes nothing"; no code reads `Status`. | `iam/authorize.go` `principalFor` | run (enforce) | doc: UpdateAccessKey; the code `InvalidClientTokenId` is memory | `enforce_test.go`: deactivate, call, expect a refusal |
| IAM-4 | P0 | Renaming a user or a group onto a name already in use overwrites the other record without a word. The overwritten user's policies and keys are gone. | `iam/store.go` `UpdateUser`, `UpdateGroup`; `iam/actions.go` `hUpdateUser`, `hUpdateGroup` | run | doc: UpdateUser and UpdateGroup list `EntityAlreadyExists` | SDK test: rename `a` to an existing `b`, expect `EntityAlreadyExists` and both intact |
| IAM-5 | P0 | Renaming a user leaves its access keys under the old name. `ListAccessKeys` for the new name is empty and a request signed with the key becomes the root. | `iam/store.go` `UpdateUser` (keys live in `keyBucket` by `UserName`) | run | memory: keys follow the user | SDK test: rename, list keys, call under enforce as the user |
| IAM-6 | P0 | Renaming a group leaves each member's membership under the old name. `ListGroupsForUser` is empty and the group's policies stop applying to its members (a simulated action went from allowed to implicitDeny). | `iam/store.go` `UpdateGroup`; `PoliciesFor` reads `userRecord.Groups` by name | run | memory: membership follows | SDK test: rename a group, `ListGroupsForUser`, simulate an action its policy allows |
| IAM-7 | P1 | The generated policy grants what was refused. Every recorded action is written as Allow, including an explicit Deny (`sqs:DeleteQueue`) and an implicit one (`s3:PutObject`), plus the root's own calls and `iam:DozeAccessLog` when no principal is named. | `iam/authorize.go` `hDozeGeneratePolicy` | run (enforce and soft) | n/a (ours) | Handler test: record one allow and one explicit deny; decide (section 10) and assert the document |
| IAM-8 | P1 | The access log counts a request to any of the seven resource-policy services twice: the middleware records the identity verdict and then the service's verdict under the same key. Two sends showed Count 4. | `iam/authorize.go` `evaluate`, `recorder.record`; `dozeaws.go` `authorized` (`RecordResource`) | run | n/a | Root test: one `SendMessage`, expect Count 1 |
| IAM-9 | P1 | No limit on access keys per user. A third key was created. `SUPPORT.md` says "a user is capped at two access keys". | `iam/store.go` `CreateAccessKey` | run | doc: IAM quotas, two access keys per user; code `LimitExceeded` | SDK test: third `CreateAccessKey` refused |
| IAM-10 | P1 | Access key ids are 21 characters (`AKIA` plus 17). | `iam/store.go` `uniqueID`, `CreateAccessKey` | run | memory: 20 | Unit test on the length once recorded |
| IAM-11 | P1 | `DeleteInstanceProfile` succeeds while the profile holds a role. | `iam/store.go` `DeleteInstanceProfile` | run | doc: DeleteInstanceProfile lists `DeleteConflict` | SDK test: delete with a role, expect `DeleteConflict` |
| IAM-12 | P1 | Delete guards are partial. `DeleteRole` and `DeleteGroup` check attached policies only; `DeleteUser` checks attached policies and keys. None checks inline policies, group membership, or a role's instance profiles, so a deleted role stays named in a profile and a deleted user in a group. | `iam/store.go` `DeleteRole`, `DeleteGroup`, `DeleteUser` | read | doc: each Delete's reference page lists what must be removed first | SDK tests: delete a role in a profile; a role with an inline policy; a user in a group; a group with a member |
| IAM-13 | P1 | `SimulateCustomPolicy` accepts a statement with no `Resource` and answers allowed; `CreatePolicy` refuses the same document. The simulator parses with `parsePolicy`, not `parseIdentityPolicy`. | `iam/simulate.go` `hSimulateCustomPolicy`, `parseInputList` | run | memory | Handler test: the same document through both |
| IAM-14 | P1 | `SimulatePrincipalPolicy` does not apply a permissions boundary; enforcement does. `SUPPORT.md` says the two "cannot disagree". | `iam/simulate.go` `hSimulatePrincipalPolicy`; `iam/authorize.go` `evaluate` | read | doc: SimulatePrincipalPolicy includes the boundary | Handler test: a user with a boundary that excludes the action |
| IAM-15 | P1 | The simulator returns one matched statement, labelled by `Sid` or `<policy>[i]`, with no `SourcePolicyType`, positions or `MissingContextValues`. AWS returns every matching statement. | `iam/simulate.go` `simulate`; `iampolicy.Evaluate` | read | doc: EvaluationResult shape | SDK test: two matching Allows, assert both come back |
| IAM-16 | P1 | Every IAM list ignores `MaxItems` and `Marker` and answers `IsTruncated=false`. The values are range-checked by the model table and then never read. | `iam/actions.go`, `policies.go`, `keys.go` (the `hList*` handlers) | read | model (members exist), doc (paging) | SDK test: `ListUsers` with `MaxItems: 1` over three users, then the paginator |
| IAM-17 | P1 | A resource policy is evaluated with four context keys only: `aws:PrincipalArn`, `aws:PrincipalAccount`, `aws:SourceAccount`, `aws:SourceArn`. `aws:SecureTransport`, `aws:SourceIp`, `aws:RequestedRegion`, `aws:UserAgent` and `aws:username` reach identity policies and not the guard, so a "deny plain HTTP" bucket policy never denies. `SUPPORT.md` lists all seven as supplied. | `internal/iamguard/guard.go` `Check` | read here; run in the S3 grounding (findings-overview 11) | doc | `guard_test.go`: a Deny on `aws:SecureTransport=false` over plain HTTP |
| IAM-18 | P1 | CloudFormation and API Gateway are missing from `actionPrefixes`, so no request to either is evaluated or recorded, in any mode. A policy on `cloudformation:*` can never matter, and the builder's autocomplete offers both prefixes. Soft mode's "every request is recorded" is false for them. | `internal/iamguard/resolve.go` `actionPrefixes`; `internal/console/handlers_iam.go` `seenActions` | read | n/a | `resolve_test.go`: a request to each resolves an action, or the ledger and the builder say it does not |
| IAM-19 | P1 | `CreateServiceLinkedRole` accepts any string as a service (`bogus` made `AWSServiceRoleForBogus`), and names the role by capitalising the first letter only (`AWSServiceRoleForAutoscaling`). | `iam/actions.go` `hCreateServiceLinkedRole` | run | memory: AWS refuses a service with no linked role, and spells it `AutoScaling` | SDK test, skipped until recorded |
| IAM-20 | P1 | `DeleteRole` deletes a service-linked role. | `iam/store.go` `DeleteRole` | run | memory: AWS refuses and requires `DeleteServiceLinkedRole` | SDK test, skipped until recorded |
| IAM-21 | P1 | A policy document with `"Version": "2020-01-01"` is accepted. | `internal/iampolicy/policy.go` `Parse` | run | memory: `MalformedPolicyDocument` | Policy test, skipped until recorded |
| IAM-22 | P1 | The synthesized `ReadOnlyAccess` grants `Get*`, `List*`, `Describe*`, `BatchGet*` and so denies `dynamodb:Query` and `Scan`, `sqs:ReceiveMessage`, `kinesis:GetRecords` and others. | `iam/managed.go` | run (`dynamodb:Query` simulated implicitDeny) | memory: the real policy allows `dynamodb:Query` and `Scan` | Unit test over a short list of real actions, once recorded |
| IAM-23 | P1 | The account alias is one string: `CreateAccountAlias` replaces whatever was set and `DeleteAccountAlias` clears it whatever name is passed. | `iam/policies.go` `hCreateAccountAlias`, `hDeleteAccountAlias` | read; a second create succeeding is run | memory | SDK test: delete a name that is not the alias |
| IAM-24 | P1 | An identity denial final at the middleware is written as JSON whatever protocol the service speaks, with code `AccessDenied` and no reason. Whether an XML or Query client reads the code from it is not established for S3, SNS, IAM and STS. | `dozeaws.go` `writeDenied`; `iam/errors.go` `errAccessDenied` | read; the JSON case is run (SQS) | doc | Root test: an explicit identity Deny on S3 and on a Query call, read through the Go SDK |
| IAM-25 | P2 | The draft simulator shows "implicit — nothing matched" on every row, allowed and explicit-deny rows included, because the client drops `MatchedStatements`. | `internal/console/client_iam.go` `SimulateCustom`; `templates/iam.html` `iam_sim_result` | run | n/a | Console test on a draft with a `Sid` |
| IAM-26 | P2 | "Condition keys referenced: … the simulation supplies the runtime set" is false: the console passes no `ContextEntries`, so the simulator has an empty context. | `templates/iam.html` `iam_sim_result`; `handlers_iam.go` `iamSimulate` | run | n/a | Browser test asserts the line; a handler test simulates a conditioned Allow |
| IAM-27 | P2 | The STS page says "that shell IS the principal" and "every AWS call from it authenticates as this principal". False until IAM-1 is fixed. | `templates/iam.html` `iam_sts`, `iam_sts_creds` | run | n/a | The IAM-1 test, then a browser test on the text |
| IAM-28 | P2 | The deactivate tooltip and toast say requests signed with the key are refused. False until IAM-3 is fixed. | `templates/iam.html` `iam_principal`; `handlers_iam.go` `iamKeyToggle` | run | n/a | The IAM-3 test |
| IAM-29 | P2 | Generate offers every user and role as a principal; a role is never recorded, so choosing one answers `NoSuchEntity`. The console never sends `ScopeToResources`, so `Resource` is always `*`. The hint says "least privilege by construction". | `templates/iam.html` `iam_home`; `client_iam.go` `GeneratedPolicy` | run | n/a | Handler test: the list holds only recorded principals; the scoped document |
| IAM-30 | P2 | "No access keys. Locally any credentials are accepted unless IAM is enforcing" and the create form's user hint say the same. Under enforce an unknown key is still the root. | `templates/iam.html` `iam_principal`, `iam_create` | read (`principalFor`) | n/a | Browser test on the text |
| IAM-31 | P2 | The `slr` branch of the delete handler cannot be reached: no template sends `slr`. A service-linked role is deleted through plain `DeleteRole`. | `handlers_iam.go` `iamDeletePrincipal` | run | n/a | Console test deletes a service-linked role and asserts the call made |
| IAM-32 | P2 | "AWS-managed policies are synthesized on demand, so naming one that does not exist yet still works." A name that matches no pattern is refused `NoSuchEntity`. | `templates/iam.html` `iam_principal` | run | n/a | Browser test on the text |
| IAM-33 | P2 | The console cannot do what the API can: a group's inline policies, permissions boundaries, paths, a non-default version's document, `CustomSuffix`, a role's `Description` at create. | `templates/iam.html`; `client_iam.go` | read | n/a | One console test per item built; the rest listed as dropped |
| IAM-34 | P2 | "Add an inline policy" with a name the principal already has replaces that policy with no warning. | `handlers_iam.go` `iamPutInline` | run | doc: Put replaces | Browser test: the form says it will replace |
| IAM-35 | P2 | The mint form's duration field stops at 43 200 for Session token and Federation, where the API takes 129 600, and always sends 3 600, hiding each operation's own default. | `templates/iam.html` `iam_sts` | read | model | Browser test per mode |
| IAM-36 | P3 | The authorization export is the raw response: XML with URL-encoded documents. The client's comment says "pretty-printed". | `client_iam.go` `AuthorizationDetails`; `templates/iam.html` `iam_auth_details` | run | n/a | Decide: decode the documents, or fix the comment |
| IAM-37 | P3 | Ledger and comments. The seven stub rows in `SUPPORT.md` and `not-built.md` total 70; the table refuses 85. "One principal. Every caller is the same local identity" contradicts key-based resolution. "Credentials are minted, not honoured" is true and is the bug in IAM-1. The `principalFor` comment about inactive keys describes code that is not there. | `docs/SUPPORT.md`, `docs/not-built.md`, `iam/authorize.go` | read | n/a | The ledger test counts stub rows against `stubActions` |
| IAM-38 | P3 | Six model operations are absent rather than refused: four in IAM, two in STS. They answer `InvalidAction`. | `iam/coverage_model_test.go`, `sts/coverage_model_test.go` (`Unreached`) | read | model | The coverage tests fail on a non-empty `Unreached` |

Owned by the foundation, not repeated here:

| Foundation id | What of IAM's it covers |
|---|---|
| A2 | `fail_inline` says "AWS refuses this too" for `DozeGeneratePolicy`'s own `NoSuchEntity` and for every state conflict |
| A3 | The `Inactive` chip and the `enforce` mode chip are drawn in the failure colour |
| A5 | IAM tags are written one call per tag (`handlers_tags.go`, `SetIAMTag`) |
| A7 | `copy.go`'s `iam` entry is never rendered, and says "IAM is off by default" |
| A13 | Rename ("policies and keys follow") and instance-profile delete ("remove its role first") |
| Stage 0 acceptance | `SUPPORT.md` rows for Kinesis, KMS, S3 and Secrets Manager say "the default `off`" |

**IAM scope, decided (D6).** CloudFormation and API Gateway join the guard: both are added to
`actionPrefixes`, and `execute-api` becomes an evaluated service. This stage
owns the action tables and the resource ARN forms for all three and the access
log's claim that every request is recorded, which becomes true. The evaluation
itself lands in the owning stages: API Gateway's control plane and
`execute-api:Invoke` in stage 14, CloudFormation's control plane and the calls
a stack makes on the caller's behalf in stage 16. Because stage 14 runs before
this one, the `apigateway` and `execute-api` action tables are written there
and checked here. IAM-18 is closed by this decision, not deferred. Adds about
a day to this stage.

## 5. What to check

Beyond the common criteria. IAM and STS speak the Query protocol only.

**Users and groups**
- [ ] Name rules and lengths per kind (user 64, group 128), and `Path` rules.
- [ ] `CreateUser` and `CreateGroup` twice: `EntityAlreadyExists`.
- [ ] Rename to a free name: policies, tags, keys and memberships all follow (IAM-5, IAM-6).
- [ ] Rename to a taken name is refused (IAM-4).
- [ ] `DeleteUser` and `DeleteGroup`: each thing AWS requires removed first is checked, and the message names it (IAM-12).
- [ ] After a user is deleted no group lists it; after a group is deleted no user lists it.
- [ ] `AddUserToGroup` twice is idempotent; `RemoveUserFromGroup` for a non-member answers as AWS does.
- [ ] `PermissionsBoundary` at create and by Put; a boundary naming a policy that does not exist.

**Roles**
- [ ] Trust policy at create and update: no `Resource`, a `Principal` required, the four principal types.
- [ ] `MaxSessionDuration` 3 600 to 43 200, default 3 600, and what reads it after IAM-2.
- [ ] `UpdateRole` and `UpdateRoleDescription` change only what they name.
- [ ] `DeleteRole` with inline policies, attached policies, and in an instance profile (IAM-12).
- [ ] Service-linked roles: the service list, the name AWS gives, `CustomSuffix`, and which calls refuse to touch one (IAM-19, IAM-20).

**Policies and versions**
- [ ] Identity document: `Resource` required, no `Principal`, `Effect` one of two, `Version` (IAM-21).
- [ ] Document size limits per kind; set the tier (section 2.3).
- [ ] `Sid` uniqueness and character rules: establish and set.
- [ ] Five versions; `SetAsDefault`; the default cannot be deleted; a missing version on each call.
- [ ] `DeletePolicy` while attached; while it is a boundary (not established).
- [ ] An AWS-managed ARN on each mutating call is refused, and each documented name pattern resolves.
- [ ] The synthesized documents against the real ones for the names the console offers (IAM-22).
- [ ] `ListPolicies` with `Scope`, `OnlyAttached`, `PathPrefix`, `PolicyUsageFilter`.
- [ ] `AttachmentCount` and `PermissionsBoundaryUsageCount` after each change.
- [ ] Attach twice is idempotent; detach when not attached is `NoSuchEntity`.
- [ ] Attachment limits per principal: establish and set.

**Access keys**
- [ ] Two per user (IAM-9); the id's length and prefix (IAM-10).
- [ ] `UpdateAccessKey` to Inactive refuses the next signed call; to Active restores it (IAM-3).
- [ ] `CreateAccessKey` with no `UserName`: today refused; AWS uses the caller. Decide after IAM-1.
- [ ] `GetAccessKeyLastUsed`: date, service and region after a call; `N/A` before.
- [ ] Deleting a user's last key, and a key that is not that user's.

**Instance profiles, account**
- [ ] One role per profile; delete with a role (IAM-11); a deleted role leaves no profile naming it.
- [ ] Account alias: length, pattern, replace and delete semantics (IAM-23).
- [ ] `GetAccountSummary`: every key the SDK reads is present.
- [ ] `GetAccountAuthorizationDetails`: `Filter`, and that inline, attached and group lists are complete.

**Lists**
- [ ] Every `List*` honours `MaxItems` and `Marker` and returns `IsTruncated` (IAM-16).
- [ ] `PathPrefix` on each list that takes one.

**Tags**
- [ ] User, role, policy, instance profile: add, replace, remove, list; the 50 limit; key and value rules.
- [ ] `Tags` at create on each kind that takes them.

**Evaluation**
- [ ] Every operator in section 2.3 with one passing and one failing case, with `IfExists` and both quantifiers.
- [ ] Absent key: positive operator fails, negated operator passes, `Null` both ways.
- [ ] Every supplied key is supplied to both halves or the table says which (IAM-17).
- [ ] A key the table marks "no" is in the ledger as not supplied.
- [ ] Policy variables: establish and set.
- [ ] Explicit Deny beats Allow across inline, attached, group, boundary and resource policy.
- [ ] Boundary: allowed by identity and not by boundary is denied, in enforcement and in the simulator (IAM-14).

**Resolution and the middleware**
- [ ] For each of the 17 services, one request resolves the action AWS authorizes it as; batch operations resolve to their base action.
- [ ] For each service in the resource table, the ARN resolved is the ARN a policy would name.
- [ ] CloudFormation, API Gateway and Logs: decided and written (IAM-18).
- [ ] An empty resource matches only `"Resource": "*"`.
- [ ] A client's `X-Doze-*` headers are stripped in all three modes.
- [ ] The denial's code and message parse in the Go v2, Go v1 and boto3 clients for a JSON, a Query, a REST-XML and a REST-JSON service (IAM-24).
- [ ] The denial message's wording against AWS's, with and without a resource.
- [ ] Soft: the same requests are recorded and none refused; the log line names the reason.
- [ ] Off: no middleware, no record, policies stored and returned.

**The access log and the generator**
- [ ] One request is one count (IAM-8).
- [ ] A later deny replaces an earlier allow's verdict and caption; the reverse does not.
- [ ] `Reset=true` clears it.
- [ ] `DozeGeneratePolicy`: what a denied event contributes (IAM-7), `Principal`, `ScopeToResources`, `UnresolvedResources`, and that the result parses as an identity policy and is under the size limit.

**Simulator**
- [ ] The same document accepted or refused by Simulate and by Create (IAM-13).
- [ ] `ResourceArns` with several ARNs, `ContextEntries`, `PolicyInputList` on the principal form.
- [ ] `MatchedStatements` complete (IAM-15); `EvalDecision` spelling.
- [ ] A group ARN as `PolicySourceArn`.

**STS**
- [ ] `AssumeRole`: role exists, trust admits the caller, `DurationSeconds` against the role's maximum, `ExternalId`, session name rules (IAM-2).
- [ ] The returned key signs calls that are judged as the role session, and expires (IAM-1).
- [ ] `GetCallerIdentity` for the root, a user key, a role session, a federated session.
- [ ] `GetSessionToken` and `GetFederationToken`: ranges, defaults, and what the session resolves to.
- [ ] Web identity and SAML: required members, what is echoed, and the role-exists decision.
- [ ] `AssumeRoot`: 0 to 900, the task policy required.
- [ ] `GetAccessKeyInfo` for a user key, a session key and a malformed id.
- [ ] `sts:AssumeRole` is itself authorized against the caller's identity policies under enforce.

**Lifecycle**
- [ ] Everything in the store survives a restart. The access log does not, and the ledger says so.
- [ ] STS sessions across a restart: decide (kept, or invalid) and write it.
- [ ] Every ARN under a non-default `--account-id`; IAM is global, so `--region` must not appear in one.

## 6. Console

The design's "IAM and STS" page group. 26 boards. Templates:
`internal/console/templates/iam.html`, shared pieces in `panes.html`. Handlers:
`internal/console/handlers_iam.go`; client `client_iam.go`, `client_sts.go`;
routes `routes_stackwide.go`. The group replaces the older single boards
`Access` and `Sts`.

| Board | Screen | Lands in | Handler | Today |
|---|---|---|---|---|
| Iam-Home | Access log, soft | `iam_home`, `iam_lp` | `iamHome` | exists; the Last column is **new** |
| Iam-01-Empty | Nothing recorded, no principals | `iam_home`, `empty_detail` | `iamHome` | log exists; the empty-state card is **new** (foundation A7) |
| Iam-02-Log-Enforce | Access log, enforce, and what the caller was told | `iam_home` | `iamHome` | table exists; the three quoted denials are static text from `iam/errors.go` and `iamguard/guard.go` |
| Iam-03-Log-Off | Access log, off | `iam_home` | `iamHome` | exists |
| Iam-04-Generate | Generated policy, save as a policy | `iam_generated` | `iamGenerate`, `iamCreate` | exists; the scope toggle and scoped document are **new** |
| Iam-05-Simulate | Simulate a principal | `iam_sim_result` | `iamSimulate` | exists; the resource is shown once above the rows instead of per row |
| Iam-06-Simulate-Draft | Simulate a draft | `iam_sim_result` | `iamSimulate` | exists; Matched by for a draft and "create this as a policy" are **new** |
| Iam-07-Simulate-Refused | Simulate refusals | `iam_sim_result`, `fail_inline` | `iamSimulate` | exists; today the error is a bare `.err`, not `fail_inline` |
| Iam-08-Create-Names | New user, group, instance profile, service-linked role | `iam_create` | `iamCreatePage`, `iamCreate` | exists; teaching lines are **new** |
| Iam-09-Create-Role | New role and its trust policy | `iam_create`, `policy_builder` | `iamCreate` | exists; the two-documents table is **new** static teaching |
| Iam-10-Create-Policy | New policy, check the draft | `iam_create`, `policy_builder`, `iam_sim_inline` | `iamCreate`, `iamSimInline` | exists; the resource box on the check is **new** |
| Iam-11-Create-Refused | Create refused | `fail_inline` | `iamCreate` | exists |
| Iam-12-User | A user: attached, groups, tags, inline | `iam_principal`, `tag_editor` | `iamPrincipal`, `iamAttach`, `iamDetach`, `iamJoinGroup` | exists as one column |
| Iam-13-User-Keys | Access keys, the one-time secret, rename | `iam_principal`, the sticky flash | `iamNewKey`, `iamKeyToggle`, `iamDeleteKey`, `iamRenameUser` | exists |
| Iam-14-Inline-Edit | Edit and add an inline policy | `iam_principal`, `policy_builder` | `iamPutInline`, `iamDeleteInline` | exists |
| Iam-15-Role | A role: attached, trust, settings, profiles, inline | `iam_principal` | `iamPrincipal`, `iamRoleMeta` | exists; "Used by" is **new** |
| Iam-16-Role-Trust | Edit the trust policy | `iam_principal`, `policy_builder` | `iamTrust` | exists |
| Iam-17-Policy | A customer policy and its versions | `iam_policy` | `iamPolicy`, `iamSetDefaultVersion`, `iamDeleteVersion`, `iamDeletePolicy` | exists |
| Iam-18-Policy-NewVersion | Publish a version | `iam_policy`, `policy_builder` | `iamNewPolicyVersion` | exists |
| Iam-19-Group | A group | `iam_group` | `iamGroupPage`, `iamGroupMember`, `iamGroupRename` | exists |
| Iam-20-Profile | An instance profile, with and without a role | `iam_profile` | `iamProfilePage`, `iamProfileRole` | exists |
| Iam-21-Sts | Assume a role; key lookup | `iam_sts` | `iamSTS` | exists; the teaching lines and the fit check are **new** |
| Iam-22-Sts-Minted | Minted credentials; lookup result | `iam_sts_creds`, `iam_sts_keyinfo` | `iamSTSMint`, `iamSTSKeyInfo` | exists |
| Iam-23-Sts-Modes | The other five mints and their refusals | `iam_sts` | `iamSTSMint` | exists |
| Iam-24-Account | Summary, alias, export | `iam_account`, `iam_auth_details` | `iamAccount`, `iamAlias`, `iamAuthDetails` | exists |
| Iam-25-Confirms | Fourteen confirms, and what is refused after | `hx-confirm` in `iam.html` | the delete, detach, rename and version handlers | exists (foundation A13) |

**New items, and what each is built from**

| Item | Board | Built from |
|---|---|---|
| Last column | Iam-Home | `AccessEvent.Last`, already decoded from `DozeAccessLog`'s `LastUsed`, through the existing `ago` helper |
| Empty-state card | Iam-01-Empty | `copy.go`'s `iam` entry through `empty_detail`, with its first sentence corrected |
| Scope toggle and scoped document | Iam-04-Generate | `DozeGeneratePolicy`'s `ScopeToResources` and `UnresolvedResources`, which the service already returns |
| Matched by for a draft | Iam-06 | `MatchedStatements` in the `SimulateCustomPolicy` response (closes IAM-25) |
| Create this as a policy | Iam-06 | The document already in the form, posted to `/iam/create?kind=policy` |
| Resource box on "Check this draft" | Iam-10, Iam-14 | `iamSimInline` already reads `resource`; the builder never sends one |
| Used by | Iam-15-Role | `Role` in each function's configuration from `ListFunctions`, and `roleArn` from `DescribeStateMachine`. The console's `Function` type does not carry `Role` today; the field is added. |
| Teaching lines on names, session name, duration, max session | Iam-08, 09, 10, 15, 21 | Foundation B4 over `iam/validate.go` and `sts/validate.go` `constraintTables`, and the ledger |
| The duration fit check | Iam-15, Iam-21 | The role's `MaxSessionDuration` from `GetRole` against the form's duration |

**Where the teaching lines come from**

| Line | Source |
|---|---|
| Trust policy against permissions policy, with the four refusals | `iam/policy.go` `parseTrustPolicy`, `parseIdentityPolicy` |
| Explicit Deny wins, then Allow, then implicit deny | `iampolicy.Evaluate`; `SUPPORT.md` "Evaluation" |
| Same-account rule; a request naming no resource | `iamguard.combine`; `iam/authorize.go` `Authorize`; `SUPPORT.md` "Resource policies" |
| A blank resource matches only `*` | `iam/simulate.go` `simulate`; `SUPPORT.md` "The resource boundary" |
| Matched-by labels | `iampolicy.statementLabel`; `iam/store.go` `documentsOf` |
| Versions: five, default evaluated, default not deletable | `iam/store.go`; `documentsOf` reads `pol.Default()` |
| Group policies reach a user at request time | `iam/store.go` `PoliciesFor` |
| One role per profile | `iam/keys.go`, the `LimitExceeded` |
| Seven context keys on a real request | `iam/authorize.go` `contextFor`; `static/policy.js` `CONTEXT_KEYS` |
| The four principal types | `static/policy.js` `PRINCIPAL_TYPES` |
| STS ranges and defaults per mint | `sts/validate.go`, `sts/actions.go` |
| What the caller is told | `iam/errors.go` `errAccessDenied`; `iamguard.Check` (run) |

**Statements on the boards that depend on a finding.** These boards describe
what doze-aws does today, bugs included. Each sentence changes when its finding
closes; none ships as written if the finding is fixed.

| Board | Sentence | Changes with |
|---|---|---|
| Iam-13 | "Inactive is recorded, not enforced" | IAM-3 |
| Iam-13 | "doze-aws does not enforce a ceiling" on keys | IAM-9 |
| Iam-13, Iam-25 | Rename: "its access keys stay under the old name" | IAM-5 |
| Iam-19 | After a rename "its members stop inheriting the group's policies" | IAM-6 |
| Iam-20, Iam-25 | Profile delete: "doze-aws does not insist" | IAM-11 |
| Iam-09, 15, 16, 21, 22 | The trust policy is read by nothing; credentials are judged as the root; `GetCallerIdentity` answers `user/test` | IAM-1, IAM-2 |
| Iam-15, Iam-21 | "doze-aws does not compare" the duration with the role's maximum | IAM-2 |
| Iam-04, Iam-10 | The generated policy lists refused actions | IAM-7 |
| Iam-06 | A draft with no `Resource` simulates as allowed | IAM-13 |
| Iam-08 | `AWSServiceRoleForAutoscaling` | IAM-19 |

**Statements on the boards not yet backed.** Remove or prove each.

- Iam-Home, Iam-01, Iam-03: "Every request is recorded." False for
  CloudFormation and API Gateway (IAM-18).
- Iam-05, Iam-06, Iam-10: a conditioned statement "cannot match here and reads
  implicitDeny". True for positive operators only; a negated operator matches
  on an absent key (`conditions.go`).
- Iam-05: "Same engine as enforce mode." Not for a permissions boundary (IAM-14).
- Iam-22: "an expired set still works." Inferred from the code; not run.
- Iam-13: "AWS would refuse the call" (inactive key), "Two keys per user on AWS".
- Iam-15, Iam-21: "AWS refuses a longer DurationSeconds than the role allows."
- Iam-20, Iam-25: "AWS answers DeleteConflict" for a profile holding a role.
- Iam-21: "What they are good for" is advice, not a fact from the code.
- Iam-02: the middleware's denial is labelled `AccessDenied` for every
  protocol; only the JSON and S3 cases were run (IAM-24).

## 7. Tests to add

| Layer | Tests |
|---|---|
| Store, handler (`iam/`) | Rename to a taken name, and what follows a rename (IAM-4, 5, 6); each delete guard (IAM-11, 12); key limit and id shape (IAM-9, 10); alias (IAM-23); generator contents (IAM-7); recorder count (IAM-8); simulator against create, boundary, matched statements (IAM-13, 14, 15) |
| Enforcement (`iam/enforce_test.go`, root) | Inactive key (IAM-3); a role session judged as the role (IAM-1); the denial through each SDK and protocol (IAM-24) |
| STS (`sts/`) | Missing role, untrusted caller, duration above the role's maximum (IAM-2); `GetCallerIdentity` per caller kind |
| `internal/iamguard` | Context keys reach the guard (IAM-17); CloudFormation, API Gateway and Logs resolution (IAM-18); one resolve case per service |
| `internal/iampolicy` | `Version` (IAM-21); every operator, absent-key cases; size limits and `Sid` rules once established |
| SDK v2 | Paging on every list (IAM-16); service-linked roles (IAM-19, 20) |
| Rejection parity | Already complete from the model; add the hand-derived rules above with their `doc` page |
| Model coverage | `Unreached` empty in both packages (IAM-38); stub rows counted against the ledger (IAM-37) |
| Restart | Users, groups, roles, policies and versions, keys, profiles, alias; and the STS session decision |
| Conformance (boto3) | Every row of section 8; today's 7 scenarios cover CRUD and refusals only, with no enforcement scenario |
| Console Go | Draft matched-by (IAM-25); generate's principal list and scope (IAM-29); service-linked delete (IAM-31) |
| Browser | Every false line in IAM-26 to IAM-35; each `new` item; each of the 14 confirms' sentences; the three modes of the log; the one-time secret |
| Mutation | Baselines for `iam/`, `sts/`, `internal/iamguard`, `internal/iampolicy`; then survivors in `authorize.go`, `store.go`, `guard.go`, `conditions.go` |

## 8. Recording queue

Every claim here is `doc` or `memory` today.

| Claim | Source now | Scenario |
|---|---|---|
| Credentials from `AssumeRole` are judged as the role session; `GetCallerIdentity` returns the assumed-role ARN | doc | `test_sts::assumed_role_identity` |
| `AssumeRole` on a missing role, and on a role that does not trust the caller: code and message | doc | `assume_role_refusals` |
| Whether `aws:SourceIp` and `aws:UserAgent` are in the request context when a service principal makes the call (SNS delivering to a queue): doze-aws supplies loopback values on a peer hop | memory | `service_principal_condition_keys` |
| `DurationSeconds` above the role's `MaxSessionDuration` | doc | `assume_role_duration_over_max` |
| A call signed with an Inactive key: code | memory (`InvalidClientTokenId`) | `test_iam::inactive_key` |
| `UpdateUser` and `UpdateGroup` to a taken name | doc | `rename_conflict` |
| After a rename, keys, memberships and policies follow | memory | `rename_follows` |
| Third `CreateAccessKey`: `LimitExceeded` and its message | doc | `access_key_limit` |
| Access key id length and prefix | memory | `access_key_shape` |
| `DeleteInstanceProfile` holding a role | doc | `delete_profile_with_role` |
| `DeleteRole`, `DeleteUser`, `DeleteGroup`: each precondition and its message | doc | `delete_conflicts` |
| `DeleteRole` on a service-linked role | memory | `delete_slr_by_delete_role` |
| `CreateServiceLinkedRole`: an unknown service; the exact role name | memory | `slr_names` |
| Policy `Version` other than the two documented | memory | `policy_version` |
| `SimulateCustomPolicy` with a statement lacking `Resource` | memory | `simulate_no_resource` |
| `SimulatePrincipalPolicy` and a permissions boundary | doc | `simulate_boundary` |
| `MatchedStatements` when two statements match | doc | `simulate_matched` |
| `ReadOnlyAccess` and the other offered managed policies, action by action | memory | `managed_policy_documents` |
| Account alias: a second create; deleting another name | memory | `account_alias` |
| The `AccessDenied` message for explicit and implicit denies, with and without a resource, per protocol | doc | `denial_wording` |
| Document size limits; `Sid` rules; attachment limits; the 50-tag limit | doc | `policy_limits` |
| Paging on each list: `MaxItems`, `Marker`, `IsTruncated` | doc | `list_paging` |
| Absent key with a negated operator matches | doc | `condition_absent_key` |
| `GetAccountPasswordPolicy` with none set | doc | `password_policy_absent` |
| `CreateAccessKey` with no `UserName` | doc | `access_key_for_caller` |

## 9. Owed to integration

| What | With |
|---|---|
| A role session is honoured by every service's guard, identity and resource half (already row "IAM" in 90-integration) | all |
| The allow and deny matrix per service, for a user key, a role session and the root, under soft and enforce | all |
| A policy generated from a recorded run, attached to a fresh user, passes the same run under enforce and nothing more | all |
| Every supplied condition key reaches each service's resource-policy evaluation | S3, SQS, SNS, Lambda, KMS, Secrets Manager, Kinesis |
| `iam:PassRole` when a function, state machine or rule is given a role; the role exists | Lambda, Step Functions, EventBridge |
| A function's own SDK calls run as its execution role | Lambda |
| `AWS::IAM::*` resources, and a role created and used in one template | CloudFormation |
| CloudFormation and API Gateway requests evaluated, if IAM-18 decides so | CloudFormation, API Gateway |
| The denial's shape through each service's own protocol | S3, SNS, Lambda, API Gateway |
| "Used by" on a role names real functions and machines | Lambda, Step Functions |
| The console, which bypasses the middleware, still meets each service's resource-policy guard | all seven |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] IAM-1 to IAM-38 each closed, with the test that failed first named beside it.
- [ ] Decided and written into `SUPPORT.md`:
  - [ ] how STS reaches IAM (a peer, or a shared store) and what a session is (IAM-1, IAM-2);
  - [ ] whether web identity, SAML, federation and root sessions resolve to anything, and their tiers;
  - [ ] whether sessions survive a restart;
  - [ ] what a denied event contributes to a generated policy (IAM-7);
  - [ ] whether CloudFormation and API Gateway are guarded (IAM-18), and Logs' empty resource;
  - [ ] the unknown-key-is-root rule, as a listed difference;
  - [ ] the service-linked role service list and naming (IAM-19).
- [ ] Section 2.3 has no "not established" left: `Sid` and `Id` rules, policy
      variables, size limits, `BinaryEquals`, service condition keys,
      EventBridge's resource ARN, Logs' resource.
- [ ] Section 2.1 has no "not established" left: the tag limit, the summary
      map's keys, `GetAccountAuthorizationDetails`' `Filter` and paging.
- [ ] The condition-key table is true for both halves and matches `SUPPORT.md`,
      `static/policy.js` `CONTEXT_KEYS`, and the builder's datalist.
- [ ] `Unreached` is empty in both coverage tests; `SUPPORT.md` and
      `not-built.md` account for all 180 and all 11, and the stub rows total 85.
- [ ] Every box in section 5 ticked, or moved to the recording queue with its
      test written and skipped.
- [ ] All 26 boards implemented from the foundation's shell, and each `new`
      item reads only the data named for it.
- [ ] Every sentence in "depends on a finding" was rewritten when its finding
      closed, and the "not yet backed" list is empty.
- [ ] All 14 confirms say only what the code does.
- [ ] The simulator and enforcement agree on a table of cases that includes a
      boundary, a group policy and a conditioned statement, in one test.
- [ ] `e2e/tests/iam.spec.ts` covers every route under `/iam` with the route
      gate on, in off, soft and enforce.
- [ ] Conformance: a scenario for every row in section 8, including at least
      one enforcement scenario. Zero "answered differently". Unverified count: ____.
- [ ] Mutation baselines recorded for the four packages: ____.
- [ ] Idle heap unchanged after STS gains access to IAM's store, or the change
      is explained.
- [ ] Actual time recorded in section 1.

## 11. Files and order of work

| Order | Work | Files |
|---|---|---|
| 1 | Contract and decisions; ledger counts | `docs/SUPPORT.md`, `docs/not-built.md`, `iam/coverage_model_test.go`, `sts/coverage_model_test.go`, `iam/iam.go` (`stubActions`), `sts/sts.go` |
| 2 | Red tests for IAM-1 to IAM-24 | `iam/enforce_test.go`, `iam/sdk_test.go`, `sts/sdk_test.go`, `internal/iamguard/guard_test.go`, `resolve_test.go`, `internal/iampolicy/policy_test.go`, `resource_policy_integration_test.go` |
| 3 | Sessions: STS mints into IAM, `principalFor` resolves them, trust policy evaluated, inactive keys refused | `sts/sts.go`, `sts/actions.go`, `iam/authorize.go`, `iam/store.go`, `iam/keys.go`, `dozeaws.go`, `peers/` |
| 4 | Store integrity: rename, delete guards, key limit, alias | `iam/store.go`, `iam/actions.go`, `iam/policies.go`, `iam/keys.go` |
| 5 | Guard: context keys, resolution, the denial's shape | `internal/iamguard/guard.go`, `resolve.go`, `dozeaws.go` (`authorized`, `writeDenied`), `iam/errors.go` |
| 6 | Evaluator and simulator: `Version`, boundary, matched statements, strict draft parse | `internal/iampolicy/policy.go`, `iam/simulate.go`, `iam/policy.go` |
| 7 | Recorder and generator | `iam/authorize.go` |
| 8 | Paging, service-linked roles, managed policy documents | `iam/actions.go`, `iam/policies.go`, `iam/managed.go` |
| 9 | Section 5 sweep | the test files above |
| 10 | Conformance scenarios and the claims file | `conformance/tests/test_iam.py`, `test_sts.py`, the file from foundation B1 |
| 11 | Console | `internal/console/templates/iam.html`, `handlers_iam.go`, `client_iam.go`, `client_sts.go`, `client_lambda.go` (the `Role` field), `routes_stackwide.go`, `static/policy.js`, `copy.go` |
| 12 | Browser tests | `e2e/tests/iam.spec.ts` |
| 13 | Ledger, changelog, status | `docs/SUPPORT.md`, `CHANGELOG.md`, `plan/README.md` |

Step 3 comes before the rest because seven boards and four findings read
differently once a role session is real.

**What should change in the plan.** IAM-1 and IAM-2 are a design change, not a
fix: STS has to learn about IAM, sessions need a store and an expiry, and every
guard has to accept a role principal. That alone is M. With 24 API findings
behind it the stage is nearer two L than one. Two foundation items are missing
and IAM needs both: a second identity kind in the "run the suites" pass (B2
runs a second account, never a non-root caller), and the stale "default `off`"
rows named in stage 0's acceptance are four, in Kinesis, KMS, S3 and Secrets
Manager. IAM-17 changes behaviour in seven earlier stages; if it is fixed here,
each of those services' enforce tests is re-run.

**Prior art.** `SUPPORT.md` already records that LocalStack and moto default to
permissive and that moto vendors AWS's managed policies as a generated file,
which is the alternative to synthesizing them if IAM-22 shows the patterns are
too coarse. AWS's "Policy evaluation logic" page in the IAM user guide is the
source for the order in section 2.3, and the Service Authorization Reference is
the source for which action each operation is authorized as and which condition
keys each service supplies; each hand-derived rule's test names its page.
