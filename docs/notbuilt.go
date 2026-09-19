package docs

// What doze-aws does not build, and why — the classification behind
// docs/not-built.md.
//
// # Why this exists at all
//
// Not-built was only ever expressed as S-tier rows scattered through eighteen
// per-service ledgers. Every row carries a reason and the reasons are good, but
// there was no page answering the question an evaluator actually asks, which is
// not "what does SSM refuse" but "what kind of thing does this project decline,
// and is the thing I need in that set".
//
// # Why a verdict here and the reason in the ledger
//
// The obvious register duplicates all eighty-one reasons onto one page, which
// is the same third-copy mistake the totals assertion avoided: two places to
// edit, one of which goes stale. The reason stays in the ledger, which is its
// single source and where the console already reads it from. What this adds is
// the CATEGORY — the reusable half — plus the cross-service view, and a test
// that keeps the two reconciled in both directions.
//
// # The verdicts
//
// Derived from the eighty-one reasons already in the tree rather than invented,
// and deliberately NOT the four in console/coverage_test.go's exempt map. Those
// answer "why no console form for something doze-aws fully implements", which is
// a different question; reusing the words would put one label on two arguments.

import "fmt"

// A verdict is the argument for an operation's absence.
type verdict string

const (
	// cloudOnly: the thing it acts on is cloud infrastructure that cannot
	// exist on a laptop — DNS and TLS termination, Organizations, a VPC, an
	// MFA fleet, a real identity provider, another account.
	cloudOnly verdict = "cloudOnly"
	// nothingToReport: a well-defined operation whose subject is vacuous here.
	// Apply is synchronous, so no update is ever in flight to cancel; nothing
	// meters requests, so there is no usage to report.
	nothingToReport verdict = "nothingToReport"
	// secondProduct: implementable, but what it needs is a query engine, a
	// trained model or a renderer — a different product wearing this API.
	secondProduct verdict = "secondProduct"
	// unservedWire: needs a transport doze-aws does not serve, such as an
	// HTTP/2 event stream.
	unservedWire verdict = "unservedWire"
	// useThisInstead: a local equivalent covers it, and the entry names it.
	useThisInstead verdict = "useThisInstead"
	// notYet: no argument against building it. The only verdict in the "Not
	// yet" section, and the only one that must say what it would take.
	notYet verdict = "notYet"
)

var verdicts = []verdict{cloudOnly, nothingToReport, secondProduct, unservedWire, useThisInstead, notYet}

func (v verdict) heading() string {
	switch v {
	case cloudOnly:
		return "It acts on cloud infrastructure"
	case nothingToReport:
		return "There is nothing here for it to do"
	case secondProduct:
		return "It is a different product wearing this API"
	case unservedWire:
		return "It needs a transport doze-aws does not serve"
	case useThisInstead:
		return "Something local already covers it"
	case notYet:
		return "Not yet"
	}
	return string(v)
}

// entry is one absence: its verdict, and for notYet what building it would take.
type entry struct {
	verdict verdict
	// takes is required for notYet and must be empty otherwise — a declined
	// entry that quietly carries a roadmap is a decision nobody made.
	takes string
}

