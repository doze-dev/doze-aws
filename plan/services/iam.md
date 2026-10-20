# IAM and STS

Stage 15. Size L (*re-estimated 2026-10-11 at about 35 days, which is XL; see
section 1*). Follows [../01-protocol.md](../01-protocol.md); the common
acceptance criteria there apply in full, and this page adds IAM's and STS's own.
Section 12 is the pre-stage review: read it first.

## Foundation hand-offs (stage 0, 2026-10-20)

What the foundation (stage 0, package P6) left for this stage, so it is not lost, and what it did already. The shared items are in [../00-foundation.md](../00-foundation.md); each line here says what is done, what is owed, and the test that proves it.

| Item | IAM and STS: done, and what this stage does |
|---|---|
| B6 the model sweep | IAM: 85 legacystub and 4 unknown of 180 operations; STS: 0 legacystub and 2 unknown of 11. Move each stub to `awshttp.NotBuilt`; `Unreached` is still in `iam/coverage_model_test.go` and `sts/coverage_model_test.go` and empties here. |
| B8 generated cases | IAM: 702 committed, 1268 regenerated (7 only in the committed file, 573 only in the new one). STS: 111 committed, 141 regenerated (0 / 30). Regenerate both in this stage; the Query wire's list cases are built around B13 (every element of a list is validated). |
| B9 stored-policy validator | IAM validates what it stores itself (`own` in `resource_policy_wiring_test.go`); the shared validator's rules came from IAM's own create, so this stage decides whether it calls the shared function instead (IAM-21 `Version`). |
| B4 settings records | 2 hand-written bounded number inputs in `iam.html`. |
| A15 fragments | No region partials (the `iam_*` partials are answers to a call). Nothing is owed. |
| A2 the why line | Set `Local: true` (`awshttp.APIError`) on each refusal that is doze-aws's own limit and not AWS's, which this document's findings mark; nothing sets the stamp today except the console's own 503. Test: one such limit per service answers with `X-Doze-Local-Limit` and the page says "Refused here" (`internal/console/why_line_test.go` is the worked example). |
| B3 mutation | Baseline recorded in `testdata/mutation.json` (`iam`); this stage runs `task mutate:check -- iam` after its changes, and any survivor it adds has a test or a reason. STS: `task mutate:record -- sts` is recorded. |


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

**Estimate (re-made 2026-10-11, after the pre-stage review in section 12):**
about **35 focused days** (range 28 to 42; the rows below sum to 33 with the stubs left declined and 36 with them promoted), the largest stage in the plan. The
README's table says 11; the first version of this page said "L, five or more
days" and "nearer two L". Reading the models' own text and running the service
found 32 findings the document did not have (IAM-39 to IAM-70), two of them P0,
and the console comparison found that **none of the 26 boards matches its
page's shape**: the IAM console was built from the boards' words in the way SQS's
first pass was, and nothing was rendered beside it. Where the days go:

| Work | Days | Why |
|---|---|---|
| Contract, the 501 shape, decisions, ledger counts | 1 to 4 | The refusal shape is one day; promoting OIDC and SAML providers, the password policy, login profiles, the credential report and the deletion status to C or F (IAM-65) is three more, if the decision is yes |
| Store integrity | 4 | Rename, delete guards, the key limit and id, names that do not distinguish case, service-linked roles and their deletion task, the boundary on create, instance-profile tags, version dates |
| The wire | 6 | The Query list shape for both services (B13, then 180 regenerated cases), paging on 25 lists, the implicit user, the authorization export, the account summary, the encoding, and the simulator's inputs, result shape and codes |
| Sessions (STS reaches IAM) | 6 | IAM-1, 2, 3, 51, 53, 54, 70: a session store with expiry that survives a restart, a trust policy evaluated with its conditions, session policies, the restrictions each kind of session carries |
| The guard and the denial | 3 | Resolution of IAM and STS resources, the denial's envelope per protocol, CloudFormation and API Gateway action tables (D6) |
| Console, 26 boards in their shape | 10 | Two-column detail pages, the right-hand explanation panels, five teaching lines on about a dozen settings, new data (Last, Used by, scope, matched-by), 14 confirms, and the browser tests |
| Proof | 3 | At least 30 claims in `test_iam.py` and `test_sts.py`, four mutation baselines (an hour or more each), the ledger and the changelog |

**Actual:** _fill in when done._ SQS is the calibration: its estimate was two
days and its volume was several times that, for the same reason (limits and
shapes written from memory, boards built from words). This page was re-read
against the model and the running service before any of it was built, which is
what should keep the ratio down; it is not a promise that it does.

## 2. Contract

### 2.1 IAM

The model (`iam/testdata/ops_iam.json`) lists **180** operations. 91 are
dispatched (`iam/actions.go`, `handlers`), 85 are refused by name
(`iam/iam.go`, `stubActions`, `UnsupportedOperationException`), and 4 reach
neither table and answer `InvalidAction`. Two more dispatched actions are doze
extensions and are not in the model.

| Operations | Count | Now | Target | Note |
|---|---|---|---|---|
| CreateUser, GetUser, UpdateUser, DeleteUser, ListUsers | 5 | F | F | Rename: IAM-4, IAM-5. Delete rules: IAM-12. Paging: IAM-16. *Added 2026-10-11:* `PermissionsBoundary` at create is dropped and never returned (IAM-40); `GetUser` with no `UserName` answers a fixed root, not the caller (IAM-44); names do not distinguish case on AWS (IAM-45); `ListUsers` returns `Tags`, which the model says it does not (IAM-60). |
| TagUser, UntagUser, ListUserTags | 3 | F | F | *Corrected 2026-10-11: was "The 50-tag limit is not established." Model: every `Tags` member and `TagKeys` is a list of 0 to 50 (`tagListType`), a key is 1 to 128 and a value 0 to 256; "If any one of the tags is invalid or if you exceed the allowed maximum number of tags, then the entire request fails" and `LimitExceeded` is on the operation's error list. The per-resource maximum is not in the model (`doc`: the IAM and STS quotas page the model names). Today 51 tags in one request are accepted (IAM-41), and nothing counts a resource's tags.* |
| CreateGroup, GetGroup, UpdateGroup, DeleteGroup, ListGroups | 5 | F | F | Rename: IAM-4, IAM-6. |
| AddUserToGroup, RemoveUserFromGroup, ListGroupsForUser | 3 | F | F | |
| CreateRole, GetRole, UpdateRole, UpdateRoleDescription, DeleteRole, ListRoles | 6 | F | F | `MaxSessionDuration` is stored and read by nothing (per-setting table). *Model, 2026-10-11:* it "determines the maximum duration that can be requested using the `DurationSeconds` parameter" (IAM-2 reads it); `PermissionsBoundary` at create is dropped (IAM-40); `ListRoles` "does not return PermissionsBoundary, RoleLastUsed, Tags" and this one returns tags (IAM-60); an empty `Description` cannot clear one (IAM-61). |
| UpdateAssumeRolePolicy | 1 | F | F | Validated and stored. Consulted by nothing today (IAM-2). |
| TagRole, UntagRole, ListRoleTags | 3 | F | F | |
| CreateServiceLinkedRole, DeleteServiceLinkedRole | 2 | F | F | IAM-19, IAM-20, IAM-50. |
| CreatePolicy, GetPolicy, DeletePolicy, ListPolicies | 4 | F | F | |
| CreatePolicyVersion, GetPolicyVersion, DeletePolicyVersion, ListPolicyVersions, SetDefaultPolicyVersion | 5 | F | F | Five-version ceiling and default-version guard hold (run). |
| TagPolicy, UntagPolicy, ListPolicyTags | 3 | F | F | |
| Attach/Detach {User, Group, Role}Policy | 6 | F | F | |
| ListAttached{User, Group, Role}Policies, ListEntitiesForPolicy | 4 | F | F | |
| Put/Get/Delete/List {User, Group, Role}Policy | 12 | F | F | Put replaces a same-named policy, as AWS's Put does. |
| Put/Delete {User, Role}PermissionsBoundary | 4 | F | F | Applied at request time, not in the simulator (IAM-14). *Added 2026-10-11:* `GetUser` and `GetRole` never return the boundary after a Put, and `CreateUser`/`CreateRole` drop theirs (IAM-40). |
| CreateAccessKey, ListAccessKeys, UpdateAccessKey, DeleteAccessKey | 4 | F | F | IAM-3, IAM-9, IAM-10. *Added 2026-10-11:* with no `UserName` the model says the user is "determined implicitly based on the access key ID signing the request"; today `CreateAccessKey` refuses and `ListAccessKeys` answers `NoSuchEntity` (IAM-44). |
| GetAccessKeyLastUsed | 1 | F | F | Real: the middleware writes it. |
| CreateInstanceProfile, GetInstanceProfile, DeleteInstanceProfile, ListInstanceProfiles | 4 | F | F | IAM-11. *Added:* `Tags` at create are dropped and never returned (IAM-47); `PathPrefix` is ignored (IAM-46). |
| AddRoleToInstanceProfile, RemoveRoleFromInstanceProfile, ListInstanceProfilesForRole | 3 | F | F | One role per profile holds (run). |
| TagInstanceProfile, UntagInstanceProfile, ListInstanceProfileTags | 3 | F | F | |
| CreateAccountAlias, DeleteAccountAlias, ListAccountAliases | 3 | F | F | IAM-23. |
| GetAccountSummary | 1 | F | F | *Corrected 2026-10-11: was "Which keys of the summary map are filled is not established."* The model's key enum has 33 (`Users`, `UsersQuota`, `Groups`, `GroupsQuota`, `ServerCertificates`, `ServerCertificatesQuota`, `UserPolicySizeQuota`, `GroupPolicySizeQuota`, `GroupsPerUserQuota`, `SigningCertificatesPerUserQuota`, `AccessKeysPerUserQuota`, `MFADevices`, `MFADevicesInUse`, `AccountMFAEnabled`, `AccountAccessKeysPresent`, `AccountPasswordPresent`, `AccountSigningCertificatesPresent`, `AttachedPoliciesPerGroupQuota`, `AttachedPoliciesPerRoleQuota`, `AttachedPoliciesPerUserQuota`, `Policies`, `PoliciesQuota`, `PolicySizeQuota`, `PolicyVersionsInUse`, `PolicyVersionsInUseQuota`, `VersionsPerPolicyQuota`, `GlobalEndpointTokenVersion`, `AssumeRolePolicySizeQuota`, `InstanceProfiles`, `InstanceProfilesQuota`, `Providers`, `RolePolicySizeQuota`, `Roles`, `RolesQuota`); the code fills five (`Users`, `Groups`, `Roles`, `Policies`, `AccountMFAEnabled`) (IAM-57). |
| GetAccountAuthorizationDetails | 1 | F | F | *Corrected 2026-10-11: was "`Filter` and paging are not established."* Model: `Filter` is a list of `User`, `Role`, `Group`, `LocalManagedPolicy`, `AWSManagedPolicy`; `MaxItems` 1 to 1000, default 100; `Marker` 1 to 320; the response has `UserDetailList`, `GroupDetailList`, `RoleDetailList` and `Policies`. The code returns users and roles only and ignores all three parameters (IAM-43). |
| GetAccountPasswordPolicy | 1 | F | F | Always `NoSuchEntity`: nothing can set one. *Model:* `NoSuchEntity` is on its error list, so the answer is right; the setter is a stub (IAM-65). |
| SimulateCustomPolicy, SimulatePrincipalPolicy | 2 | F | F | IAM-13, IAM-14, IAM-15. *Added:* bad documents answer a code the model does not list for them, and six inputs are accepted and ignored (IAM-48); policy variables are never substituted (IAM-49). |
| GetContextKeysForCustomPolicy, GetContextKeysForPrincipalPolicy | 2 | F | F | |
| MFA devices and their tags | 11 | S | S | "there is no MFA fleet locally" |
| SAML and OIDC providers, their tags, outbound federation | 21 | S | S, **decision IAM-65** | The model describes each provider as a record (OIDC: `Url`, `ClientIDList`, `ThumbprintList`, `Tags`; SAML: metadata document, name, `Tags`) with no behaviour beyond storage; 10 of the 21 are OIDC and 8 SAML. Terraform's EKS and GitHub Actions modules create an OIDC provider. |
| Server, signing and SSH certificates and their tags | 17 | S | S | |
| Login profiles, ChangePassword, Update/DeleteAccountPasswordPolicy | 7 | S | S, **decision IAM-65** | The password policy and a user's login profile are records with ranges the model states (`MinimumPasswordLength` 6 to 128, `MaxPasswordAge` 1 to 1095, `PasswordReusePrevention` 1 to 24, defaults in the member text). CIS-style baseline modules set the policy. |
| Credential report, service-last-accessed (3), ListPoliciesGrantingServiceAccess | 6 | S | S; the credential report **decision IAM-65 and L7** | `GenerateCredentialReport` returns `STARTED`, `INPROGRESS` or `COMPLETE`; `GetCredentialReport` answers `ReportInProgress` (404), `ReportNotPresent` (410) or `ReportExpired` (410) until one exists. The three service-last-accessed jobs are `IN_PROGRESS`, `COMPLETED`, `FAILED` and "recent activity usually appears within four hours": derived from CloudTrail, stay S. |
| Organizations features, access reports, delegation | 15 | S | S | |
| Service-specific credentials | 5 | S | S | |
| SetSecurityTokenServicePreferences, GetServiceLinkedRoleDeletionStatus, GetHumanReadableSummary | 3 | S | S; `GetServiceLinkedRoleDeletionStatus` **F (IAM-50)** | Not in any ledger row today (IAM-37). The deletion status is the second half of `DeleteServiceLinkedRole`, which is F. |
| AcquireRole, GetAccountProperties, GetRoleTemplateVersion, PutAccountProperties | 4 | absent (`InvalidAction`) | S | Listed in `iam/coverage_model_test.go` as `Unreached`. The protocol allows nothing absent (IAM-38). |
| DozeAccessLog, DozeGeneratePolicy | 2 | extension | extension | Not AWS operations. IAM-7, IAM-8. |

91 + 85 + 4 = 180. The ledger's seven stub groups add up to 70, not 85.

*Verified 2026-10-11:* `handlers` has 93 entries (91 AWS operations and the two
extensions), `stubActions` 85, and `testdata/modelsweep.json` records 85
`legacystub` and 4 `unknown` for IAM and 2 `unknown` for STS. The model has 180
and 11. The 85 **answer 400 `UnsupportedOperationException`, not the 501
`NotImplemented` with `X-Doze-Not-Built` that decision D5 and protocol step 1
require** (IAM-39); the groups above are by operation family, not by what the
caller sees. The model also carries operations that did not exist when this
service was written: delegation requests (8), role templates (`AcquireRole`,
`GetRoleTemplateVersion`), account properties, outbound web-identity federation
(3) and `ListOrganizationsFeatures`.

### 2.2 STS

The model (`sts/testdata/ops_sts.json`) lists **11** operations. Nine are
dispatched (`sts/sts.go`, `handlers`).

