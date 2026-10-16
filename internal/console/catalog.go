package console

// The catalogue: the one list of services the console knows.
//
// There were five copies of this and none of them agreed. The rail hand-wrote
// thirteen entries; apiCounts counted thirteen; the palette's NAV knew nine,
// its ACTS eight, its KIND nine and its SVCSET nine; and the 404's nearest-match
// had its own. So Kinesis, CloudFormation, API Gateway and IAM were unreachable
// from ⌘K even though each has a working page — and the palette still offered
// "Flows", a page deleted commits ago, because nothing connected the list to the
// router.
//
// One list, read by all of them, so that cannot happen again.

type svcEntry struct {
	Key   string // console key: colour, icon, URL segment
	Label string // what the rail and the palette call it
	Group string // rail heading; "" pins it above the first heading
	Noun  string // what one of its resources IS, for palette result kinds
	// Plural heads the service's list pane, as the service's Home board writes
	// it ("Queues", "Streams", "State machines"). IAM's board heads its pane
	// "IAM": the pane holds principals, policies and an access log, not one kind.
	Plural string
	// Item is what one row of the list pane is, for the key bar's "j k queue"
	// and "c new queue". Empty means Noun; EventBridge's Noun names a bus and a
	// rule, and its list pane holds buses.
	Item string
	// CreatePath is prefix-relative and empty when the service has no create
	// form — CloudFormation and API Gateway are deploy targets, and offering a
	// button that does not exist is worse than offering none.
	CreatePath  string
	CreateLabel string
}

var catalog = []svcEntry{
	{Key: "s3", Label: "S3", Group: "Storage & data", Noun: "bucket", Plural: "Buckets",
		CreatePath: "/s3/create", CreateLabel: "Create bucket"},
	{Key: "ddb", Label: "DynamoDB", Group: "Storage & data", Noun: "table", Plural: "Tables",
		CreatePath: "/ddb/create", CreateLabel: "Create table"},

	{Key: "sqs", Label: "SQS", Group: "Messaging", Noun: "queue", Plural: "Queues",
		CreatePath: "/sqs/create", CreateLabel: "Create queue"},
	{Key: "sns", Label: "SNS", Group: "Messaging", Noun: "topic", Plural: "Topics",
		CreatePath: "/sns/create", CreateLabel: "Create topic"},
	{Key: "eb", Label: "EventBridge", Group: "Messaging", Noun: "bus / rule", Plural: "Event buses", Item: "bus",
		CreatePath: "/eb/create-bus", CreateLabel: "Create event bus"},
	{Key: "kinesis", Label: "Kinesis", Group: "Messaging", Noun: "stream", Plural: "Streams",
		CreatePath: "/kinesis/create", CreateLabel: "Create stream"},

	{Key: "lambda", Label: "Lambda", Group: "Compute & APIs", Noun: "function", Plural: "Functions",
		CreatePath: "/lambda/create", CreateLabel: "Create function"},
	{Key: "apigw", Label: "API Gateway", Group: "Compute & APIs", Noun: "API", Plural: "APIs",
		CreatePath: "/apigw/create", CreateLabel: "Create API"},
	{Key: "sfn", Label: "Step Functions", Group: "Compute & APIs", Noun: "state machine", Plural: "State machines",
		CreatePath: "/sfn/create", CreateLabel: "Create state machine"},
	{Key: "logs", Label: "CloudWatch Logs", Group: "Compute & APIs", Noun: "log group", Plural: "Log groups",
		CreatePath: "/logs/create", CreateLabel: "Create log group"},
	{Key: "cw", Label: "CloudWatch", Group: "Compute & APIs", Noun: "alarm", Plural: "Alarms",
		CreatePath: "/cw/create", CreateLabel: "Create alarm"},

	{Key: "kms", Label: "KMS", Group: "Config & secrets", Noun: "key", Plural: "Keys",
		CreatePath: "/kms/create", CreateLabel: "Create key"},
	{Key: "sm", Label: "Secrets Manager", Group: "Config & secrets", Noun: "secret", Plural: "Secrets",
		CreatePath: "/sm/create", CreateLabel: "Store secret"},
	{Key: "ssm", Label: "Parameter Store", Group: "Config & secrets", Noun: "parameter", Plural: "Parameters",
		CreatePath: "/ssm/create", CreateLabel: "Create parameter"},

	{Key: "iam", Label: "IAM", Group: "Stack-wide", Noun: "principal", Plural: "IAM",
		CreatePath: "/iam/create", CreateLabel: "Create principal"},
	{Key: "cfn", Label: "CloudFormation", Group: "Stack-wide", Noun: "stack", Plural: "Stacks",
		CreatePath: "/cfn/create", CreateLabel: "Create stack"},
}

// surfaces are the pages that are not a service. They lead the rail with no
// heading above them, because neither is a resource and Connect is the first
// thing a new user needs.
var surfaces = []svcEntry{
	{Key: "traffic", Label: "The wire", Noun: "surface"},
	{Key: "connect", Label: "Connect", Noun: "surface"},
}

func catalogOf(key string) (svcEntry, bool) {
	for _, e := range catalog {
		if e.Key == key {
			return e, true
		}
	}
	return svcEntry{}, false
}

// nounFor is what one of a service's resources is called, for a palette result's
// kind label. Empty for anything not in the catalogue.
func nounFor(key string) string {
	e, _ := catalogOf(key)
	return e.Noun
}
