# What doze-aws does not build, and why

Every absence on this page is deliberate, and every one is also an S-tier row in
[docs/api-support/](api-support/). A test reconciles the two in both directions,
so this page cannot fall behind the code without the build failing: a new
refusal with no verdict fails, and a verdict for something that has since been
implemented fails too.

**The distinction that matters is declined versus not yet**, and they are kept
in separate sections because they are different promises. A declined operation
is one where building it would mean pretending — emulating DNS that does not
resolve, an identity provider that does not exist, an account that is not there.
A deferred one is just work nobody has done, and it says what the work is.

Nothing here is a judgement about the operation. Most of these are good APIs
doing exactly what they should on AWS; they have nothing to act on when AWS is a
binary on your laptop.

## What it does not do, at all

For the person reviewing whether this is safe to run:

- **No outbound connections of its own.** It serves requests and calls its own
  services. It does not phone home, check for updates, or report usage — the
  only `https://` anywhere in `cmd/doze-aws` is a URL in the help text,
  and a test keeps it that way.
- **No account, no sign-up, no token.** Nothing to register, nothing to expire.
- **No telemetry and no analytics**, in the binary or the console.
- **Nothing outside the data directory you name.** Delete it and the state is
  gone; there is no other store, cache or profile.
- **Apache 2.0**, and it runs fully offline.

## Declined


81 entries below, covering about 336 operations across 18 services. A row is one decision, which is often a family — "Custom domains and base path mappings (12 operations)" is one argument, not twelve.

### It acts on cloud infrastructure

| Service | What is absent | Why |
|---|---|---|
| apigateway | Client certificates (5 operations) | certificate material is cloud infrastructure |
| apigateway | Custom domains and base path mappings (12 operations) | there is no DNS or TLS termination locally |
| apigateway | SDK and export generation (5 operations) | code generation is a cloud-side service |
| apigateway | VPC links (5 operations) | there is no VPC locally |
| apigatewayv2 | Domain names and API mappings (11 operations) | there is no DNS or TLS termination locally |
| apigatewayv2 | Portals, portal products, product pages, routing rules (26 operations) | the developer-portal surface has no local counterpart |
| apigatewayv2 | VPC links (5 operations) | there is no VPC locally |
| cloudformation | EstimateTemplateCost | there is no pricing API locally |
| cloudformation | Extension registry (14 operations) | RegisterType, PublishType, hooks and type configuration are cloud infrastructure |
| cloudformation | Generated templates (6 operations) | template generation reads a real account |
| cloudformation | Organizations access (3 operations) | there is no Organizations locally |
| cloudformation | RecordHandlerProgress | used by registry resource providers |
| cloudformation | Resource scanning (5 operations) | scanning reads a real account |
| cloudformation | SignalResource | there are no EC2 instances to signal |
| cloudformation | Stack refactoring (5 operations) | a cloud-side operation |
| cloudformation | StackSets (17 operations) | StackSets need Organizations |
| cloudwatch | Datasets and OTel enrichment (GetDataset, Associate/DisassociateDatasetKmsKey), PutLogAlarm, GetMetricWidgetImage | need services or a renderer that do not run locally |
| cloudwatch | Metric streams (Put/Get/Delete/List, Start/Stop) | stream metrics to a Firehose that does not exist locally |
| dynamodb | Global tables, DAX, Kinesis destinations | multi-region/cloud infrastructure |
| eventbridge | Partner event sources, global endpoints, cross-account permissions, schemas registry | cloud infrastructure |
| iam | Credential and access reports (5 operations) | derived from CloudTrail history |
| iam | Login profiles and password policy (7 operations) | there is no console sign-in locally |
| iam | MFA devices (8 operations) | there is no MFA fleet locally |
| iam | Organizations and delegation (16 operations) | there is no Organizations locally |
| iam | SAML and OIDC providers (17 operations) | federation needs a real identity provider |
| iam | Server, signing and SSH credentials (12 operations) | certificate material is cloud infrastructure |
| iam | Service-specific credentials (5 operations) | for CodeCommit and Keyspaces, which doze-aws does not serve |
| kms | Custom key stores, ImportKeyMaterial, multi-region replication, DeriveSharedSecret | cloud-infrastructure-only |
| lambda | Container images, SnapStart, provisioned concurrency semantics, code signing | config accepted where trivial; execution semantics are cloud-only |
| logs | Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens | govern an account, not a local store |
| logs | Destinations (PutDestination, PutDestinationPolicy, DescribeDestinations, DeleteDestination) | cross-account receivers for another account's filters; subscribe a function or a stream directly |
| logs | Transformers, integrations, S3 Table sources, KMS association, syslog configurations | need pipelines or services that do not run locally |
| s3 | Analytics configuration (4 operations) | analytics exports read a real account |
| s3 | CreateSession / ListDirectoryBuckets | directory buckets are an express-zone feature |
| s3 | GetBucketAbac / PutBucketAbac | attribute-based access control needs IAM on the S3 path |
| s3 | GetObjectTorrent | BitTorrent distribution is a cloud feature |
| s3 | Intelligent-tiering configuration (4 operations) | there are no storage tiers locally |
| s3 | Inventory configuration (4 operations) | inventory reports read a real account |
| s3 | Metadata and metadata-table configuration (9 operations) | metadata tables are a managed analytics feature |
| s3 | Object annotations (4 operations) | a metadata-table feature with no local store |
| s3 | RenameObject | a directory-bucket operation |
| s3 | RestoreObject | there is no Glacier tier locally |
| s3 | WriteGetObjectResponse | S3 Object Lambda is a cloud feature |
| secretsmanager | ReplicateSecretToRegions / RemoveRegionsFromReplication / StopReplicationToReplica | exactly one region locally |
| sns | Mobile push (Platform applications/endpoints), SMS + sandbox, phone-number opt-out ops | carrier/platform infrastructure cannot exist locally; each answers a clean coded error |
| ssm | Documents, Automation, Run Command, Sessions, fleet/instances, associations, patching, inventory, compliance, maintenance windows, OpsCenter, resource data sync, service settings | need managed instances / agent infrastructure that does not exist locally; each answers UnsupportedOperationException |