// notBuilt maps "<service>/<the ledger's Operation cell, verbatim>" to a
// verdict.
//
// Keyed on the cell text, which is brittle to copy-editing and deliberately so:
// that brittleness is the ratchet, the same bargain knownGaps already makes.
// Editing a ledger row's wording fails this until the key follows.
var notBuilt = map[string]entry{
	// ---- API Gateway (REST) ----
	"apigateway/GetUsage / UpdateUsage":                                {verdict: nothingToReport},
	"apigateway/ImportApiKeys":                                         {verdict: useThisInstead},
	"apigateway/Custom domains and base path mappings (12 operations)": {verdict: cloudOnly},
	"apigateway/Client certificates (5 operations)":                    {verdict: cloudOnly},
	"apigateway/VPC links (5 operations)":                              {verdict: cloudOnly},
	"apigateway/Request validators and models (10 operations)": {verdict: notYet,
		takes: "a schema validator on the request path, and a reason to run it — " +
			"nothing local consumes the models today"},
	"apigateway/Documentation parts and versions (10 operations)": {verdict: secondProduct},
	"apigateway/SDK and export generation (5 operations)":         {verdict: cloudOnly},
	"apigateway/Gateway responses (4 operations)": {verdict: notYet,
		takes: "response templating on the refusal path; the shapes are stored, nothing reads them"},

	// ---- API Gateway (HTTP) ----
	"apigatewayv2/Domain names and API mappings (11 operations)": {verdict: cloudOnly},
	"apigatewayv2/VPC links (5 operations)":                      {verdict: cloudOnly},
	"apigatewayv2/Models and GetModelTemplate (6 operations)": {verdict: notYet,
		takes: "the same schema validator the REST side wants; one implementation would serve both"},
	"apigatewayv2/Route responses and integration responses (10 operations)":              {verdict: useThisInstead},
	"apigatewayv2/DeleteRouteRequestParameter":                                            {verdict: useThisInstead},
	"apigatewayv2/ImportApi / ReimportApi / ExportApi":                                    {verdict: secondProduct},
	"apigatewayv2/Portals, portal products, product pages, routing rules (26 operations)": {verdict: cloudOnly},

	// ---- CloudFormation ----
	"cloudformation/CancelUpdateStack":                                                 {verdict: nothingToReport},
	"cloudformation/ContinueUpdateRollback / RollbackStack":                            {verdict: nothingToReport},
	"cloudformation/DetectStackDrift / DetectStackResourceDrift / DetectStackSetDrift": {verdict: nothingToReport},
	"cloudformation/DescribeStackDriftDetectionStatus / DescribeStackResourceDrifts":   {verdict: nothingToReport},
	"cloudformation/StackSets (17 operations)":                                         {verdict: cloudOnly},
	"cloudformation/Extension registry (14 operations)":                                {verdict: cloudOnly},
	"cloudformation/Resource scanning (5 operations)":                                  {verdict: cloudOnly},
	"cloudformation/Generated templates (6 operations)":                                {verdict: cloudOnly},
	"cloudformation/Stack refactoring (5 operations)":                                  {verdict: cloudOnly},
	"cloudformation/Organizations access (3 operations)":                               {verdict: cloudOnly},
	"cloudformation/EstimateTemplateCost":                                              {verdict: cloudOnly},
	"cloudformation/DescribeAccountLimits":                                             {verdict: nothingToReport},
	"cloudformation/SignalResource":                                                    {verdict: cloudOnly},
	"cloudformation/DescribeEvents":                                                    {verdict: useThisInstead},
	"cloudformation/RecordHandlerProgress":                                             {verdict: cloudOnly},

	// ---- CloudWatch ----
	"cloudwatch/Metric math (Metrics on an alarm, Expression on a query)": {verdict: secondProduct},
	"cloudwatch/Composite alarms (PutCompositeAlarm, DescribeAlarmContributors)": {verdict: notYet,
		takes: "an evaluator over other alarms' states; the metric-alarm evaluator is the half that exists"},
	"cloudwatch/Anomaly detection (PutAnomalyDetector, DescribeAnomalyDetectors, DeleteAnomalyDetector, ThresholdMetricId, the anomaly comparison operators)": {verdict: secondProduct},
	"cloudwatch/Alarm warm-up (WarmUpConfiguration)": {verdict: notYet,
		takes: "suppressing evaluation for a window; refused by name today because evaluating " +
			"anyway would fire an alarm the caller asked to be held back"},
	"cloudwatch/Insight rules (Put/Delete/Describe/Enable/Disable, managed rules, GetInsightRuleReport)": {verdict: secondProduct},
	"cloudwatch/Metric streams (Put/Get/Delete/List, Start/Stop)":                                        {verdict: cloudOnly},
	"cloudwatch/Alarm mute rules (Put/Get/List/Delete)": {verdict: notYet,
		takes: "a suppression schedule consulted by the alarm evaluator"},
	"cloudwatch/Datasets and OTel enrichment (GetDataset, Associate/DisassociateDatasetKmsKey), PutLogAlarm, GetMetricWidgetImage": {verdict: cloudOnly},

	// ---- DynamoDB ----
	"dynamodb/Global tables, DAX, Kinesis destinations":   {verdict: cloudOnly},
	"dynamodb/Backups / exports / imports / PITR restore": {verdict: useThisInstead},

	// ---- EventBridge ----
	"eventbridge/CancelReplay": {verdict: nothingToReport},
	"eventbridge/Partner event sources, global endpoints, cross-account permissions, schemas registry": {verdict: cloudOnly},

	// ---- IAM ----
	"iam/MFA devices (8 operations)":                          {verdict: cloudOnly},
	"iam/SAML and OIDC providers (17 operations)":             {verdict: cloudOnly},
	"iam/Server, signing and SSH credentials (12 operations)": {verdict: cloudOnly},
	"iam/Login profiles and password policy (7 operations)":   {verdict: cloudOnly},
	"iam/Credential and access reports (5 operations)":        {verdict: cloudOnly},
	"iam/Organizations and delegation (16 operations)":        {verdict: cloudOnly},
	"iam/Service-specific credentials (5 operations)":         {verdict: cloudOnly},

	// ---- Kinesis ----
	"kinesis/SubscribeToShard":           {verdict: unservedWire},
	"kinesis/UpdateStreamWarmThroughput": {verdict: nothingToReport},

	// ---- KMS ----
	"kms/Grants (Create/Retire/Revoke/List)":                                                 {verdict: useThisInstead},
	"kms/Custom key stores, ImportKeyMaterial, multi-region replication, DeriveSharedSecret": {verdict: cloudOnly},

	// ---- Lambda ----
	"lambda/Container images, SnapStart, provisioned concurrency semantics, code signing": {verdict: cloudOnly},

	// ---- CloudWatch Logs ----
	"logs/Logs Insights (StartQuery, GetQueryResults, query definitions, scheduled queries, lookup tables, log fields and records)": {verdict: secondProduct},
	"logs/StartLiveTail": {verdict: unservedWire},
	"logs/Destinations (PutDestination, PutDestinationPolicy, DescribeDestinations, DeleteDestination)": {verdict: cloudOnly},
	"logs/Deliveries, delivery sources and destinations, configuration templates": {verdict: notYet,
		takes: "a vended-logs path from the services that emit them; the subscription path it " +
			"would reuse already works"},
	"logs/Export and import tasks": {verdict: notYet,
		takes: "batch jobs writing to the local S3, which is the one dependency already present"},
	"logs/Anomaly detectors, anomalies": {verdict: secondProduct},
	"logs/Account, data-protection, index, storage-tier, resource and deletion-protection policies, bearer tokens": {verdict: cloudOnly},
	"logs/Transformers, integrations, S3 Table sources, KMS association, syslog configurations":                    {verdict: cloudOnly},

	// ---- S3 ----
	"s3/GetObjectTorrent":                                         {verdict: cloudOnly},
	"s3/RestoreObject":                                            {verdict: cloudOnly},
	"s3/SelectObjectContent":                                      {verdict: secondProduct},
	"s3/RenameObject":                                             {verdict: cloudOnly},
	"s3/UpdateObjectEncryption":                                   {verdict: nothingToReport},
	"s3/Object annotations (4 operations)":                        {verdict: cloudOnly},
	"s3/Analytics configuration (4 operations)":                   {verdict: cloudOnly},
	"s3/Inventory configuration (4 operations)":                   {verdict: cloudOnly},
	"s3/Metrics configuration (4 operations)":                     {verdict: nothingToReport},
	"s3/Intelligent-tiering configuration (4 operations)":         {verdict: cloudOnly},
	"s3/Metadata and metadata-table configuration (9 operations)": {verdict: cloudOnly},
	"s3/GetBucketAbac / PutBucketAbac":                            {verdict: cloudOnly},
	"s3/CreateSession / ListDirectoryBuckets":                     {verdict: cloudOnly},
	"s3/WriteGetObjectResponse":                                   {verdict: cloudOnly},

	// ---- Secrets Manager ----
	"secretsmanager/ReplicateSecretToRegions / RemoveRegionsFromReplication / StopReplicationToReplica": {verdict: cloudOnly},

	// ---- SNS ----
	"sns/Mobile push (Platform applications/endpoints), SMS + sandbox, phone-number opt-out ops": {verdict: cloudOnly},

	// ---- SSM ----
	"ssm/Documents, Automation, Run Command, Sessions, fleet/instances, associations, patching, inventory, compliance, maintenance windows, OpsCenter, resource data sync, service settings": {verdict: cloudOnly},

	// ---- STS ----
	"sts/DecodeAuthorizationMessage": {verdict: nothingToReport},
}

// key is how a ledger row addresses its register entry.
func key(svc, ops string) string { return fmt.Sprintf("%s/%s", svc, ops) }