| Operation | Now | Target | Note |
|---|---|---|---|
| GetCallerIdentity | F | F | Answers `user/test` for every caller, a user's key included. Target: the caller the key resolves to (IAM-1). *Model:* "No permissions are required to perform this operation. If an administrator attaches a policy to your identity that explicitly denies access to the sts:GetCallerIdentity action, you can still perform this operation." Under enforce it is evaluated and denied (IAM-51). |
| AssumeRole | F | F | Mints for any well-formed ARN. Target: the role exists and its trust policy admits the caller (IAM-2), and the credentials resolve to the role (IAM-1). *Added 2026-10-11 from the model:* `ExternalId` 2 to 1224, `SerialNumber`/`TokenCode` (MFA) and `SourceIdentity` (2 to 64, not `aws:`-prefixed, echoed in the response), `Policy` (JSON, and a malformed one is `MalformedPolicyDocument`), up to 10 `PolicyArns` that "must exist in the same account as the role", up to 50 `Tags` and `TransitiveTagKeys`, `MinimumSessionTokenSize` 0 to 4096, `DurationSeconds` 900 to 43 200 and "up to the maximum session duration set for the role", and a role-chained session "to a maximum of one hour". None of it is read (IAM-53, IAM-54, IAM-70). |
| AssumeRoleWithWebIdentity | F | F or C | Token unverified, claims echoed. Whether the role must exist is a decision (section 10). *Model errors:* `ExpiredTokenException` (400), `InvalidIdentityToken` (400), `IDPRejectedClaim` (403), `IDPCommunicationError` (400); none is produced and a token that is not a JWT is accepted. "Calling AssumeRoleWithWebIdentity does not require the use of AWS security credentials." |
| AssumeRoleWithSAML | F | F or C | Same. The response members come from the assertion (`Audience` is its `Recipient`, `NameQualifier` is `BASE64(SHA1(issuer + account + "/" + provider))`); the code hard-codes the first and randomises the second (IAM-56). |
| AssumeRoot | F | C | Mints. Nothing treats the result differently from any other key. *Model:* `TaskPolicyArn` required, `DurationSeconds` 0 to 900 (default 900), the target is "a member account principal ARN or account ID" 12 to 2048 characters, and the caller must be an Organizations management or delegated-administrator account (`RegionDisabledException` and `ExpiredTokenException` are its only modelled errors). |
| GetSessionToken | F | F | Target: the session resolves to the calling user. *Model:* "No permissions are required for users to perform this operation"; `DurationSeconds` 900 to 129 600, default 43 200, and for the account root "restricted to a maximum of 3,600 seconds"; "If the duration is longer than one hour, the session for AWS account owners defaults to one hour" (so not an error; IAM-63). Denied under enforce today (IAM-51). |
| GetFederationToken | F | C or F | Whether a federated session is modelled is a decision. *Model:* `Name` 2 to 32; no policy means "the resulting federated user session has no permissions"; the credentials "cannot call any IAM operations" and "any STS operations except GetCallerIdentity" (IAM-70). |
| GetAccessKeyInfo | F | F | Every key maps to the one account. *Model:* "This operation does not indicate the state of the access key"; `AKIA` is long-term, `ASIA` temporary; `AccessKeyId` 16 to 128, `\w` only (enforced). A deleted key "might return an error that the key doesn't exist": not established. |
| DecodeAuthorizationMessage | S | S | In `not-built.md`. *Found 2026-10-11:* it is dispatched and answers 400 `InvalidAuthorizationMessageException`, a modelled code that means "the message was invalid or has expired", so a caller catching it reads "your message is bad" (IAM-39). |
| GetDelegatedAccessToken, GetWebIdentityToken | absent (`InvalidAction`) | S | `Unreached` in `sts/coverage_model_test.go` (IAM-38). *Verified:* both answer 400 `InvalidAction` today. |

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
| EventBridge | yes | `Name`, `EventBusName` | no | *Corrected 2026-10-11: was "F; which ARN `Name` becomes per operation is not established".* Read in `resolve.go`: both fields become `arn:...:events:...:rule/<value>`, so a bus-level call (`DescribeEventBus`, `PutPermission`, `CreateEventBus`: `Name` is the bus) is authorised on a **rule** ARN, and `PutEvents`, whose bus is in `Entries[].EventBusName`, on none (IAM-52). |
| CloudWatch | yes | first alarm of a batch; none for metrics | n/a | F, as documented |
| CloudWatch Logs | yes | none: always empty | no | *Read 2026-10-11: no `logs` entry in `resourceRules`, so every Logs request is authorised against `Resource: "*"` only. Intended or not is a decision for the Logs stage's resolver; the ARN forms are `arn:aws:logs:<region>:<account>:log-group:<name>` and `...:log-group:<name>:log-stream:<name>` (memory).* |
| Step Functions | yes | machine, activity, execution | n/a | F |
| IAM, STS | yes | none | n/a | *Corrected 2026-10-11: was "F".* Resolved to no resource, so a policy that allows `sts:AssumeRole` on a role's ARN (the model's own example: "a policy that allows the user to call AssumeRole for the ARN of the role in the other account") or `iam:CreateRole` on `role/app-*` never matches. Run under enforce: both denied (IAM-52). |
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
| Policy `Version` | not checked | `2020-01-01` is accepted (IAM-21). *Read 2026-10-11:* identity and trust policies go through `iampolicy.Parse`, which does not look at `Version`; a **resource** policy goes through `ValidateResourcePolicy`, which does (`2012-10-17`, `2008-10-17`; foundation B9). `memory` for the two values. |
| Policy `Id`, statement `Sid` uniqueness | not established | *Read:* neither is checked; `Id` and `Sid` only label a matched statement. Whether a duplicate `Sid` is refused is `memory`. |
| Policy variables (`${aws:username}`) | not established | *Corrected 2026-10-11: was "not established".* **Not implemented**: no substitution anywhere in `iampolicy`; `Resource: arn:aws:s3:::home/${aws:username}/*` simulated for `dave` against `.../home/dave/x` with `aws:username=dave` supplied answers `implicitDeny` (run). IAM-49. |
| Session policies, `PolicyArns` on AssumeRole | accepted by validation, ignored | Target C, said so. *Corrected:* not even validated. A malformed `Policy` and a `PolicyArns` entry that names no policy are both accepted and answered 200 (run); the model lists `MalformedPolicyDocument` on four STS operations (IAM-53). |
| Trust policy | validated, stored | Evaluated by nothing (IAM-2). |
| Size limits (6 144 managed, 2 048 user inline, 10 240 role inline) | not established | Model length is 131 072. *Corrected 2026-10-11: the model's text says only "the maximum length ... is listed below" and points to the "IAM and STS character quotas" page; none of the three numbers is in it. They stay `memory` (and the group inline number, 5 120, is `memory` too and was missing).* The AssumeRole `Policy` has no length in the model (`unrestrictedSessionPolicyDocumentType`, minimum 1) where SAML, web identity and federation have 2 048: the plaintext rule is prose. |

**Condition operators.** Implemented: `StringEquals`, `StringNotEquals`,
`StringEqualsIgnoreCase`, `StringNotEqualsIgnoreCase`, `StringLike`,
`StringNotLike`, the six `Numeric*`, the six `Date*`, `Bool`, `IpAddress`,
`NotIpAddress`, `ArnEquals`, `ArnNotEquals`, `ArnLike`, `ArnNotLike`, `Null`,
the `...IfExists` suffix, `ForAllValues:` and `ForAnyValue:`. An absent key
satisfies a negated operator. An unknown operator never matches. `BinaryEquals`
is not implemented; whether that is intended is not established. *Read
2026-10-11: it is absent from `applyOperator`, so a statement using it never
matches, with no refusal at write time; AWS defines it (`memory`). Folded into
IAM-49 with the policy variables, since both are grammar the evaluator lacks.*

**Condition keys supplied.**

