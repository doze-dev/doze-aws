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
	// CreatePath is prefix-relative and empty when the service has no create
	// form — CloudFormation and API Gateway are deploy targets, and offering a
	// button that does not exist is worse than offering none.
	CreatePath  string
	CreateLabel string
}

var catalog = []svcEntry{
	{Key: "s3", Label: "S3", Group: "Storage & data", Noun: "bucket",
		CreatePath: "/s3/create", CreateLabel: "Create bucket"},
	{Key: "ddb", Label: "DynamoDB", Group: "Storage & data", Noun: "table",
		CreatePath: "/ddb/create", CreateLabel: "Create table"},

	{Key: "sqs", Label: "SQS", Group: "Messaging", Noun: "queue",
		CreatePath: "/sqs/create", CreateLabel: "Create queue"},
	{Key: "sns", Label: "SNS", Group: "Messaging", Noun: "topic",
		CreatePath: "/sns/create", CreateLabel: "Create topic"},
	{Key: "eb", Label: "EventBridge", Group: "Messaging", Noun: "bus / rule",
		CreatePath: "/eb/create-bus", CreateLabel: "Create event bus"},
	{Key: "kinesis", Label: "Kinesis", Group: "Messaging", Noun: "stream",
		CreatePath: "/kinesis/create", CreateLabel: "Create stream"},

	{Key: "lambda", Label: "Lambda", Group: "Compute & APIs", Noun: "function",
		CreatePath: "/lambda/create", CreateLabel: "Create function"},
	{Key: "apigw", Label: "API Gateway", Group: "Compute & APIs", Noun: "API"},

	{Key: "kms", Label: "KMS", Group: "Config & secrets", Noun: "key",
		CreatePath: "/kms/create", CreateLabel: "Create key"},
	{Key: "sm", Label: "Secrets Manager", Group: "Config & secrets", Noun: "secret",
		CreatePath: "/sm/create", CreateLabel: "Store secret"},
	{Key: "ssm", Label: "Parameter Store", Group: "Config & secrets", Noun: "parameter",
		CreatePath: "/ssm/create", CreateLabel: "Create parameter"},

	{Key: "iam", Label: "IAM", Group: "Stack-wide", Noun: "principal",
		CreatePath: "/iam/create", CreateLabel: "Create principal"},
	{Key: "cfn", Label: "CloudFormation", Group: "Stack-wide", Noun: "stack"},
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