### There is nothing here for it to do

| Service | What is absent | Why |
|---|---|---|
| apigateway | GetUsage / UpdateUsage | doze-aws does not meter requests, so there is no usage to report or reset |
| cloudformation | CancelUpdateStack | apply is synchronous, so there is never an update in flight to cancel |
| cloudformation | ContinueUpdateRollback / RollbackStack | same reason: nothing is ever mid-flight |
| cloudformation | DescribeAccountLimits | there are no account limits locally |
| cloudformation | DescribeStackDriftDetectionStatus / DescribeStackResourceDrifts | as above |
| cloudformation | DetectStackDrift / DetectStackResourceDrift / DetectStackSetDrift | there is nothing to drift from locally |
| eventbridge | CancelReplay | local replays complete synchronously, so there is never a running replay to cancel |
| kinesis | UpdateStreamWarmThroughput | a capacity hint with no local meaning |
| s3 | Metrics configuration (4 operations) | request metrics need an AWS/S3 producer; CloudWatch is local now, but S3 publishes nothing to it |
| s3 | UpdateObjectEncryption | objects are stored under the data directory, not KMS-enveloped |
| sts | DecodeAuthorizationMessage | doze-aws never produces encoded authorization messages, so there is nothing to decode |

### It is a different product wearing this API

| Service | What is absent | Why |
|---|---|---|
| apigateway | Documentation parts and versions (10 operations) | documentation exports are a publishing feature |
| apigatewayv2 | ImportApi / ReimportApi / ExportApi | OpenAPI import and export are a publishing feature |
| cloudwatch | Anomaly detection (PutAnomalyDetector, DescribeAnomalyDetectors, DeleteAnomalyDetector, ThresholdMetricId, the anomaly comparison operators) | need a trained band to compare against |
| cloudwatch | Insight rules (Put/Delete/Describe/Enable/Disable, managed rules, GetInsightRuleReport) | Contributor Insights reads an account's log volume |
| cloudwatch | Metric math (Metrics on an alarm, Expression on a query) | a single MetricStat is accepted, which is what CDK emits for a threshold alarm; an expression needs an evaluator doze-aws does not have |
| logs | Anomaly detectors, anomalies | a trained model over an account's logs; not built |
| logs | Logs Insights (StartQuery, GetQueryResults, query definitions, scheduled queries, lookup tables, log fields and records) | a query engine that does not exist locally; FilterLogEvents covers what a developer reads |
| s3 | SelectObjectContent | S3 Select is a query engine, and AWS has discontinued it |

### It needs a transport doze-aws does not serve

| Service | What is absent | Why |
|---|---|---|
| kinesis | SubscribeToShard | enhanced fan-out delivers over an HTTP/2 event stream; register the consumer and poll GetRecords instead |
| logs | StartLiveTail | an HTTP event stream to a tailer fleet; aws logs tail --follow polls FilterLogEvents, which works |

### Something local already covers it

| Service | What is absent | Why |
|---|---|---|
| apigateway | ImportApiKeys | reads a CSV of keys; create them one at a time |
| apigatewayv2 | DeleteRouteRequestParameter | update the route's requestParameters instead |
| apigatewayv2 | Route responses and integration responses (10 operations) | WebSocket API machinery; an HTTP API answers with its integration's response |
| cloudformation | DescribeEvents | an alias for DescribeStackEvents that no current SDK emits |
| dynamodb | Backups / exports / imports / PITR restore | copy the data directory instead |
| kms | Grants (Create/Retire/Revoke/List) | grants are IAM machinery |

---

## Not yet

No argument against these. What each would take:

| Service | What is absent | What it would take |
|---|---|---|
| apigateway | Gateway responses (4 operations) | response templating on the refusal path; the shapes are stored, nothing reads them |
| apigateway | Request validators and models (10 operations) | a schema validator on the request path, and a reason to run it — nothing local consumes the models today |
| apigatewayv2 | Models and GetModelTemplate (6 operations) | the same schema validator the REST side wants; one implementation would serve both |
| cloudwatch | Alarm mute rules (Put/Get/List/Delete) | a suppression schedule consulted by the alarm evaluator |
| cloudwatch | Alarm warm-up (WarmUpConfiguration) | suppressing evaluation for a window; refused by name today because evaluating anyway would fire an alarm the caller asked to be held back |
| cloudwatch | Composite alarms (PutCompositeAlarm, DescribeAlarmContributors) | an evaluator over other alarms' states; the metric-alarm evaluator is the half that exists |
| logs | Deliveries, delivery sources and destinations, configuration templates | a vended-logs path from the services that emit them; the subscription path it would reuse already works |
| logs | Export and import tasks | batch jobs writing to the local S3, which is the one dependency already present |

---

## How to read a row

The reason in each row is the one in that service's ledger — this page shows the
category and points at the single source rather than copying eighty-one reasons
into a second place that could then disagree with the first.

If something here is blocking you, it is worth raising. "Declined" is a judgement
about what can be honestly emulated on one machine, not a refusal to discuss it,
and two of the arguments — *nothing here for it to do* and *something local
already covers it* — stop being true the moment a local counterpart exists.