| Key | Identity policies (`iam/authorize.go`, `contextFor`) | Resource policies (`iamguard/guard.go`, `Check`) | Simulator |
|---|---|---|---|
| `aws:PrincipalArn`, `aws:PrincipalAccount` | yes | yes | no |
| `aws:RequestedRegion`, `aws:SecureTransport`, `aws:SourceIp`, `aws:UserAgent`, `aws:username` | yes | *Corrected 2026-10-11: was **no** (IAM-17). Now **yes**: foundation B10 (a27f866, c5d7e33) builds the context once in `iamguard.RequestContext` and both halves call it; `aws:SecureTransport` is always `"true"` (decision D7); `aws:username` is a user's name and absent for a role or the root.* | no |
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
| Package tests, `iam/` | 41 tests in 10 files (*Corrected 2026-10-11: was 40 in 9; `secure_transport_test.go` landed with B10*): SDK v2 lifecycle (12), the three modes (6), resource policies (4 + 5 + 4), spoofing (4), conformance fixes (2), rejection parity (2), model coverage (1), secure transport (1) |
| Package tests, `sts/` | 14 tests in 5 files: SDK v2 (7 + 1), SDK v1 (4), rejection parity (1), model coverage (1) |
| `internal/iamguard` | 27 tests (*was 25; `context_test.go`, 2, is B10's*): guard (4), resolve (8), reauthorize (6), chunked peek (4), edges (3), context (2) |
| `internal/iampolicy` | 25 tests (*was 24; `validate_test.go`, 1, is B9's*): policy (10), conditions (5), evaluate (4), principal (3), public (2), validate (1) |
| Root package | `iam_rest_actions_test.go` (1), `resource_policy_integration_test.go` (2), `identity_test.go` (3) |
| Console Go tests | `iam_mutations_test.go` (3), `access_policy_test.go` (6), `policy_editor_test.go` (2) |
| Parity | Ledger: 702 of 702 IAM model constraints across 89 of 93 dispatched operations; 108 of 108 for STS across 7 of 8; `knownGaps` empty in both. *Corrected 2026-10-11: "702 of 702" is the count of the tables as generated before foundation B8. `dzaudit` now also reads a list's length and a map's key rule, finds 32 more constraints in the IAM model and 6 in STS that are in no committed case (`dzaudit summary iam`: 1 003 constrained members over 164 operations, 22 list or map bounds with no case), and the committed `validate.go` has no list-length entry at all: `TagUser` has `Tags[].Key` and no bound on `Tags`. Each service regenerates `cases_*.json` in its stage (IAM-41).* |
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

*Re-run 2026-10-11 against a binary built from the working tree, with the AWS
CLI v1 (botocore 1.43.62) as the client: IAM-1, 2, 3, 8, 9, 11, 12 and 24 are
reproduced as written (IAM-12 wider than written); IAM-17 is closed in code by
foundation B10. Rows IAM-39 to IAM-70 were found in that review, by reading
every operation's and member's documentation in `.audit-models/iam.json` and
`sts.json` and then running what the reading raised. Sources in the older rows
that said `doc` or `memory` and are in the model's text are corrected in place,
marked "Corrected 2026-10-11".*

| Id | Sev | What | Where | Evidence | AWS source | Test to write first |
|---|---|---|---|---|---|---|
| IAM-1 | P0 | Credentials minted by STS are not honoured. Only a user's access key resolves to a principal; an `ASIA` key is unknown, so the call is the account root. Under enforce a role with no policies listed queues, and `GetCallerIdentity` answered `user/test`. An app tested "as its role" is tested as root, and the missing permission surfaces at deploy. | `iam/authorize.go` `principalFor`; `sts/actions.go` `mintCredentials`, `getCallerIdentity` | run (enforce) | doc: STS API reference, AssumeRole and GetCallerIdentity | `iam/enforce_test.go`: assume a role with no policies, sign with the result, expect `AccessDenied`; `sts/sdk_test.go`: `GetCallerIdentity` returns the assumed-role ARN |
| IAM-2 | P0 | `AssumeRole` reads neither the role nor its trust policy. It mints for a role that does not exist, and for one whose trust policy names only `lambda.amazonaws.com`. | `sts/actions.go` `assumeRole`; `sts/sts.go` (no store, no peer) | run | doc: AssumeRole errors; `AccessDenied` for an untrusted caller | SDK test: a missing role, and a role that does not trust the caller, both refused |
| IAM-3 | P0 | A deactivated access key still authenticates and is judged by its user's policies. The comment above the lookup says "An inactive key authorizes nothing"; no code reads `Status`. | `iam/authorize.go` `principalFor` | run (enforce; re-run 2026-10-11: an Inactive key's `ListQueues` is judged by the user's policies, `AccessDenied`) | *Corrected: was `doc`.* **model**: UpdateAccessKey.Status, "Active means that the key can be used for programmatic calls to Amazon Web Services, while Inactive means that the key cannot be used". The refusal's code is `memory` (`InvalidClientTokenId`); no code is modelled | `enforce_test.go`: deactivate, call, expect a refusal |
| IAM-4 | P0 | Renaming a user or a group onto a name already in use overwrites the other record without a word. The overwritten user's policies and keys are gone. | `iam/store.go` `UpdateUser`, `UpdateGroup`; `iam/actions.go` `hUpdateUser`, `hUpdateGroup` | run | *Corrected: was `doc`.* **model**: `UpdateUser` and `UpdateGroup` list `EntityAlreadyExists` (409), and `NewUserName`/`NewGroupName`: "Names are not distinguished by case" (IAM-45 widens the rule). Direction: more forgiving | SDK test: rename `a` to an existing `b`, expect `EntityAlreadyExists` and both intact; rename `a` to `A` |
| IAM-5 | P0 | Renaming a user leaves its access keys under the old name. `ListAccessKeys` for the new name is empty and a request signed with the key becomes the root. | `iam/store.go` `UpdateUser` (keys live in `keyBucket` by `UserName`) | run | memory: keys follow the user (*2026-10-11: the model's `UpdateUser` text says only "You should understand the implications of changing an IAM user's path or name" and points at a User Guide page; not read*) | SDK test: rename, list keys, call under enforce as the user |
| IAM-6 | P0 | Renaming a group leaves each member's membership under the old name. `ListGroupsForUser` is empty and the group's policies stop applying to its members (a simulated action went from allowed to implicitDeny). | `iam/store.go` `UpdateGroup`; `PoliciesFor` reads `userRecord.Groups` by name | run | memory: membership follows | SDK test: rename a group, `ListGroupsForUser`, simulate an action its policy allows |
| IAM-7 | P1 | The generated policy grants what was refused. Every recorded action is written as Allow, including an explicit Deny (`sqs:DeleteQueue`) and an implicit one (`s3:PutObject`), plus the root's own calls and `iam:DozeAccessLog` when no principal is named. | `iam/authorize.go` `hDozeGeneratePolicy` | run (enforce and soft) | n/a (ours) | Handler test: record one allow and one explicit deny; decide (section 10) and assert the document |
| IAM-8 | P1 | The access log counts a request to any of the seven resource-policy services twice: the middleware records the identity verdict and then the service's verdict under the same key. Two sends showed Count 4. | `iam/authorize.go` `evaluate`, `recorder.record`; `dozeaws.go` `authorized` (`RecordResource`) | run | n/a | Root test: one `SendMessage`, expect Count 1 |
| IAM-9 | P1 | No limit on access keys per user. A third key was created. `SUPPORT.md` says "a user is capped at two access keys". | `iam/store.go` `CreateAccessKey` | run (re-run 2026-10-11: a third key is created) | doc: IAM quotas, two access keys per user. *Model, 2026-10-11:* `LimitExceeded` (409) is on `CreateAccessKey`'s error list and `ListAccessKeys` says "each user is limited to a small number of keys"; the number two is not in the model, so it stays `doc` (the quotas page) until recorded | SDK test: third `CreateAccessKey` refused |
| IAM-10 | ~~P1~~ **P3** (*downgraded 2026-10-11*) | Access key ids are 21 characters (`AKIA` plus 17). STS's `ASIA` keys are `ASIA` plus 16, so doze's own two kinds disagree. | `iam/store.go` `uniqueID`, `CreateAccessKey` | run | memory: 20. *Model: `accessKeyIdType` is 16 to 128 characters of `\w`; 21 satisfies it, so nothing the model states is broken and no SDK reads the length. A tool that matches `AKIA[0-9A-Z]{16}` as a prefix still matches.* | Unit test on the length once recorded |
| IAM-11 | P1 | `DeleteInstanceProfile` succeeds while the profile holds a role. | `iam/store.go` `DeleteInstanceProfile` | run | *Corrected: was `doc`.* **model**: DeleteInstanceProfile, "The instance profile must not have an associated role", and `DeleteConflict` (409) is on its error list. Direction: more forgiving | SDK test: delete with a role, expect `DeleteConflict` |
| IAM-12 | P1 | Delete guards are partial. `DeleteRole` and `DeleteGroup` check attached policies only; `DeleteUser` checks attached policies and keys. None checks inline policies, group membership, or a role's instance profiles, so a deleted role stays named in a profile and a deleted user in a group. *Widened 2026-10-11:* `DeletePolicy` checks only the attachment count; the model also requires every non-default version to be deleted first (run: a policy with v1 and v2 deleted). *Corrected:* for a **group** the model names members and attached policies, not inline policies ("The group must not contain any users or have any attached policies"); an inline policy on a group is `memory`. What the model lists per operation: `DeleteUser` (password, access keys, signing certificate, SSH key, service-specific credentials, MFA device, inline policies, attached managed policies, group memberships), `DeleteRole` (inline policies, attached managed policies, instance profiles), `DeleteGroup` (users, attached policies), `DeletePolicy` (every attachment and every non-default version). | `iam/store.go` `DeleteRole`, `DeleteGroup`, `DeleteUser`, `DeletePolicy` | read; run 2026-10-11 for all four (a user with an inline policy, a group with a member, a role with an inline policy and in a profile, a policy with a second version: each deleted with 200) | *Corrected: was `doc`.* **model**: the operation documentation above, and `DeleteConflict` (409) on each. Direction: more forgiving | SDK tests: delete a role in a profile; a role with an inline policy; a user in a group; a user with an inline policy; a group with a member; a policy with a second version |
| IAM-13 | P1 | `SimulateCustomPolicy` accepts a statement with no `Resource` and answers allowed; `CreatePolicy` refuses the same document. The simulator parses with `parsePolicy`, not `parseIdentityPolicy`. | `iam/simulate.go` `hSimulateCustomPolicy`, `parseInputList` | run | memory. *2026-10-11: the model says only that each `PolicyInputList` member is "the complete, valid JSON text of an IAM policy"; it does not say a `Resource` is required. The model's error list for the simulators is `InvalidInput` and `PolicyEvaluation` only, so a refusal here is not `MalformedPolicyDocument` (IAM-48).* | Handler test: the same document through both |
| IAM-14 | P1 | `SimulatePrincipalPolicy` does not apply a permissions boundary; enforcement does. `SUPPORT.md` says the two "cannot disagree". | `iam/simulate.go` `hSimulatePrincipalPolicy`; `iam/authorize.go` `evaluate` | read | *Corrected: was `doc`.* **model**: SimulatePrincipalPolicy.PermissionsBoundaryPolicyInputList, "An IAM entity can only have one permissions boundary in effect at a time. For example, if a permissions boundary is attached to an entity and you pass in a different permissions boundary policy using this parameter, then the new permissions boundary policy is used for the simulation", and `EvaluationResult.PermissionsBoundaryDecisionDetail` (`AllowedByPermissionsBoundary`). Direction: more forgiving | Handler test: a user with a boundary that excludes the action |
| IAM-15 | P1 | The simulator returns one matched statement, labelled by `Sid` or `<policy>[i]`, with no `SourcePolicyType`, positions or `MissingContextValues`. AWS returns every matching statement. *Widened 2026-10-11 from the model's current text:* "The simulator now returns a single EvaluationResult per action, regardless of how many resource ARNs are provided. Previously, simulating one action against N resources returned N evaluation results"; the top-level `EvalDecision`, `MatchedStatements` and `MissingContextValues` are the aggregate ("the most restrictive decision across all resources") and each resource's own verdict is in `ResourceSpecificResults` (`EvalResourceName`, `EvalResourceDecision`, `MatchedStatements`, `MissingContextValues`); `EvalResourceName` at the top level is "the ARN template for the simulated resource type ... or `*`", not the caller's ARN. The code returns one result per (action, resource) pair (run: 2 actions by 2 resources gave 4 results) and no `ResourceSpecificResults`. Direction: wrong shape. | `iam/simulate.go` `simulate`; `iampolicy.Evaluate` | read; run 2026-10-11 | *Corrected: was `doc`.* **model**: `EvaluationResult`, `ResourceSpecificResult`, `Statement` (`SourcePolicyId`, `SourcePolicyType`, `StartPosition`, `EndPosition`) | SDK test: two matching Allows, assert both come back; two actions by two resources, assert two results each with two `ResourceSpecificResults` |
| IAM-16 | P1 | Every IAM list ignores `MaxItems` and `Marker` and answers `IsTruncated=false`. The values are range-checked by the model table and then never read. | `iam/actions.go`, `policies.go`, `keys.go` (the `hList*` handlers) | read; run 2026-10-11 (`--page-size 1` is the only way the CLI sends `MaxItems`; the server answered all items, `IsTruncated` false) | *Corrected: was `model` and `doc`.* **model**: `MaxItems` 1 to 1000, "If you do not include this parameter, the number of items defaults to 100", "IAM might return fewer results, even when there are more results available. In that case, the `IsTruncated` response element returns true"; `Marker` 1 to 320 characters. 39 operations take `MaxItems` and `Marker`, 25 of them dispatched (`GetGroup` and `GetAccountAuthorizationDetails` among them, and both simulators); `ListOpenIDConnectProviders` and `ListSAMLProviders` take neither. Direction: more forgiving (everything on page one, and no default of 100). Foundation B11's `internal/awspage.Slice` is the helper | SDK test: `ListUsers` with `MaxItems: 1` over three users, then the paginator |
| IAM-17 | P1 | **Closed 2026-10-10 by foundation B10** (a27f866, c5d7e33; `internal/iamguard/context.go`, `context_test.go`, `iam/secure_transport_test.go`): one `RequestContext` builds the seven keys for both halves, and by decision D7 `aws:SecureTransport` is always `"true"`, so a "deny plain HTTP" policy never fires locally. The document was written before it landed and was not updated. *Was:* A resource policy is evaluated with four context keys only: `aws:PrincipalArn`, `aws:PrincipalAccount`, `aws:SourceAccount`, `aws:SourceArn`. `aws:SecureTransport`, `aws:SourceIp`, `aws:RequestedRegion`, `aws:UserAgent` and `aws:username` reach identity policies and not the guard, so a "deny plain HTTP" bucket policy never denies. `SUPPORT.md` lists all seven as supplied. | `internal/iamguard/guard.go` `Check` | read here; run in the S3 grounding (findings-overview 11) | doc | `guard_test.go`: a Deny on `aws:SecureTransport=false` over plain HTTP |
| IAM-18 | P1 | CloudFormation and API Gateway are missing from `actionPrefixes`, so no request to either is evaluated or recorded, in any mode. A policy on `cloudformation:*` can never matter, and the builder's autocomplete offers both prefixes. Soft mode's "every request is recorded" is false for them. | `internal/iamguard/resolve.go` `actionPrefixes`; `internal/console/handlers_iam.go` `seenActions` | read | n/a | `resolve_test.go`: a request to each resolves an action, or the ledger and the builder say it does not |
| IAM-19 | P1 | `CreateServiceLinkedRole` accepts any string as a service (`bogus` made `AWSServiceRoleForBogus`), and names the role by capitalising the first letter only (`AWSServiceRoleForAutoscaling`). | `iam/actions.go` `hCreateServiceLinkedRole` | run (re-run 2026-10-11) | memory: AWS refuses a service with no linked role, and spells it `AutoScaling`. *Model, 2026-10-11:* `CreateServiceLinkedRole`'s errors are `InvalidInput` (400), `LimitExceeded` (409), `NoSuchEntity` (404) and `ServiceFailure` (500) only; a repeat "fails with a duplicate role name error" and the code returns `EntityAlreadyExists` (409), which that operation does not list, so one of the four is the answer; `AWSServiceName` is "the service principal ... Service principals are unique and case-sensitive" (example `elasticbeanstalk.amazonaws.com`); `CustomSuffix` "is combined with the service-provided prefix". The role name's exact spelling and the separator are not in the model | SDK test, skipped until recorded; the duplicate's code is a test now |
| IAM-20 | P1 | `DeleteRole` deletes a service-linked role. *Widened 2026-10-11:* so does every other write the model protects: run, on a service-linked role, `PutRolePolicy`, `UpdateAssumeRolePolicy`, `UpdateRole` and `DeleteRole` all answered 200. The model lists `UnmodifiableEntity` (400) on `DeleteRole`, `AttachRolePolicy`, `DetachRolePolicy`, `PutRolePolicy`, `DeleteRolePolicy`, `UpdateAssumeRolePolicy`, `UpdateRole`, `UpdateRoleDescription`, `PutRolePermissionsBoundary`, `DeleteRolePermissionsBoundary` and `RemoveRoleFromInstanceProfile`; "You cannot set the boundary for a service-linked role"; `TagRole` is allowed ("The role can be a regular role or a service-linked role"). | `iam/store.go` `DeleteRole`; every role mutator in `iam/actions.go`, `policies.go`, `keys.go` | run | *Corrected: was `memory`.* **model**: error shape `UnmodifiableEntity`, "service-linked roles are protected Amazon Web Services resources. Only the service that depends on the service-linked role can modify or delete the role on your behalf". Direction: more forgiving | SDK test per operation on a service-linked role |
| IAM-21 | P1 | A policy document with `"Version": "2020-01-01"` is accepted. | `internal/iampolicy/policy.go` `Parse` | run | memory: `MalformedPolicyDocument` | Policy test, skipped until recorded |
| IAM-22 | P1 | The synthesized `ReadOnlyAccess` grants `Get*`, `List*`, `Describe*`, `BatchGet*` and so denies `dynamodb:Query` and `Scan`, `sqs:ReceiveMessage`, `kinesis:GetRecords` and others. | `iam/managed.go` | run (`dynamodb:Query` simulated implicitDeny) | memory: the real policy allows `dynamodb:Query` and `Scan` | Unit test over a short list of real actions, once recorded |
| IAM-23 | P1 | The account alias is one string: `CreateAccountAlias` replaces whatever was set and `DeleteAccountAlias` clears it whatever name is passed. | `iam/policies.go` `hCreateAccountAlias`, `hDeleteAccountAlias` | read; a second create succeeding is run | memory. *Model, 2026-10-11:* "you can have only one" (`ListAccountAliases`), "Amazon Web Services supports only one alias per account"; `CreateAccountAlias` lists `EntityAlreadyExists` and `LimitExceeded`, `DeleteAccountAlias` lists `NoSuchEntity`; which of the first two a second create answers is not in the model | SDK test: delete a name that is not the alias |
| IAM-24 | P1 | An identity denial final at the middleware is written as JSON whatever protocol the service speaks, with code `AccessDenied` and no reason. Whether an XML or Query client reads the code from it is not established for S3, SNS, IAM and STS. *Established 2026-10-11 (run, enforce, AWS CLI v1 as the client, a user with no policy):* IAM `list-users`, STS `get-caller-identity` and SNS `list-topics` print "Unable to parse response (not well-formed (invalid token): line 1, column 0), invalid XML received. Further retries may succeed" followed by the JSON body, so the caller never sees `AccessDenied` and a retryer is told to retry; S3 `ls` prints "An error occurred (403) when calling the ListBuckets operation: Forbidden" with no code; SQS, DynamoDB and Lambda print `AccessDenied` correctly. Severity stays P1 (a denial that parses nowhere is the one an app's error handling needs). | `dozeaws.go` `writeDenied`; `iam/errors.go` `errAccessDenied` | run 2026-10-11 | model for the envelopes (`awsQuery` for IAM and STS: `<ErrorResponse><Error><Type>Sender</Type><Code>`); the code `AccessDenied` is `memory` | Root test: an explicit identity Deny on S3 and on a Query call, read through the Go SDK v1, v2 and boto3 |
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

**Found by the pre-stage review, 2026-10-11** (section 12): by reading the
documentation of every operation and member in `.audit-models/iam.json` and
`sts.json`, then running what the reading raised against a binary built from
the working tree with the AWS CLI v1 as the client. Ids continue the page's own.
The direction is named, because "stricter than AWS" breaks a correct caller
exactly as "more forgiving" breaks a deploy. Totals with the 38 above: 70
findings, one closed (IAM-17), P0 8, P1 30, P2 22, P3 9 open.

| Id | Sev | What | Direction | Where | Evidence | Source | Test to write first |
|---|---|---|---|---|---|---|---|
| IAM-39 | P1 | Every refused operation answers 400 `UnsupportedOperationException` ("doze-aws does not implement X: why"). The protocol's refusal is HTTP 501 `NotImplemented` with `X-Doze-Not-Built: declined` or `deferred` (decision D5, foundation B6, `awshttp.NotBuilt`). That is 85 IAM operations, and STS's `DecodeAuthorizationMessage`, which is dispatched and answers 400 `InvalidAuthorizationMessageException`: a modelled code that tells a caller its message was invalid or expired | wrong code, wrong status | `iam/iam.go` `ServeHTTP`, `stubActions`; `sts/actions.go` `decodeAuthorizationMessage`; `testdata/modelsweep.json` (85 `legacystub`) | run (`CreateOpenIDConnectProvider`, `DecodeAuthorizationMessage`) | n/a (D5); `model` for what the STS code means: "the message passed to DecodeAuthorizationMessage was invalid" | The model sweep's baseline: the 85 move to `notbuilt`; one SDK test per SDK that the caller sees the error once |
| IAM-40 | **P0** | `CreateUser` and `CreateRole` accept `PermissionsBoundary` and drop it (the handlers never read it). `GetUser`, `GetRole`, the create responses and `GetAccountAuthorizationDetails` never return `PermissionsBoundary`, not even after `PutUserPermissionsBoundary` (`userView` and `roleView` have no such member), and `Policy` has no `PermissionsBoundaryUsageCount`. A Terraform plan shows a permanent difference on every user and role that declares a boundary, and a test of "the role cannot do X because of its boundary" cannot be written | missing (a silent no-op) | `iam/actions.go` `hCreateUser`, `hCreateRole`, `userView`, `roleView`, `policyView` | run: `create-user --permissions-boundary arn:...:policy/pol1`, then `get-user`: no boundary; `put-user-permissions-boundary`, then `get-user`: none | `model`: `CreateUser.PermissionsBoundary`, `CreateRole.PermissionsBoundary`, `User.PermissionsBoundary`, `Role.PermissionsBoundary` (`AttachedPermissionsBoundary`), `Policy.PermissionsBoundaryUsageCount` | SDK v2: create a role with a boundary and `GetRole` returns it; Put then Get; under enforce an action outside the boundary is denied |
| IAM-41 | P1 | `modelcheck.FromQuery` keeps one element of each Query list and merges the rest into it. So (a) a list's length is never checked: 51 tags in one `TagUser` are accepted (model: 0 to 50), and likewise `PolicyInputList`, `ActionNames`, `ResourceArns`, `TagKeys`, `ClientIDList`, `ThumbprintList` and STS's `Tags` and `TransitiveTagKeys` ("up to 10 `PolicyArns`" is prose in the model, with no length trait, so it is a separate rule: IAM-53); (b) an element's rule is checked against whichever element the form's map iteration wrote last: a second tag with a 129-character key was refused in 2 of 8 identical requests. The same defect as SQS-26, in the two services that call `FromQuery` directly | more forgiving, and nondeterministic | `internal/modelcheck/modelcheck.go` `FromQuery`, `insertQuery` ("One element is enough"); `iam/validate.go` and `sts/validate.go` (no list-length entry) | run | `model`: `tagListType`, `tagKeyListType` 0 to 50, `ProvidedContextsListType` 1 to 5, `policyDocumentType` per member. Foundation B13 owns the walker; this stage owns the per-operation cases and the regenerated `cases_*.json` | For every list member of every dispatched operation: one case at the bound, one over it, and a second element invalid, run 20 times |
| IAM-42 | P1 | Policy documents are encoded with `url.QueryEscape`, which writes a space as `+`. The model: "URL-encoded compliant with RFC 3986", which is `%20`. botocore (CLI v1, boto3) decodes with `unquote`, which leaves `+`: `GetPolicyVersion` of a policy with one space printed `%7B%22Version%22...+%22Sid%22...` still encoded, while a document with none printed a decoded object. Affects `GetPolicyVersion`, `Get{User,Group,Role}Policy`, every `Role` (`CreateRole`, `GetRole`, `ListRoles`, `UpdateRoleDescription`) and `GetAccountAuthorizationDetails` | wrong shape | `iam/actions.go` `viewRole`; `iam/policies.go` `hGetPolicyVersion`, `getInline`, `hGetAccountAuthorizationDetails` | run (CLI v1) | `model`: `GetPolicyVersion`, `GetUserPolicy`, `GetGroupPolicy`, `GetRolePolicy`, `GetRole`, `GetAccountAuthorizationDetails` ("Policies returned by this operation are URL-encoded compliant with RFC 3986") | A document with a space in a `Sid` and in a value, read through boto3 and decoded with `unquote` |
| IAM-43 | P1 | `GetAccountAuthorizationDetails` returns `UserDetailList` and `RoleDetailList` only: no `GroupDetailList`, no `Policies` (managed policies with every version), and it ignores `Filter`, `MaxItems` and `Marker` (run: `--filter Group` returned users and roles while two groups existed). `UserDetail` lacks `PermissionsBoundary` and `Tags`; `RoleDetail` lacks `InstanceProfileList`, `PermissionsBoundary`, `Tags` and `RoleLastUsed` | missing, more forgiving | `iam/policies.go` `hGetAccountAuthorizationDetails` | run | `model`: the operation, `UserDetail`, `GroupDetail`, `RoleDetail`, `ManagedPolicyDetail` | SDK v2: a group, a user in it, a customer policy with two versions; each `Filter` value; the paginator |
| IAM-44 | P1 | Where the model lets a call omit `UserName` and take the user from the signing key, the code refuses or answers something else: `CreateAccessKey` answers `ValidationError` "UserName is required (doze-aws has no implicit calling user)"; `ListAccessKeys` answers `NoSuchEntity` "The user with name  cannot be found." (an empty name); `GetUser` answers a fixed user named `root`, created in 1970, for every key, a user's included. `UpdateAccessKey` and `DeleteAccessKey` ignore a `UserName` that is not the key's owner (the model lists `NoSuchEntity`). Section 5's "decide after IAM-1" is settled by the model | stricter, wrong code, wrong data | `iam/keys.go` `hCreateAccessKey`, `hListAccessKeys`, `hUpdateAccessKey`, `hDeleteAccessKey`; `iam/actions.go` `hGetUser` | run (`CreateAccessKey`, `ListAccessKeys`); read (`GetUser`) | `model`: `CreateAccessKey` ("If you do not specify a user name, IAM determines the user name implicitly based on the Amazon Web Services access key ID signing the request"), `ListAccessKeys` ("If a temporary access key is used, then UserName is required"), `UpdateAccessKey`, `DeleteAccessKey`, `GetUser`, `ListMFADevices`. A user's key resolves today; a session key waits for IAM-1 | SDK v2 signed with a user's key: each of the five with no `UserName`; with another user's name |
| IAM-45 | P1 | Names are case-sensitive here and are not on AWS: `Devs` and `devs` coexist as groups (run) and `GetUser ALICE` answers `NoSuchEntity` for `Alice`. A policy's identity is its path plus its name, so `/pol1` and `/a/pol1` coexist; the model says names are unique "within the account". That creation is refused is the model's; that a lookup in another case finds the entity is `memory` | more forgiving at create, stricter at lookup | `iam/store.go` every `Create*` and `Get*` (bucket key is the name; policies `normPath(path)+name`) | run | `model`: `CreateUser.UserName`, `CreateGroup.GroupName`, `CreateRole.RoleName`, `CreatePolicy.PolicyName`, `UpdateUser.NewUserName`, `UpdateGroup.NewGroupName`: "IAM user, group, role, and policy names must be unique within the account. Names are not distinguished by case" | Create `A` then `a` for each kind: `EntityAlreadyExists`; policy `/x/p` then `/p`; rename `a` to `A` |
| IAM-46 | P1 | Parameters the model defines are accepted and ignored, and entities the model says must exist are not looked up. `ListPolicies` ignores `Scope` (`--scope AWS` returned the customer policy, and AWS-managed policies are never listed) and `PolicyUsageFilter`; `ListEntitiesForPolicy` ignores `PathPrefix` and `PolicyUsageFilter`, lists nothing for `LocalManagedPolicy` or `AWSManagedPolicy`, and answers 200 for a policy that does not exist; `ListInstanceProfiles` and `ListAttached{User,Group,Role}Policies` ignore `PathPrefix`; `ListInstanceProfilesForRole` answers 200 for a role that does not exist; `RemoveUserFromGroup` answers 200 for a group that does not exist and for a user who is not a member; `RemoveRoleFromInstanceProfile` answers 200 for a role that does not exist or is not in the profile | more forgiving | `iam/policies.go` `hListPolicies`, `hListEntitiesForPolicy`, `listAttached`; `iam/keys.go` `hListInstanceProfiles`, `hListInstanceProfilesForRole`, `hRemoveRoleFromInstanceProfile`; `iam/actions.go` `hRemoveUserFromGroup` | run (each) | `model`: each parameter's text, and `NoSuchEntity` on each operation's list. Whether a non-member's removal is `NoSuchEntity` is `memory` | One SDK case per row |
| IAM-47 | P1 | `CreateInstanceProfile` accepts `Tags` and drops them, and the response's `InstanceProfile` has no `Tags` member, so `GetInstanceProfile` never returns what `TagInstanceProfile` wrote (`ListInstanceProfileTags` does). Terraform's `aws_iam_instance_profile` with `tags` plans a difference forever | missing | `iam/keys.go` `hCreateInstanceProfile`, `instanceProfileView` | run | `model`: `CreateInstanceProfile.Tags` ("If any one of the tags is invalid or if you exceed the allowed maximum number of tags, then the entire request fails and the resource is not created"), `InstanceProfile.Tags`; `ListInstanceProfiles` "does not return tags" | Create with two tags, `GetInstanceProfile`, `ListInstanceProfiles` |
| IAM-48 | P1 | The simulators. (a) An unparseable `PolicyInputList` answers `MalformedPolicyDocument`; the model's errors for `SimulateCustomPolicy` and `SimulatePrincipalPolicy` are `InvalidInput` and `PolicyEvaluation` (500), and for `GetContextKeysFor*Policy` `InvalidInput`. (b) These inputs are accepted and ignored: `ResourcePolicy`, `CallerArn`, `ResourceOwner`, `ResourceHandlingOption`, `PermissionsBoundaryPolicyInputList` (run: an Allow outside the passed boundary came back `allowed`), `OrderedOrganizationPolicyInputList`, `PolicyExclusionList`, `MaxItems`, `Marker`. (c) `ActionNames` "does not support using wildcards (*) in an action name" (`SimulateCustomPolicy`) and the code evaluates `s3:*` as a name | wrong code; more forgiving | `iam/simulate.go` | run (b, boundary), read | `model`: both operations' errors and member text | Each input changes the answer, or is refused; a bad document answers `InvalidInput` on all four |
| IAM-49 | P1 | The evaluator lacks grammar. Policy variables are not substituted: `Resource: arn:aws:s3:::home/${aws:username}/*` simulated for `dave` against `.../home/dave/x` with `aws:username=dave` supplied answers `implicitDeny` (run), and so is a variable in a `Condition` value. `BinaryEquals` is not an operator the engine knows, so a statement that uses it silently never matches. The per-user home-directory bucket policy and `${aws:PrincipalTag/team}` are the common uses | stricter | `internal/iampolicy/policy.go` `resourceMatches`; `conditions.go` `applyOperator` | run | `memory` (the IAM User Guide's policy-variable and condition-operator pages, not read). The model's `ContextEntries`: "Whenever a context key is evaluated in one of the simulated IAM permissions policies, the corresponding value is supplied" | Policy tests, skipped until recorded: a variable in `Resource` and in `Condition`; `BinaryEquals` |
| IAM-50 | P1 | `DeleteServiceLinkedRole` answers `DeletionTaskId` `task/<RoleName>/00000000`; the model: "task/aws-service-role/<service-principal>/<role-name>/<task-uuid>". It deletes synchronously through `DeleteRole` (so it also refuses while a policy is attached, which the model's text does not list as a precondition, and deletes a role that is not service-linked), and its other half, `GetServiceLinkedRoleDeletionStatus`, is refused: `Status` is `SUCCEEDED`, `IN_PROGRESS`, `FAILED` or `NOT_STARTED`, and "If you call this operation more than once for the same service-linked role and an earlier deletion task is not complete, then the DeletionTaskId of the earlier request is returned". Decided in section 12 (L7) | wrong shape, missing | `iam/actions.go` `hDeleteServiceLinkedRole`; `iam/iam.go` `stubActions` | run | `model`: both operations | Delete, read the id's shape, poll to `SUCCEEDED`; delete again |
| IAM-51 | **P0** | `sts:GetCallerIdentity` and `sts:GetSessionToken` are evaluated like any action, so under enforce a user with no policy gets `AccessDenied` from both (run), and an explicit Deny on `sts:GetCallerIdentity` refuses it. Every SDK credential check, Terraform provider and CI action starts with `GetCallerIdentity` | stricter (a failure AWS does not produce) | `internal/iamguard/resolve.go` (`sts:` actions resolved), `iam/authorize.go` `Authorize` | run (enforce) | `model`: "No permissions are required to perform this operation. If an administrator attaches a policy to your identity that explicitly denies access to the sts:GetCallerIdentity action, you can still perform this operation. Permissions are not required because the same information is returned when access is denied"; `GetSessionToken`: "No permissions are required for users to perform this operation" | Enforce: a user with no policy, and one with an explicit Deny; both succeed |
| IAM-52 | P1 | IAM and STS requests resolve to no resource (no `resourceRules` entry), so under enforce a policy that grants `sts:AssumeRole` on a role's ARN, the commonest form, never matches: run, a user with exactly that Allow got `AccessDenied`; likewise `iam:CreateRole` on `role/app-*`. EventBridge's `Name` and `EventBusName` both become `rule/<value>`: a bus operation is authorised on a rule ARN and `PutEvents` (`Entries[].EventBusName`) on none | stricter (enforce only) | `internal/iamguard/resolve.go` `resourceRules` | run, read | `model` for AssumeRole ("The administrator must attach a policy that allows the user to call AssumeRole for the ARN of the role in the other account"); the IAM and EventBridge ARN forms are `memory` (the Service Authorization Reference) | `resolve_test`: `AssumeRole`'s `RoleArn` resolves to it; an enforce test with the grant; a bus operation resolves to `event-bus/<name>` |
| IAM-53 | P1 | STS does not read `Policy`, `PolicyArns`, `Tags` or `TransitiveTagKeys`. A `Policy` that is not JSON, and a `PolicyArns` entry that names no policy, are answered 200 (run); `GetFederationToken` with a malformed `Policy` too. A session policy is never intersected with the role's. The model lists `MalformedPolicyDocument` (400) and `PackedPolicyTooLarge` (400) on `AssumeRole`, `AssumeRoleWithSAML`, `AssumeRoleWithWebIdentity` and `GetFederationToken`; the policies "must exist in the same account as the role"; "up to 10 managed policy ARNs"; "If you pass a session tag with the same key as an inherited tag, the operation fails" | more forgiving | `sts/actions.go` | run | `model` | A malformed `Policy` on each of the four; a missing `PolicyArns` entry; 11 entries |
| IAM-54 | P2 | STS responses omit what the model returns: `SourceIdentity` is never echoed (AssumeRole, SAML, web identity; AssumeRoot answers the constant `test`), `SessionTokenSize` and `SessionTokenUtilization` are absent, `PackedPolicySize` is always 0, `MinimumSessionTokenSize` ("STS increases the session token to at least this size") is ignored, and `SerialNumber`, `TokenCode` and `ProvidedContexts` are accepted and dropped | missing | `sts/actions.go` | run | `model` | A `SourceIdentity` round trip; `MinimumSessionTokenSize: 2000` gives a token of at least 2 000 bytes |
| IAM-55 | P2 | The assumed-role ARN carries the local account whatever account the `RoleArn` names: `role/other` in `123456789012` came back `arn:aws:sts::000000000000:assumed-role/other/sess`, and credentials were minted with no trust check | wrong data | `sts/actions.go` `assumedRole` | run | `memory` (the ARN form; the model's `AssumedRoleUser` says only that the ARN and id "include the RoleSessionName") | An `AssumeRole` on a role ARN in another account |
| IAM-56 | P2 | `AssumeRoleWithSAML` fills its response from constants: `Audience` is always `https://signin.aws.amazon.com/saml` (model: "the value of the Recipient attribute of the SubjectConfirmationData element"), `NameQualifier` is 24 random characters (model: `BASE64(SHA1("<issuer>" + "<account>" + "/<provider name>"))`), `SubjectType` is always `persistent` (model: the NameID `Format` with the `urn:oasis:names:tc:SAML:2.0:nameid-format` prefix removed). `AssumeRoleWithWebIdentity` answers empty `<Audience>` and `<Provider>` when the token is not a JWT and never produces `ExpiredTokenException`, `InvalidIdentityToken` or `IDPRejectedClaim` (a token's shape and `exp` are not read: a documented difference) | wrong data | `sts/actions.go` `assumeRoleWithSAML`, `assumeRoleWithWebIdentity` | run, read | `model` | An assertion with a Recipient and a persistent NameID; a token with an `exp` in the past |
| IAM-57 | P2 | `GetAccountSummary` returns five of the model's 33 keys (run). A caller that reads `UsersQuota`, `AccessKeysPerUserQuota`, `InstanceProfiles`, `PolicyVersionsInUse` or `GlobalEndpointTokenVersion` gets nothing. Counts come from the store; the quota defaults are `memory` | missing | `iam/policies.go` `hGetAccountSummary` | run | `model`: `summaryKeyType` | Every key present; the counts follow a create and a delete |
| IAM-58 | P2 | Every version of a policy reports the policy's creation time as its `CreateDate` in `GetPolicyVersion` and `ListPolicyVersions` (run: three versions published in turn, one time), and `CreatePolicyVersion` answers `pol.Updated`. The store keeps no per-version time | wrong data | `iam/store.go` `policy`; `iam/policies.go` | run | `model`: `PolicyVersion.CreateDate`, "when the policy version was created"; `Policy.UpdateDate`, "when the most recent policy version was created" | Two versions a second apart (the clock option) |
| IAM-59 | P2 | The synthesised AWS-managed policies answer for names AWS has not published: `S3FullAccess`, `AmazonSecretsManagerFullAccess`, `AmazonLambdaFullAccess` and `AmazonStepFunctionsFullAccess` all resolved (run), because the `Amazon`, `AWS` and empty vendor prefixes are each tried against every service suffix. A template that attaches one deploys here and fails on AWS | more forgiving | `iam/managed.go` `synthesize` | run | `memory`: which names exist. moto vendors the list; the recording is `aws iam list-policies --scope AWS` | A table of names AWS publishes and names it does not, once recorded |
| IAM-60 | P3 | List operations return attributes the model says they do not: `ListUsers`, `ListRoles` and `ListPolicies` return `Tags`; `ListPolicies` returns `Description` | extra attributes | `iam/actions.go` and `iam/policies.go` `list*` | run | `model`: "This operation does not return the following attributes ... PermissionsBoundary, RoleLastUsed, Tags"; `Policy.Description` "is not included in the response to the ListPolicies operation" | The list response's keys, per operation |
| IAM-61 | P3 | `UpdateRole` with an empty `Description` changes nothing (`if v != ""`), so a description cannot be cleared; the console's role form inherits the gap. `UpdateRoleDescription` can | stricter | `iam/actions.go` `hUpdateRole` | read | `model`: `roleDescriptionType` 0 to 1000 (what empty means is `memory`) | `UpdateRole` with `Description=` |
| IAM-62 | P3 | `GetAccessKeyLastUsed` answers the stack's region as `Region`, not the region of the key's last request; `RoleLastUsed` is never returned by `GetRole` (a role session is never recorded: IAM-1) | wrong data | `iam/keys.go` `hGetAccessKeyLastUsed` | read | `model`: `AccessKeyLastUsed.Region`, "where this access key was most recently used"; `Role.RoleLastUsed` | A call signed in `eu-west-1`, then `GetAccessKeyLastUsed` |
| IAM-63 | P3 | `GetSessionToken` and `GetFederationToken` use the default 43 200 and the maximum 129 600 for every caller. For the account root the model says "restricted to a maximum of 3,600 seconds" and "If the duration is longer than one hour, the session for Amazon Web Services account owners defaults to one hour" (so not a refusal). Every unknown key is the root here, so both a default call and a long one should give one hour | wrong data | `sts/actions.go` `getSessionToken`, `getFederationToken` | read | `model` | Root: no duration gives 3 600 s, `43200` gives 3 600 s |
| IAM-64 | P2 | **Decision.** Time- and consistency-based rules (L8) are not modelled: `EntityTemporarilyUnmodifiable` ("a user name that was deleted and then recreated ... try again after waiting several minutes"; on `UpdateUser`, `DeleteLoginProfile`, `ChangePassword`, `UpdateLoginProfile`, `EnableMFADevice`, `DeactivateMFADevice`); IAM's eventual consistency ("You must then wait for the change to appear across all of Amazon Web Services because of eventual consistency", `AddRoleToInstanceProfile`); and a session's expiry. Proposed in section 12: all three off by default, on under one setting, `--iam-strict` / `[iam] strict = true`, held in memory | more forgiving, by default | n/a | read | `model` (the rules and their names); `memory` ("several minutes", and the code an expired session gets) | With the setting on: delete and recreate a user, `UpdateUser` within the window; use a session past `Expiration` |
| IAM-65 | P1 | **Decision.** Operations declared S that the model describes as records with no behaviour beyond storage, and that common deploys need: OIDC providers (10 operations; EKS IRSA and GitHub Actions modules create one), SAML providers (8), the account password policy and login profiles (7; CIS-style baseline modules), the credential report (2; an async status, L7), and `GetServiceLinkedRoleDeletionStatus` (IAM-50). Each answers 400 `UnsupportedOperationException` today (IAM-39). Recommended: C for the providers, the password policy and login profiles (stored, returned, nothing signs in or federates), F for the report and the deletion status (a small visible state each); every other S row stays | stricter (a deploy fails) | `iam/iam.go` `stubActions`; `docs/SUPPORT.md`; `docs/not-built.md` | read; run (one refusal) | `model` for the shapes, ranges and defaults; `memory` for how often modules create them (moto and LocalStack implement them) | Per operation family: create, get, list, update, delete, tags; the report's three states |
| IAM-66 | P2 | The policy builder shows `AWS` in the principal-type select for a `Service` principal: the default trust document (`"Principal": { "Service": "lambda.amazonaws.com" }`) renders as type `AWS` beside `lambda.amazonaws.com`, on create and on the role's trust editor. The JSON stays right (a Sid edit serialised `Service`); only the display is wrong | wrong display | `templates/iam.html` `policy_builder` (the `x-for` options render after the select's value is set) | run (the select's `value` is `AWS`, `selectedIndex` 0) | n/a | A browser test: the select reads `Service` |
| IAM-67 | P2 | The builder's statement row is broken at 1440: the Effect select fills the row and the Sid input collapses to 22 px beside the trash button, on every builder in the console (create role, create policy, trust, inline, new version). The boards draw `Allow [select] [Sid - optional] remove` in one row. Cause: `select.input { width: 100% }` (specificity 0,1,1) outranks `.pb-effect { width: 92px }` (0,1,0) | differs from the design, unusable field | `static/app.css` `.pb-head`, `.pb-effect`, `.pb-sid` against `select.input` | run (the Sid input is 22 px wide, and the screenshots) | n/a | A browser test: the Sid input is at least 160 px wide and the Effect select at most 100 |
| IAM-68 | P2 | The access log's principal column shows the full ARN, truncated: every row reads `arn:aws:iam::000000000000:us...` or `...:ro...`, so users cannot be told apart; the boards show `user/harbour-ci` and `root`. The "Matched by" header wraps and its values are clipped (`NoDeletes` runs off the table), and the "Count" header is cut to "COUN" | wrong display | `templates/iam.html` `iam_home` (the table's column widths; the principal is `Principal`, not its short form) | run (screenshots) | n/a | A browser test: the first row's principal cell reads `user/harbour-ci` |
| IAM-69 | P3 | Smaller visible defects: the list pane's "New" is clipped to "Ne" beside the info button on every IAM page (the same header is in other services' list panes; check there); the "AWS managed" chip wraps to two lines and overflows the Source column on every principal page; the Generate button is `disabled` in off mode, as its template says, and is drawn exactly like an enabled one (there is no `.btn[disabled]` style outside the tag footer) | wrong display | `templates/iam.html` `iam_lp`, `iam_principal`; `app.css` | run (screenshots; the "New" link is 26 px wide with 34 px of content) | n/a | Browser tests on the three |
| IAM-70 | P2 | Sessions are not the role: the model's session rules have nothing to attach to. Role chaining "limits your Amazon Web Services CLI or Amazon Web Services API role session to a maximum of one hour" and a longer `DurationSeconds` fails; a `GetFederationToken` session with no policy "has no permissions", "cannot call any IAM operations" and cannot call "any STS operations except GetCallerIdentity"; a `GetSessionToken` session "cannot call any IAM API operations unless MFA authentication information is included" and "any STS API except AssumeRole or GetCallerIdentity"; an `AssumeRole` session "cannot call the Amazon Web Services STS GetFederationToken or GetSessionToken API operations"; a `DurationSeconds` above the role's `MaxSessionDuration` "fails". All of it follows IAM-1 | missing | `sts/`, `iam/authorize.go` | read | `model` | One enforce test per rule, once sessions exist |

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
- [ ] `CreateAccessKey`, `ListAccessKeys`, `UpdateAccessKey`, `DeleteAccessKey` and `GetUser` with no `UserName` take the user from the signing key; with a `UserName` that is not the key's owner: `NoSuchEntity` (IAM-44). *Corrected 2026-10-11: was "today refused; AWS uses the caller. Decide after IAM-1." The model decides; a session key's case waits for IAM-1.*
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
- [ ] STS sessions across a restart: decide (kept, or invalid) and write it. *Proposed in section 12 (L9): kept, with their expiry; doze reaps an idle backend after five minutes, which would otherwise end a session the application is still holding.*
- [ ] Every ARN under a non-default `--account-id`; IAM is global, so `--region` must not appear in one.

**Added by the pre-stage review (2026-10-11).** Each is checked on the one wire
the model declares (`awsQuery`; GET and POST, signed and unsigned: all four
already route correctly, run).
- [ ] Every list member of every dispatched operation has a bound case and a second-element case (IAM-41), IAM and STS both.
- [ ] A document with a space comes back decodable by `unquote` (IAM-42).
- [ ] `CreateUser` and `CreateRole` keep `PermissionsBoundary`, and `GetUser`, `GetRole`, `GetAccountAuthorizationDetails` return it (IAM-40).
- [ ] Names that differ only in case are one name, for users, groups, roles and policies; a policy's name is unique whatever its path (IAM-45).
- [ ] `DeleteUser`, `DeleteGroup`, `DeleteRole`, `DeletePolicy` and `DeleteInstanceProfile` each refuse for every item the model lists (IAM-12, IAM-11); `RemoveUserFromGroup` and `RemoveRoleFromInstanceProfile` answer `NoSuchEntity` for what is not there (IAM-46).
- [ ] Every write the model protects on a service-linked role answers `UnmodifiableEntity` (IAM-20); `TagRole` does not.
- [ ] `CreateServiceLinkedRole` answers one of the four codes the model lists for a duplicate and for an unknown service (IAM-19); `DeleteServiceLinkedRole` returns a task id of the modelled shape and `GetServiceLinkedRoleDeletionStatus` answers it (IAM-50).
- [ ] `GetCallerIdentity` and `GetSessionToken` need no permission and are never denied (IAM-51); a grant on a role's ARN admits `AssumeRole` (IAM-52).
- [ ] An `AssumeRole`'s `Policy`, `PolicyArns`, `Tags`, `TransitiveTagKeys`, `SourceIdentity`, `ExternalId` and `MinimumSessionTokenSize` are each read, or the ledger says it is not (IAM-53, IAM-54).
- [ ] `ListPolicies` with `Scope` `AWS`, `Local`, `All`, and `OnlyAttached`; `ListEntitiesForPolicy` with each `EntityFilter`, `PathPrefix` and `PolicyUsageFilter`; `PathPrefix` on every list that takes one (IAM-46).
- [ ] The simulators: each accepted input changes the answer or is refused; a bad document is `InvalidInput`; one result per action with `ResourceSpecificResults` (IAM-15, IAM-48).
- [ ] A policy variable in `Resource` and in `Condition`; `BinaryEquals` (IAM-49, skipped until recorded).
- [ ] Attachment, inline-policy and group-membership limits: the model lists `LimitExceeded` on each and gives no number. Establish by recording; nothing is set on `memory`.
- [ ] Which of `AttachRolePolicy`, `AttachUserPolicy`, `AttachGroupPolicy` and the boundary Puts answers `PolicyNotAttachable` (400): "Amazon Web Services service role policies can only be attached to the service-linked role for that service". The synthesiser cannot tell without the published list (IAM-59).

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

*Corrected 2026-10-11:* the `Today` column says only that the screen's
**content** exists (its tab, its form, its button). Rendered beside its board,
no screen matches: see 6a, where 26 of 26 differ. "exists" in this table is not
"built from the board".

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

**The settings that need a record (L11, written 2026-10-11).** The page has no
`internal/settings` record yet (foundation B4: "each service adds its records and
its form rows in its stage"), and none of the five teaching lines is built on any
form. Each row is one setting's lines 1 to 4 (name and unit, value in words, what
it does, range and default and **enforced or stored**) with the neighbour check
of line 5 where there is one. Every range below is `model`.

| Setting (AWS name, unit) | Range, default | Enforced or stored | Line 5, with real numbers |
|---|---|---|---|
| User name, `UserName`, characters | 1 to 64 (1 to 128 on an existing user), `[\w+=,.@-]+` | enforced | none |
| Group name, `GroupName` | 1 to 128, same pattern | enforced | none |
| Role name, `RoleName` | 1 to 64, same pattern | enforced | none |
| Policy name, `PolicyName` | 1 to 128, same pattern; unique in the account whatever the path (IAM-45) | enforced | the names already taken, case-insensitively |
| Instance profile name, `InstanceProfileName` | 1 to 128, same pattern | enforced | none |
| Path, `Path`, characters | 1 to 512, begins and ends with `/`; default `/`; a policy's path may not contain `*` | enforced | none |
| Policy document, `PolicyDocument`, characters | 1 to 131 072 (the model's length); the quota is smaller and `memory` | enforced (length), stored | the version count of five, on a new version |
| Trust policy, `AssumeRolePolicyDocument`, characters | 1 to 131 072; a trust policy names a Principal and no Resource | enforced (length), validated, stored, **read by nothing** until IAM-2 | none |
| Role description, `Description`, characters | 0 to 1 000 | stored | none |
| Policy description, `Description` | 0 to 1 000; "immutable. After a value is assigned, it cannot be changed" | stored | none |
| Max session, `MaxSessionDuration`, seconds | 3 600 to 43 200; default 3 600 | enforced as a range, stored, **read by AssumeRole** after IAM-2 | the form's `DurationSeconds` against it |
| Session name, `RoleSessionName` | 2 to 64, `[\w+=,.@-]*` | enforced | none |
| Duration, `DurationSeconds`, seconds | AssumeRole, SAML, web identity 900 to 43 200, default 3 600 and "up to the maximum session duration set for the role"; session token and federation 900 to 129 600, default 43 200, root 3 600; AssumeRoot 0 to 900, default 900 | enforced as a range; the role's maximum and the root's after IAM-2 and IAM-63 | the role's `MaxSessionDuration`, from `GetRole` |
| External id, `ExternalId` | 2 to 1 224, `[\w+=,.@:\/-]*` | enforced; read by a trust policy's `sts:ExternalId` after IAM-2 | the trust policy's condition, if any |
| Source identity, `SourceIdentity` | 2 to 64, `[\w+=,.@-]*`, not `aws:`-prefixed | enforced; echoed after IAM-54 | none |
| Tags, `Tags` | 0 to 50 per request; key 1 to 128, value 0 to 256, `[\p{L}\p{Z}\p{N}_.:/=+\-@]` | enforced after IAM-41; the per-resource count `doc` | the resource's tag count of the maximum |
| Account alias, `AccountAlias`, characters | 3 to 63, lower-case letters, digits, hyphen, not at an end, not twice in a row | enforced | none (one alias only) |
| Page size, `MaxItems` | 1 to 1 000; default 100 | enforced after IAM-16 | none |
| Federated user name, `Name` | 2 to 32, `[\w+=,.@-]*` | enforced | none |
| Versions of a policy | at most 5; `v` followed by digits | enforced | versions used of five |
| Access keys of a user | "a small number" (model); two (`doc`) | enforced after IAM-9 | keys used of two |

Source for the neighbour check of the duration: the role's own maximum, read on
the form from `GetRole`. The refusal half ("AWS refuses a duration above the
role's maximum") is `model` and is on the board; the check that does the
refusing is IAM-2's.

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
| Iam-12, Iam-13, Iam-25 *(added 2026-10-11)* | The built page's rename confirm says "Policies and keys follow; references by name elsewhere do not" (`templates/iam.html`), which is false until IAM-5; the board says the keys stay under the old name, which is true today | IAM-5 |
| Iam-19, Iam-25 *(added)* | Group delete: the built confirm says "Members keep their own permissions; they lose the group's" (`templates/iam.html`, `iam_group`); AWS refuses a group with members | IAM-12 |
| Iam-17, Iam-25 *(added)* | Policy delete "Detach it from every principal first" lacks "and delete its other versions" | IAM-12 |
| Iam-21, Iam-22, Iam-23 *(added)* | "Minted, not honoured" and "GetCallerIdentity still answers user/test"; the CLI preview's sentence "Paste into a shell and every AWS call from it authenticates as this principal" (built, false today) | IAM-1 (IAM-27) |
| Iam-24 *(added)* | "Summary: live counts" shows four of 33 keys | IAM-57 |

**Statements on the boards not yet backed.** Remove or prove each.

- Iam-Home, Iam-01, Iam-03: "Every request is recorded." False for
  CloudFormation and API Gateway (IAM-18).
- Iam-05, Iam-06, Iam-10: a conditioned statement "cannot match here and reads
  implicitDeny". True for positive operators only; a negated operator matches
  on an absent key (`conditions.go`).
- Iam-05: "Same engine as enforce mode." Not for a permissions boundary (IAM-14).
- Iam-22: "an expired set still works." Inferred from the code; not run.
  *2026-10-11: read, not run: `principalFor` never looks at an expiry, and an
  ASIA key is unknown to it. Held by the same reading as IAM-1, and changes
  with IAM-64.*
- Iam-13: "AWS would refuse the call" (inactive key), "Two keys per user on AWS".
  *2026-10-11: the first is `model` (UpdateAccessKey.Status: "Inactive means that
  the key cannot be used"); the second is `doc` (the quotas page the model
  names); the model says only "a small number of keys".*
- Iam-15, Iam-21: "AWS refuses a longer DurationSeconds than the role allows."
  *2026-10-11: `model`: "If you specify a value higher than this setting or the
  administrator setting (whichever is lower), the operation fails."*
- Iam-20, Iam-25: "AWS answers DeleteConflict" for a profile holding a role.
  *2026-10-11: `model`: DeleteInstanceProfile, "The instance profile must not
  have an associated role", `DeleteConflict` on its list.*
- Iam-21: "What they are good for" is advice, not a fact from the code.
- Iam-02: the middleware's denial is labelled `AccessDenied` for every
  protocol; only the JSON and S3 cases were run (IAM-24).
  *2026-10-11: run for IAM, STS, SNS (unparseable), S3 (no code), SQS,
  DynamoDB, Lambda (`AccessDenied`). The board's "A JSON service such as SQS
  answers AccessDeniedException with the same words" is the **guard's** answer
  (a resource-policy denial); the middleware's identity denial on SQS is
  `AccessDenied`. Neither half is backed until IAM-24 is closed.*
- Iam-19, Iam-25: "Members keep their own permissions when the group is deleted;
  they lose the group's." *2026-10-11: false for AWS: the model's `DeleteGroup`
  refuses while the group has users (IAM-12). Corrected in the board.*
- Iam-17, Iam-25: "Delete the policy last. It is refused while anything holds
  it." *2026-10-11: incomplete: it is also refused while it has versions beside
  the default (IAM-12). Corrected in the board.*
- Iam-07: the refusal's code. *2026-10-11: the model's error list for the
  simulators has no `MalformedPolicyDocument` (IAM-48). Corrected in the board.*
- Iam-11: `EntityAlreadyExists` for a duplicate service-linked role.
  *2026-10-11: not on `CreateServiceLinkedRole`'s list (IAM-19). Corrected in
  the board.*
- Iam-07, Iam-11, Iam-14, Iam-16, Iam-18: "the rule comes from AWS's own service
  model, so the same call would have failed on deploy." True of the
  `ValidationError` constraints; **not** of the policy-grammar refusals
  ("Has prohibited field Resource", "Policy statement must contain resources"),
  which are `memory` (the model has no policy grammar). The built copy is the
  foundation's A2 sentence ("it breaks a rule of the service"), which does not
  claim a provenance; keep it, and do not copy the board's.
- Iam-10: "Up to 131 072 characters." True of the model's length; AWS enforces a
  smaller quota (`memory`: 6 144 non-whitespace characters for a managed policy),
  so a document the form accepts may be refused on AWS. Stays until recorded.
- Iam-08: `AWSServiceRoleForAutoscaling`. `memory`: AWS spells it
  `AWSServiceRoleForAutoScaling` (IAM-19); not fixed on memory.

### 6a. Design fidelity, board by board

Written 2026-10-11 by rendering each of the 26 boards (`console-design/project/Iam-*.dc.html`,
1440 by 900 under `file://`, light, plus dark and 900 wide for the main create
and detail boards) and the real page in the same state, side by side, and
reading both. The pages came from an e2e build on a fresh data directory seeded
through the IAM wire with the boards' own names (`harbour-ci`, `harbour-lambda-exec`,
`developers`, `harbour-batch`, `harbour-read-statements`); the enforce and off
boards from two further instances. The first pass built the *content* the
`Today` column of the table above calls "exists" (the tabs, the forms, the
buttons) and **none of the boards' shape**: no two-column page, no right-hand
explanation panel, no teaching line, no callout. No board was compared before
this. **26 of 26 differ**; the table says how, and which of the differences are
decisions.

The shell (rail, top bar, the 1024 px notice at 900 wide, the dialog component)
is foundation C and A13 and is not re-opened here. The per-service icon stays:
the boards draw `IA` and, on the STS boards, `ST`; the page keeps `iam.svg` for
both (there is no STS icon in `static/aws/`).

| Board | Verdict | What differs |
|---|---|---|
| Iam-Home | differs | (a) The principal column is the full ARN, truncated, so every row reads `arn:aws:iam::000000000000:us…`; the board shows `user/harbour-ci` and `root` (IAM-68). (b) The **Last** column (`new`, `AccessEvent.Last`) is absent. (c) The mode note is a titled callout ("Soft mode" and a second sentence: "The mode is set when doze-aws starts, with `--iam-mode`; this page only shows it."); the page has one unframed sentence. (d) The subtitle is "denials first, then by action"; the page says "denials first". (e) The paragraph under the table ("Matched by is the statement that decided: its Sid, or the policy's name and the statement's position ... recorded as root") is absent. (f) Generate: the "the output →" link and "Writes one Allow for every action recorded for that principal, denied ones included" are absent; the page says "least privilege by construction rather than by guesswork", which IAM-7 shows false. (g) The page's log table is height-capped and scrolls; the board's is not. Dark matches the board's palette. At 900 the page shows the shell's 1024 px notice and still works; the board's two columns clip (boards are drawn at 1440 and are not responsive) |
| Iam-01-Empty | differs; **decision** | The empty-state card ("No principals yet", the `IA` mark, "New principal") is absent, and so is the "Soft mode" callout. Foundation A7 deleted IAM's `copy.go` entry because the log always exists, so the board's note ("the copy the console already holds for IAM and never shows") is stale: the card is new copy, not the old copy. Whether an empty principal list deserves a card when the log exists is the decision; recommended yes, built from `empty_detail` with one sentence |
| Iam-02-Log-Enforce | differs | The two panels under the table are absent: **"What the caller was told"** (three `Denied` callouts quoting `errAccessDenied` and the guard's message: explicit Deny, nothing allowed it, names no resource) and **"How a request is decided"** (three numbered rules), and so is the note that the console's own buttons are not judged. The mode callout ("Only a call signed with a user's access key is judged by that user's policies. Any other key id, the default `test` key included, is the account root") is a single sentence. The mode chip is drawn in the failure colour (red) where the board's is a plain bold chip: enforce is not a failure (the foundation's A3 list). The board's sentence about a JSON service answering `AccessDeniedException` is not backed (section 6, "not yet backed") |
| Iam-03-Log-Off | differs | The "IAM is off" callout (both flag names) and the third panel "What off means for resource policies" are absent. The board replaces the Simulate form with an explanation and "Open simulate"; the page keeps the full form, which works in off mode (the board's sentence says so, so the page is the more useful: deliberate unless the board's three-panel row is wanted). Generate is disabled and drawn as enabled (IAM-69) |
| Iam-04-Generate | differs | The board is a page of its own ("Access log · Generate a policy", "Back to the log") in two columns: left the form with the **scope switch** (`new`, off/on, `ScopeToResources`), the document and "Save as a customer policy"; right "The same principal, scoped" (`new`, with "1 call named no resource and stays *", from `UnresolvedResources`), two numbered notes (refused calls are Allowed here; a role never appears) and an "Other outcomes of this form" list with two refusals. The page generates under the log, one document, no scope switch, no scoped document, no notes; choosing a role answers a bare error (IAM-29: roles are offered) |
| Iam-05-Simulate | differs | The board is a page of its own: form left, **"Reading the answer"** (four notes) right; the result is a table under a strap "Every row is asked about `<resource>`" and a second run, "The same question with resource left blank" (the console can make that second call; the engine is unchanged). The page puts the result under the form inside the log page's Simulate panel, repeats the resource on every row, and has no notes and no second run. The line "the simulation supplies the runtime set" (IAM-26) is false |
| Iam-06-Simulate-Draft | differs | "Matched by" for a draft (`new`: the page prints "implicit — nothing matched" on every row, IAM-25) and "Create this as a policy" (`new`) are absent, and so is the three-note panel "Before you create it". Note 3 ("A statement with no Resource simulates as allowed here") changes with IAM-13 |
| Iam-07-Simulate-Refused | differs | The page draws the refusal as bare red text under the form (`.err`: "MalformedPolicyDocument: PolicyInputList.member.1: policy document is not valid JSON ..."), not the board's purple callout with code, message and sentence; the "Other things this form comes back with" list (`Nothing was sent`, `NoSuchEntity`, `ValidationError`, "Not refused, and worth knowing") is absent. The page's JSON editor adds a lint line the board does not have. The board's code is corrected to `InvalidInput` (IAM-48); the page still answers `MalformedPolicyDocument` |
| Iam-08-Create-Names | differs | **No form has a teaching line.** The board has, per kind: "User name · UserName", a sentence, "1 – 64 characters · letters, digits and + = , . @ _ - · enforced", and a grey box "What it can do is nothing until you attach a policy". The right column "The same form, for the other names" (group, instance profile, service-linked with its `makes` and `path` preview) is absent, and so are the Cancel button and the "After it: ..." lines. The page's kind tabs carry icons the board's do not |
| Iam-09-Create-Role | differs | Teaching lines absent (name; "Trust policy · who may assume this role" and its sentence). The right column "Two documents, two jobs" (a five-row table: answers, names, never, also refused, lives on) and "What this build does with a trust policy" are absent. The builder's row is broken and shows `AWS` for the `Service` principal (IAM-67, IAM-66). No Cancel, no "After it" line |
| Iam-10-Create-Policy | differs | The name line ("1 – 128 characters · ... · enforced") is absent. "Check this draft" has no **resource box** (`new`: `iamSimInline` reads `resource` and the builder never sends it). The notes "What a statement says" (four), the link to the generator and "Up to 131 072 characters. A document that does not parse, or has no Resource, is refused beside the field" are absent. The builder and the check chips are as drawn |
| Iam-11-Create-Refused | differs; **sentence deliberate** | The page puts the refusal under the builder and above the sentence, beside the control that caused it (the SQS rule); the board puts it at the top of the page. The sentence is the foundation's (A2: "doze-aws refused the request: it breaks a rule of the service. The input is what to change."), not the board's, whose claim "the rule comes from AWS's own service model" is false for policy grammar (section 6). The right column of other refusals is absent. The board's service-role row is corrected (IAM-19) |
| Iam-12-User | differs | One column against the board's two (attached policies, groups, tags on the left; inline policies, "What this user can do" and the link to the keys page on the right). The name's `user` badge and "created ..." sit beside the name on the board and in chips below it on the page. "Add an inline policy" is a header button on the board and a panel at the page's end. The page carries the access keys and the rename on this page; the board moves keys to Iam-13. The sentence under Attach is the opposite of the board's: "AWS-managed policies are synthesized on demand, so naming one that does not exist yet still works" (IAM-32; the board: "A name that matches no pattern is refused NoSuchEntity", which is what the code does). The "AWS managed" chip wraps (IAM-69). Dark and 900 as for Home |
| Iam-13-User-Keys | differs | A page of its own on the board ("Access keys", "Back to the user"): per-row **text buttons** Activate / Deactivate and Delete, the "How keys are read" notes (the key id names the caller; Inactive is recorded, not enforced; two keys on AWS; the secret is shown once) and the rename with its note. The page shows the table inside the user page with icon buttons and no notes; its empty-state sentence is IAM-30's false one. The board's sentences 1 to 3 wait on IAM-3, IAM-9 and IAM-5 |
| Iam-14-Inline-Edit | differs | The board is a page for inline policies: the edit panel left, "Add an inline policy" right with the name's teaching line, and three notes ("Inline lives and dies with its principal"; "A name is a key"; "It is evaluated with the rest"). The page edits in place on the user page. "Check this draft" has no resource box (`new`). The refusal's placement is the page's own (beside the builder) |
| Iam-15-Role | differs | One column against two. **Role settings** have no teaching lines: the board has "Description · up to 1 000 characters", "Max session · MaxSessionDuration · s", the unit echo "2 hours", "The longest a session assumed from this role may last", "3 600 – 43 200 · default 3 600 · enforced as a range, and stored", and the callout about `DurationSeconds`; the page has two bare fields. **Used by** (`new`) is absent: it needs `Role` on the console's `Function` type (`client_lambda.go`) and `roleArn` from `DescribeStateMachine`. The trust sentence ("Stored and validated. Nothing here reads it ...") is absent; the "Instance profiles" panel is a chip row |
| Iam-16-Role-Trust | differs | A page of its own with "Reading a trust policy" (three notes) and "Locally"; the page edits in place under a collapsed Document panel. "Replaces the whole document. There is no merge." is absent. The "Locally" paragraph waits on IAM-2 |
| Iam-17-Policy | differs | One column against two; the board's right column holds the versions, the New version panel and four notes. "Attached to" chips carry no kind on the page and carry `role` and `group` on the board. A `customer policy` badge is absent. Every version shows one time (IAM-58). Notes 3 and 4 are corrected in the board (IAM-12) |
| Iam-18-Policy-NewVersion | differs | A page of its own on the board; the page has the "New version" panel in place. "What publishing does" (three notes), "Room for five. Three are used" and "Refused, from this panel" (`LimitExceeded`, `MalformedPolicyDocument`, `NoSuchEntity`) are absent; "Check this draft" has no resource box (`new`) |
| Iam-19-Group | differs | One column against two ("How a group works": three notes; Rename with its long note). The sentence on deleting a group is corrected in the board (IAM-12); the page's confirm still carries the old one |
| Iam-20-Profile | differs | "What a profile is here" (three notes) is absent. The board draws the profile twice, with and without a role; the page shows one state at a time (fine). Note 3 ("doze-aws does not insist") waits on IAM-11 |
| Iam-21-Sts | differs | **No teaching line.** The board has, for Role, Session name and Duration, name and unit, the sentence, the range and "enforced", the unit echo "1 hour" and the fit callout; "Whose key is this?" has its own line "16 – 128 characters · letters, digits and _ · enforced". The right column (that panel, **"Minted, not honoured"**, **"What they are good for"**) is below on the page without the second and third. The lead sentence differs: the page says "paste the export line into a shell and that shell IS the principal. With IAM enforcing, this is how you test what a role can actually do", which is false (IAM-27); the board's is true |
| Iam-22-Sts-Minted | differs; **waits on IAM-1** | A page of its own on the board ("AssumeRole · Minted", "Mint another"): the credential rows, a one-line export, and **"What happens when you use them"**, a recorded trial under enforce, with three notes. The page shows the same rows under the form, plus the false sentence "every AWS call from it authenticates as this principal" (IAM-27). The trial panel is a static claim about today and is rewritten when IAM-1 closes |
| Iam-23-Sts-Modes | differs | A comparison board: all six modes with their ranges and refusals and the identity each gives. The page shows one mode's form at a time with its refusal inline; the refusal text matches ("Value null at 'name' failed to satisfy constraint: Member must not be null" for federation, run). The per-mode helper lines (ranges, defaults) are absent; the duration field's `max` is 43 200 for session token and federation (IAM-35) |
| Iam-24-Account | differs | Two columns. The account id beside the title and the alias block's lines are absent: "3 – 63 characters · lower-case letters, digits and hyphens · enforced", "Nothing signs in here, so an alias changes only what ListAccountAliases answers", "removing it does not ask". The password-policy panel ("Shown only when the account has set one ...") is absent. The export button is in the section header on the board and below it on the page, and the note "The response is shown as it arrives: XML, with each policy document URL-encoded" is absent. The four counts match |
| Iam-25-Confirms | differs; **deliberate** | The dialog is the foundation's A13 component (the title names the thing, the sentence follows, the verb is the button), not the board's "Are you sure? / Cancel / Confirm". Of the 14 sentences, 11 match the page. **Three differ:** group delete (the page's "Members keep their own permissions; they lose the group's" is false: IAM-12; the board is corrected), policy delete (the page lacks "and delete its other versions": IAM-12; corrected), user rename (the page says "Policies and keys follow ...", false until IAM-5; the board says what is true today). The "after confirm" refusals (user with keys, policies left, attached policy, AWS-managed policy, default version, sixth version) match the code's messages; the user-with-policies refusal was run |

**Where the design is wrong** (corrected in the working copy on 2026-10-11 in
Iam-07, Iam-11, Iam-17, Iam-19 and Iam-25; the published canvas still carries the
old text until it is republished)
- Iam-19 and Iam-25, group delete: "Members keep their own permissions when the
  group is deleted; they lose the group's." The model: a group "must not contain
  any users or have any attached policies" (`DeleteConflict`).
- Iam-17 note 4 and Iam-25, policy delete: "Detach it from every principal
  first" omits the second condition: every non-default version deleted.
- Iam-07, refusal code: `MalformedPolicyDocument` is not on the simulators'
  error list; `InvalidInput` is (the message wording is for the recording).
- Iam-11, service role: `EntityAlreadyExists` is not on
  `CreateServiceLinkedRole`'s list; `InvalidInput` is, and the model's text says
  "a duplicate role name error".
- Iam-01: "the copy the console already holds for IAM and never shows" is gone
  (foundation A7). Not edited: the card is new copy and the note is a design
  note; flagged for the integrator.
- The Refused callouts' sentence "the rule comes from AWS's own service model"
  (Iam-07, 11, 14, 16, 18) is true of the `ValidationError` constraints only.
  Not edited (it is the board's chrome); the built copy must not claim it.
- Not fixed on `memory`: Iam-08's `AWSServiceRoleForAutoscaling` (AWS spells
  it `AutoScaling`: IAM-19), and Iam-10's "Up to 131 072 characters" (AWS's
  quota is smaller). Both are on the recording queue.

**Build list, in order**
1. The settings records for IAM and STS (the table above) and `setting_field`
   on every form: lines 1 to 4 from the record, line 5 where a neighbour
   exists (duration against the role's maximum, versions of five, keys of two).
   This is most of Iam-08, 09, 10, 15, 21, 24.
2. The defects that make a board unbuildable as drawn: the builder's row
   (IAM-67) and principal-type display (IAM-66); the log's principal column
   and widths (IAM-68); the list pane's "New", the chip wrap and the disabled
   style (IAM-69).
3. The page shapes: a two-column detail page for user, role, policy, group,
   profile, account and STS, with the right-hand panels' text (static where the
   board's is static: "How a group works", "Two documents, two jobs", "Reading
   the answer"; computed where it has a number in it: "Room for five. Three are
   used", the fit callout, "1 call named no resource").
4. Home and its modes: the callout per mode, "What the caller was told" and
   "How a request is decided" from `iam/errors.go` and `iamguard.Check`, the
   off-mode panel, the Last column, the short principal names, the empty-state
   card (decision above).
5. The new data, each from a call the service already answers:
   `ScopeToResources` and `UnresolvedResources` (Iam-04); the statement a draft
   matched (`MatchedStatements` of `SimulateCustomPolicy`, after IAM-15's
   reshape the console reads `ResourceSpecificResults`); "Create this as a
   policy"; the resource box on "Check this draft"; the blank-resource second
   run (Iam-05); "Used by" (`Function.Role`, `roleArn`); the unit echoes.
6. The pages the boards split out: the keys page (Iam-13), the inline-policy
   page (Iam-14), the trust page (Iam-16), the policy new-version page
   (Iam-18), Generate and Simulate as their own pages (Iam-04, 05, 06, 07),
   and the minted-credentials page (Iam-22). Each is a route under
   `routes_stackwide.go` with "Back to the ..." and an entry in the route gate.
   *Decision, recommended yes:* the shape is followed exactly; if the integrator
   would rather keep them in place, that is a written deviation per board.
7. The sentences: the three confirms (group delete, policy delete, user
   rename), IAM-26, IAM-27, IAM-28, IAM-30, IAM-32, each rewritten when its
   finding closes, never before.
8. Dark and 900 wide re-rendered for Iam-Home, 08, 09, 10, 12, 15, 17, 21, 24
   and compared with the boards again.

No behaviour change is needed for a board's sake beyond the findings above;
every item in 5 reads data the service already returns or will return once the
named finding closes.

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
| *Added 2026-10-11:* The 501 sweep | The 85 and `DecodeAuthorizationMessage` move to `notbuilt` in `testdata/modelsweep.json`; one SDK test per SDK that the caller sees the error once; a promoted operation (IAM-65) leaves the baseline (IAM-39) |
| *Added:* The Query shape | Both services: for every list member of every dispatched operation a case at the bound, over it, and a second element invalid, repeated 20 times; `cases_*.json` regenerated (IAM-41) |
| *Added:* The wire | A document with a space decodes under `unquote` (IAM-42); `GetAccountAuthorizationDetails` per `Filter` value and paged (IAM-43); every summary key (IAM-57); each list's keys against the model's "does not return" text (IAM-60) |
| *Added:* Store | Boundary at create and Get (IAM-40); case-insensitive names and a policy's name across paths (IAM-45); the implicit user for five operations, and a foreign `UserName` (IAM-44); instance-profile tags (IAM-47); version dates (IAM-58); the parameters and missing entities of IAM-46 |
| *Added:* Enforcement | `GetCallerIdentity` and `GetSessionToken` never denied, with no policy and with an explicit Deny (IAM-51); a grant on a role's ARN admits `AssumeRole`, `iam:CreateRole` on `role/app-*` (IAM-52); a bus operation resolves to a bus ARN |
| *Added:* STS | A malformed `Policy` on each of four operations; a missing `PolicyArns` entry; `SourceIdentity`, `MinimumSessionTokenSize` (IAM-53, 54); SAML members from the assertion (IAM-56); root caps (IAM-63); the session rules of IAM-70 once sessions exist |
| *Added:* Simulators | Each accepted input changes the answer or is refused; `InvalidInput` for a bad document; one result per action with `ResourceSpecificResults` (IAM-15, 48) |
| *Added:* Browser | The builder's row and principal type (IAM-66, 67); the log's principal column (IAM-68); one test per board in 6a once built, each opened beside its board's text |

## 8. Recording queue

Every claim here is `doc` or `memory` today, **except** the rows marked `model`
(2026-10-11): those are already settled by the model's text and the scenario
only confirms the code and the message wording the model does not give.

| Claim | Source now | Scenario |
|---|---|---|
| Credentials from `AssumeRole` are judged as the role session; `GetCallerIdentity` returns the assumed-role ARN | doc (*the ARN's form is `memory`; that the trust policy decides is `model`*) | `test_sts::assumed_role_identity` |
| `AssumeRole` on a missing role, and on a role that does not trust the caller: code and message | memory (*corrected 2026-10-11 from `doc`: the model's `AssumeRole` list has no `AccessDenied` or `NoSuchEntity`; its text says only "access denied" for a failed MFA condition*) | `assume_role_refusals` |
| Whether `aws:SourceIp` and `aws:UserAgent` are in the request context when a service principal makes the call (SNS delivering to a queue): doze-aws supplies loopback values on a peer hop | memory | `service_principal_condition_keys` |
| `DurationSeconds` above the role's `MaxSessionDuration`: the operation fails | model; the code and message are `memory` | `assume_role_duration_over_max` |
| A call signed with an Inactive key: code | memory (`InvalidClientTokenId`); that it is refused is `model` | `test_iam::inactive_key` |
| `UpdateUser` and `UpdateGroup` to a taken name | model (`EntityAlreadyExists`) | `rename_conflict` |
| After a rename, keys, memberships and policies follow | memory | `rename_follows` |
| Third `CreateAccessKey`: `LimitExceeded` and its message; that the number is two | model (the code), doc (two) | `access_key_limit` |
| Access key id length and prefix | memory (the model allows 16 to 128) | `access_key_shape` |
| `DeleteInstanceProfile` holding a role | model | `delete_profile_with_role` |
| `DeleteRole`, `DeleteUser`, `DeleteGroup`, `DeletePolicy`: each precondition and its message | model (the preconditions), memory (the messages) | `delete_conflicts` |
| Every write the model protects on a service-linked role: `UnmodifiableEntity`, and its message | model (the code), memory (the message) | `delete_slr_by_delete_role` |
| `CreateServiceLinkedRole`: an unknown service (`NoSuchEntity` or `InvalidInput`); a duplicate (`InvalidInput`?); the exact role name and separator | memory | `slr_names` |
| `DeleteServiceLinkedRole`: the task id's form, the status over time, the repeat returning the earlier id | model | `slr_deletion_task` |
| Policy `Version` other than the two documented | memory | `policy_version` |
| `SimulateCustomPolicy` with a statement lacking `Resource` | memory | `simulate_no_resource` |
| `SimulatePrincipalPolicy` and a permissions boundary | model | `simulate_boundary` |
| `MatchedStatements` when two statements match; the aggregate result and `ResourceSpecificResults` | model | `simulate_matched` |
| `ReadOnlyAccess` and the other offered managed policies, action by action; which names exist | memory | `managed_policy_documents` |
| Account alias: a second create (`EntityAlreadyExists` or `LimitExceeded`); deleting another name | memory (the model: one alias; both codes listed) | `account_alias` |
| The `AccessDenied` message for explicit and implicit denies, with and without a resource, per protocol | doc | `denial_wording` |
| Document size limits; `Sid` rules; attachment limits | memory (*corrected from `doc`: the model points at the quotas page and has none of the numbers*) | `policy_limits` |
| The per-resource tag maximum | doc (the quotas page the model names); the 50 per request is `model` | `tag_limits` |
| Paging on each list: `MaxItems`, `Marker`, `IsTruncated`; the default of 100 | model | `list_paging` |
| Absent key with a negated operator matches | doc | `condition_absent_key` |
| `GetAccountPasswordPolicy` with none set | model (`NoSuchEntity` is listed) | `password_policy_absent` |
| `CreateAccessKey` with no `UserName` | model | `access_key_for_caller` |
| *Added 2026-10-11:* `GetCallerIdentity` and `GetSessionToken` with a policy that denies them | model | `test_sts::no_permission_required` |
| *Added:* Policy variables in `Resource` and in `Condition`; `BinaryEquals` | memory | `policy_variables` |
| *Added:* Which AWS-managed policy names exist (`list-policies --scope AWS`), and which are `PolicyNotAttachable` | memory | `managed_policy_names` |
| *Added:* The account summary's quota values (`UsersQuota`, `AccessKeysPerUserQuota`, ...) | memory | `summary_quotas` |
| *Added:* `GenerateCredentialReport`: the states and their timing; `GetCredentialReport` while `INPROGRESS`; the report's columns | model (states, errors), memory (timing, columns) | `credential_report` |
| *Added:* `EntityTemporarilyUnmodifiable`: the window and the operations that raise it | model (existence), memory (the window) | `entity_temporarily_unmodifiable` |
| *Added:* An expired session's code, and whether it is refused at the service or at IAM | memory | `expired_session` |
| *Added:* A request that grants `sts:AssumeRole` on a role's ARN admits `AssumeRole` | model | `assume_role_granted_by_arn` |
| *Added:* `AssumeRoleWithSAML`'s `Audience`, `NameQualifier`, `SubjectType` from an assertion | model | `saml_members` |
| *Added:* A document with a space, read through boto3 (`%20`) | model | `document_encoding` |

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
| *Added 2026-10-11:* The denial's envelope per protocol: the Query services (IAM, STS, SNS) and S3 cannot read the JSON body (IAM-24) | SNS, S3 (the fix is in `dozeaws.go`; each service's stage re-runs its enforce test) |
| *Added:* `PutEvents` and the bus operations resolve to the bus ARN, not a rule's (IAM-52) | EventBridge |
| *Added:* Logs' resource (`log-group:...`) is resolved, or the ledger says every Logs request is authorised against `*` | CloudWatch Logs |
| *Added:* A function or state machine given a role ARN that does not exist is refused (`iam:PassRole`'s other half) | Lambda, Step Functions |
| *Added:* `AWS::IAM::OIDCProvider`, `AWS::IAM::SAMLProvider`, `AWS::IAM::ServiceLinkedRole` and `AWS::IAM::AccessKey` resources, if IAM-65 promotes the operations | CloudFormation |
| *Added:* A session store opened by both STS and IAM adds to the idle heap; the lightness gate is run with it | all |

## 10. Acceptance criteria

On top of every box in the protocol.

- [ ] IAM-1 to IAM-70 each closed (IAM-17 already is), with the test that failed
      first named beside it; the decisions IAM-64 and IAM-65 written down first.
- [ ] Decided and written into `SUPPORT.md`:
  - [ ] how STS reaches IAM (a peer, or a shared store) and what a session is (IAM-1, IAM-2);
  - [ ] whether web identity, SAML, federation and root sessions resolve to anything, and their tiers;
  - [ ] whether sessions survive a restart (proposed: yes, with their expiry; section 12, L9);
  - [ ] what a denied event contributes to a generated policy (IAM-7);
  - [ ] whether CloudFormation and API Gateway are guarded (IAM-18), and Logs' empty resource;
  - [ ] the unknown-key-is-root rule, as a listed difference;
  - [ ] the service-linked role service list and naming (IAM-19);
  - [ ] the opt-in strictness setting and what is under it (IAM-64);
  - [ ] which stubs are promoted to C or F (IAM-65), each with its ledger row and its console copy.
- [ ] Section 2.3 has no "not established" left: `Sid` and `Id` rules, size
      limits, service condition keys. (Policy variables, `BinaryEquals`,
      EventBridge's resource ARN and Logs' resource are established as read
      above; their AWS side is `memory` and on the recording queue.)
- [ ] Section 2.1 has no "not established" left. (*2026-10-11: the tag limit,
      the summary map's keys and `GetAccountAuthorizationDetails`' `Filter` and
      paging are now read from the model; what is left is the per-resource tag
      maximum and the quota values, `doc` and `memory`.*)
- [ ] **The fidelity table (6a) exists, was made from rendered boards beside
      rendered pages, and no row says "differs" without a decision beside it:**
      each row is built, or is a written deviation with its reason. The 26 rows
      all say "differs" today.
- [ ] **The per-service icon stays** in the rail, the list pane header and every
      resource header (the page keeps `iam.svg` where a board draws `IA` or `ST`).
- [ ] **The five teaching lines are on every setting in the table in section 6**,
      written from its `internal/settings` record, and a test holds each form
      input's min and max to the record (`TestFormConstraintsAreTheServicesOwnRange`).
- [ ] **Both services declare one protocol (`awsQuery`) and serve it on GET and
      POST**; every dispatched operation has a test with the request spelled as
      the reference spells it, IAM and STS, and a validation walk that sees the
      whole request (IAM-41).
- [ ] **Each asynchronous operation is decided (L7):** the credential report
      (states over time), the service-linked role deletion task, and the three
      service-last-accessed jobs (stay S, listed), each as modelled or as
      immediate with the difference in the ledger (section 12).
- [ ] **Every time- or consistency-based rule is on a setting, off by default**
      (IAM-64), and the ledger lists them.
- [ ] **Every refused operation answers 501 `NotImplemented` with
      `X-Doze-Not-Built`** (IAM-39), and the model sweep's baseline for `iam`
      and `sts` is only `handled` and `notbuilt`.
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
| 1 | Contract and decisions (IAM-64, IAM-65); the 501 refusal (IAM-39); ledger counts | `docs/SUPPORT.md`, `docs/not-built.md`, `testdata/modelsweep.json`, `iam/coverage_model_test.go`, `sts/coverage_model_test.go`, `iam/iam.go` (`stubActions`), `sts/sts.go`, `sts/actions.go` (`decodeAuthorizationMessage`), `internal/awshttp` (`NotBuilt`) |
| 1a | The Query shape and regenerated cases (IAM-41), with foundation B13 | `internal/modelcheck/modelcheck.go`, `iam/validate.go`, `iam/testdata/cases_iam.json`, `sts/validate.go`, `sts/testdata/cases_sts.json`, `iam/rejection_parity_test.go`, `sts/rejection_parity_test.go` |
| 2 | Red tests for IAM-1 to IAM-70 (not 17) | `iam/enforce_test.go`, `iam/sdk_test.go`, `sts/sdk_test.go`, `internal/iamguard/guard_test.go`, `resolve_test.go`, `internal/iampolicy/policy_test.go`, `resource_policy_integration_test.go`, `internal/console/*_test.go`, `e2e/tests/iam.spec.ts` |
| 3 | Sessions: STS mints into IAM, `principalFor` resolves them, trust policy evaluated, inactive keys refused | `sts/sts.go`, `sts/actions.go`, `iam/authorize.go`, `iam/store.go`, `iam/keys.go`, `dozeaws.go`, `peers/` |
| 4 | Store integrity: rename, delete guards, key limit, alias; names without case, the boundary at create, instance-profile tags, per-version dates, service-linked roles and their deletion task, the implicit user (IAM-40, 44, 45, 47, 50, 58) | `iam/store.go`, `iam/actions.go`, `iam/policies.go`, `iam/keys.go` |
| 4a | Settings records for `iam` and `sts` (section 6's table), registered as `sqs/settings.go` does | `iam/settings.go`, `sts/settings.go`, `internal/settings` |
| 5 | Guard: context keys, resolution, the denial's shape | `internal/iamguard/guard.go`, `resolve.go`, `dozeaws.go` (`authorized`, `writeDenied`), `iam/errors.go` |
| 6 | Evaluator and simulator: `Version`, boundary, matched statements, strict draft parse | `internal/iampolicy/policy.go`, `iam/simulate.go`, `iam/policy.go` |
| 7 | Recorder and generator | `iam/authorize.go` |
| 8 | Paging, service-linked roles, managed policy documents | `iam/actions.go`, `iam/policies.go`, `iam/managed.go` |
| 9 | Section 5 sweep | the test files above |
| 10 | Conformance scenarios and the claims file | `conformance/tests/test_iam.py`, `test_sts.py`, the file from foundation B1 |
| 11 | Console, in the order of 6a's build list | `internal/console/templates/iam.html` (and the pages it splits into: keys, inline, trust, new version, generate, simulate, minted), `handlers_iam.go`, `client_iam.go`, `client_sts.go`, `client_lambda.go` (the `Role` field), `routes_stackwide.go`, `static/app.css` (`.pb-*`, the log table, disabled buttons), `static/policy.js`, `copy.go`, `e2e/route-gaps.txt` |
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
*Corrected 2026-10-11: IAM-17 is already fixed (foundation B10), and its enforce
re-runs were part of that item. What replaces it as the cross-stage change is
IAM-51 and IAM-52: the middleware stops evaluating `sts:GetCallerIdentity` and
`sts:GetSessionToken`, and resolves IAM and STS resources, so each earlier
service's enforce test is re-run once for a user key and once for a role
session. And the stage is not "nearer two L": it is about 35 days (section 1),
because the console is 26 boards built from nothing in their shape and the
review found 32 API and console findings beside the 38.*

**Prior art.** `SUPPORT.md` already records that LocalStack and moto default to
permissive and that moto vendors AWS's managed policies as a generated file,
which is the alternative to synthesizing them if IAM-22 shows the patterns are
too coarse. AWS's "Policy evaluation logic" page in the IAM user guide is the
source for the order in section 2.3, and the Service Authorization Reference is
the source for which action each operation is authorized as and which condition
keys each service supplies; each hand-derived rule's test names its page.
*Added 2026-10-11 (all `memory`, none read):* moto implements OIDC and SAML
providers, the account password policy, login profiles and the credential
report as stored records, and LocalStack's IAM is moto underneath; that is the
precedent for promoting them (IAM-65) rather than refusing. How either
emulator treats a role's trust policy when it mints is not known here, so the
trust-evaluation rule (IAM-2) has no prior art to lean on and is recorded
rather than assumed.

## 12. Pre-stage review (2026-10-11)

Written before the stage starts, to the questions in
[../02-lessons-from-sqs.md](../02-lessons-from-sqs.md). **What was read:** the
documentation of every operation and every input and output member of
`.audit-models/iam.json` (180 operations) and `sts.json` (11), their error
lists with the code and status each `awsQuery` shape gives, every constrained
shape, the `statusType` and `summaryKeyType` enums, and the structure docs for
`User`, `Role`, `Policy`, `PolicyVersion`, `EvaluationResult`, `UserDetail`,
`RoleDetail` and `ManagedPolicyDetail`; `iam/` and `sts/` whole (the store, the
handlers, the validate tables, the authoriser, the simulator, the synthesiser),
`internal/iampolicy`, `internal/iamguard` (guard, context, resolve),
`internal/modelcheck` (`FromQuery`), `dozeaws.go`'s middleware, `docs/SUPPORT.md`'s
IAM section and `docs/not-built.md`, and the console's `iam.html`,
`handlers_iam.go` and clients. **What was run:** a binary built from the working
tree on three instances (soft, enforce, off) with the AWS CLI v1 (botocore
1.43.62) and `curl`; about 120 calls, each a row above; the console from an e2e
build; 26 boards and about 60 pages rendered. **Not done:** nothing was
recorded against AWS (`conformance/snapshots/` is empty), so every `memory` row
stays `memory`.

| Q | Answer |
|---|---|
| **L1** model's text before any rule | **Findings, and corrections in place.** ~55 claims in this document were corrected against the model (marked "Corrected 2026-10-11" in 2.1, 2.2, 2.3, 3, 4, 5, 6, 8): the tag bound (0 to 50 per request, per-resource number not in the model), the summary map's 33 keys, the authorization export's `Filter` and paging, the size limits (none of the three numbers is in the model: `memory`), IAM-3, 4, 11, 12, 14, 15, 16, 20 (`doc` or `memory` to `model`), IAM-10 downgraded (the model allows 16 to 128), IAM-17 closed (B10), the access-key limit's code (`model`) and its number (`doc`), the test counts, the parity counts. New: IAM-39 to IAM-70 |
| **L2** optional parameters have meanings | **Findings:** IAM-40 (`PermissionsBoundary` at create), IAM-44 (`UserName` omitted: five operations), IAM-43 (`Filter`, `MaxItems`, `Marker`), IAM-46 (`Scope`, `PolicyUsageFilter`, `PathPrefix`, `EntityFilter`), IAM-47 (`Tags` on an instance profile), IAM-48 (six simulator inputs), IAM-53 (`Policy`, `PolicyArns`, `Tags`), IAM-54 (`SourceIdentity`, `MinimumSessionTokenSize`), IAM-61 (`Description` empty), IAM-63 (a root's `DurationSeconds`). Read and found right: `ResourceArns` defaulting to `*` (and `*` meaning no particular resource), `SetAsDefault`, `OnlyAttached`, a policy's immutable description, `CreateAccessKey`'s status `Active`, the root's `GetUser` |
| **L3** error codes | **Findings:** IAM-39 (501 `NotImplemented`, and a modelled code used to say "not built"), IAM-48 (`MalformedPolicyDocument` is not on the simulators' lists), IAM-19 (`EntityAlreadyExists` is not on `CreateServiceLinkedRole`'s), IAM-46 and IAM-12 (`NoSuchEntity`, `DeleteConflict` not produced), IAM-20 (`UnmodifiableEntity`). **Checked, none:** every status the code uses matches the model's `awsQueryError` (`NoSuchEntity` 404, `EntityAlreadyExists` 409, `DeleteConflict` 409, `LimitExceeded` 409, `InvalidInput` 400, `MalformedPolicyDocument` 400, `ServiceFailure` 500); `ValidationError` for a constraint breach is `memory` (the model has no code for it) and is what both services answer |
| **L4** direction | Every row from IAM-39 on names it. Both directions are present: stricter (IAM-44, 49, 51, 52, 61), more forgiving (IAM-12, 41, 45, 46, 48, 53, 59), wrong shape or code (IAM-39, 42, 43, 50), missing (IAM-40, 47, 54, 57, 70) |
| **L5** every wire, one operation at a time | **One wire.** Both models declare only `awsQuery` (no `awsJson`, no `awsQueryCompatible`); both services serve it on GET and POST, signed and unsigned, and route an unsigned `AssumeRoleWithWebIdentity` and `GetCallerIdentity` correctly (run). **`xmlName` and `xmlFlattened`: none in either model** (0 members of 191 operations' shapes); the maps (`SummaryMap`, `EvalDecisionDetails`, `Properties`, `ReplacementValues`, `Certifications`) are the non-flattened `entry`/`key`/`value` form and the code's `SummaryMap` is right. **Findings:** IAM-41 (the Query list walk, B13), IAM-42 (encoding), IAM-40, 43, 47, 57, 60 (response members) |
| **L6** tokens are per use | **Findings:** IAM-16 (`Marker` 1 to 320, default page of 100, `IsTruncated`), IAM-50 (`DeletionTaskId`'s form and the repeat returning the earlier id), IAM-54 (the session token's size is a request and a response member), IAM-10 (the key id). **Not in the model:** what a stale or foreign `Marker` does (`InvalidInput` is listed on some lists and not others), what a foreign `JobId` does (a stub), what a session token does after the role is deleted. All three go on the recording queue; `awspage`'s cursor is the entry's key, so a listing that changes between pages moves nothing. A policy `VersionId` is `v[1-9][0-9]*` with an optional suffix; the store hands out `v1`, `v2`, and does not reuse a deleted number (right) |
| **L7** asynchronous means observable | **Decided, proposed:** (1) the **service-linked role deletion task** is modelled: `NOT_STARTED` then `IN_PROGRESS` for a short visible window (one second by default, so a poll sees it), then `SUCCEEDED` and the role is gone; a repeat while it runs returns the same id; a finished record persists, a running one does not (IAM-50). (2) The **credential report** (if IAM-65 says yes) is modelled: `STARTED`, `INPROGRESS`, `COMPLETE` over about two seconds, `ReportInProgress` while it runs, `ReportNotPresent` before one was ever made; `ReportExpired` after four hours is `memory` and not modelled. (3) The three **service-last-accessed** jobs and the **Organizations access report** stay S (derived from CloudTrail); their states are listed, not modelled. (4) **Delegation requests** stay S. (5) **Eventual consistency** ("wait for the change to appear across all of AWS") is **immediate** and listed as a difference; there is no honest delay to model. (6) A session's **expiry**: IAM-64. (7) `GetAccessKeyLastUsed` is updated in the request; AWS's lag is `memory` and listed |
| **L8** latency rules are opt-in strictness | **One switch, `--iam-strict` / `[iam] strict = true`, off by default, state in memory** (IAM-64), mirroring `--sqs-strict`: `EntityTemporarilyUnmodifiable` (a user name deleted and recreated, on `UpdateUser`; the "several minutes" is `memory`), and a session past its `Expiration` refused (the code is `memory`). Eventual consistency is not under it (nothing to switch on). The default stays "more forgiving" because a minute in every local test loop is a worse tool |
| **L9** state in memory unless losing it changes what the application sees | **The store** (users, groups, roles, policies and versions, keys, profiles, alias): persisted, as today. **Sessions** (new): **persisted with their expiry**, because they are the application's credentials and doze reaps an idle backend after five minutes; losing them would end a session an app still holds. **The access log and the generator's input**: memory, as today and as the ledger says (the process's). **Deletion tasks**: finished records persist, running ones are memory-only (as SQS move tasks). **Credential report**: persisted (one record the user asked for). **Observation to measure, not a finding:** `TouchAccessKey` writes the store on every request signed with a user's key; under the load test that is a bolt write per request, where a coalesced write (at most one a second) would change nothing the application sees |
| **L10** the board is the spec | **Table 6a. 26 of 26 boards differ in shape or content; the facts of five boards were wrong and are corrected in the working copy (Iam-07, 11, 17, 19, 25).** Rendered light for all, dark and 900 wide for Home, 08, 09, 10, 12, 15, 17, 21, 24. Defects found by looking: IAM-66, 67, 68, 69 |
| **L11** the five teaching lines | **None built.** The settings table in section 6 lists 21 settings, each with its lines 1 to 4 from the model and line 5 where a neighbour exists. `internal/settings` has no `iam` or `sts` record; plan step 4a adds them. The `setting_field` kit exists |
| **L12** icons | `iam.svg` is in the rail, the list pane header and every resource header, and stays. The boards draw `IA` and (STS) `ST`: the page keeps `iam.svg` for both; there is no STS icon in `static/aws/`. Nothing else about a board's chrome is exempt |

**Classes of mistake this service is most at risk of**
1. **Accepted and dropped.** The tables validate; the handlers read members by
   hand, and several are never read (IAM-40, 47, 48, 53, 54, 61). A reflection
   test that every input member of every dispatched operation is read by the
   handler, or listed with a reason, would have found most of them.
2. **Grammar and numbers written from memory.** The model has no policy grammar
   and no size quota. Every claim about variables, `Version`, `Sid`, sizes,
   managed-policy names and the quota values is `memory`. The risk is a stage
   that "fixes" one without a recording, as SQS first fixed 256 KiB.
3. **A design change hidden in an F row.** STS sessions (IAM-1, 2, 3, 51, 53,
   54, 70) change `principalFor`, the store, the guard's input and the weight.
   The row says F today. Size it as its own piece of work (6 of the 35 days).
4. **Content without shape, again.** The page is "complete" if one counts
   buttons. Nothing is finished until each board is rendered beside its page.
5. **Honest refusals that are not.** 85 operations answer a code the SDKs treat
   as a client error, and the denial's body is unreadable to the Query clients
   of the two services this stage owns (IAM-24, 39).
6. **Case.** Names that do not distinguish case touch every store and every
   lookup; it is a small edit in one place that is easy to apply to three of
   four kinds (IAM-45).
7. **Weight.** A session store opened by two services, and a bolt write per
   signed request, are both in the idle-heap and the load gates' path.

**Decisions needed from a person** (each is a row above with a recommendation)
1. IAM-65: promote OIDC providers, SAML providers, the password policy, login
   profiles, the credential report and the deletion status. Recommended yes.
2. IAM-64: the strictness setting and what is under it. Recommended as written.
3. Sessions: persisted with their expiry (L9), and whether an expired session
   is refused by default (recommended: no; under the switch).
4. Whether the boards that split a page out (keys, inline, trust, new version,
   generate, simulate, minted) are built as pages (recommended, "the shape is
   followed exactly") or kept in place with a written deviation each.
5. Whether an empty principal list gets a card although the log always exists
   (Iam-01; recommended yes).
6. `GetUser` with no name for the account root: the model gives no root user's
   fields; keep the fixed root, and make its `CreateDate` the data directory's
   creation time rather than 1970 (`memory` for the shape).
