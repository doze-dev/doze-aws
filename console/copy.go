package console

// Empty-state copy, kept together so the tone stays one person's.
//
// There are four states a list surface can be in and the console handled two of
// them. Nothing exists, nothing is selected, the filter matched nothing, and
// something is selected. "Nothing exists" and "nothing selected" shared a single
// sentence — "Select a bucket, or create one to store objects" — which is
// addressed to someone who has buckets, shown to someone who has none. And a
// filter matching nothing rendered a silently blank pane, which is the one that
// looks broken rather than empty.
//
// The division of labour: the LIST pane says what is missing, the DETAIL pane
// says what to do about it. That is what Lambda already did by hand and it is
// the only one of the thirteen that had a real first-run state.
//
// Voice, borrowed from the best line already in the console — panes.html's
// "nothing reads this queue — wire a Lambda trigger to close the loop": say the
// consequence, not the status. "No queues yet" is a fact about the database.
// "Nothing has anywhere to queue up" is a fact about the user's stack.

// emptyCopy is one service's voice in the states where it has nothing to show.
type emptyCopy struct {
	// Short is the list pane's HEADING — no end punctuation, per Cloudscape's
	// rule that headings and buttons take none and descriptions do. The pane is
	// 232px wide, so it gets the
	// fact and nothing else — the explanation belongs in the detail pane, and
	// printing the same paragraph in both is how a two-pane empty state reads
	// as a stutter rather than as a division of labour.
	Short string
	// None is the detail pane's copy, shown when the service holds nothing at
	// all. It should say what the service is FOR, because someone seeing it may
	// not know.
	None string
	// Unsel is shown when rows exist but none is chosen.
	Unsel string
	// CTA labels the primary button; empty when there is nothing to create.
	CTA string
	// CTAPath is prefix-relative.
	CTAPath string
}

var emptyCopyBySvc = map[string]emptyCopy{
	"s3": {
		Short:   "No buckets yet",
		None:    "No buckets yet. A bucket is where objects live — and once one exists you can point notifications at a queue, a topic or a function, so writing a file becomes the thing that starts a workflow.",
		Unsel:   "Select a bucket to browse its objects, or edit its versioning, lifecycle and event notifications.",
		CTA:     "New bucket",
		CTAPath: "/s3/create",
	},
	"ddb": {
		Short:   "No tables yet",
		None:    "No tables yet. A table is the one store here that can also be a source: turn on a stream and every write can drive a Lambda.",
		Unsel:   "Select a table to scan it, query it, or run PartiQL against it.",
		CTA:     "New table",
		CTAPath: "/ddb/create",
	},
	"sqs": {
		Short:   "No queues yet",
		None:    "No queues yet, so nothing in this stack has anywhere to queue up. A queue is what lets a slow consumer fall behind without losing work — and what a dead-letter queue catches when it fails to keep up at all.",
		Unsel:   "Select a queue to read its messages without consuming them, send one, or set up redrive.",
		CTA:     "New queue",
		CTAPath: "/sqs/create",
	},
	"sns": {
		Short:   "No topics yet",
		None:    "No topics yet. A topic is how one event reaches several places at once — queues, functions, webhooks — each with its own filter policy deciding what it actually wants.",
		Unsel:   "Select a topic to publish to it, or to see who is subscribed and what each subscriber filters on.",
		CTA:     "New topic",
		CTAPath: "/sns/create",
	},
	"eb": {
		Short:   "No event buses yet",
		None:    "No event buses yet. A bus routes by the shape of an event rather than by its destination, so a rule can pick out the events it cares about and nothing else has to know.",
		Unsel:   "Select a bus to add rules, test an event against them, or replay from an archive.",
		CTA:     "New bus",
		CTAPath: "/eb/create-bus",
	},
	"kinesis": {
		Short:   "No streams yet",
		None:    "No streams yet. A stream keeps records in order and keeps them after they are read, which is the difference between it and a queue.",
		Unsel:   "Select a stream to read records, put one, or split and merge its shards.",
		CTA:     "New stream",
		CTAPath: "/kinesis/create",
	},
	"lambda": {
		Short:   "No functions yet",
		None:    "No functions yet. New points a function at a build directory on disk — edit the code and re-invoke, no zip, no deploy.",
		Unsel:   "Select a function to invoke it, edit its configuration, or wire an event source to it.",
		CTA:     "New function",
		CTAPath: "/lambda/create",
	},
	"kms": {
		Short:   "No keys yet",
		None:    "No keys yet. Keys here do real cryptography — encrypt, sign and HMAC against actual key material, so a signature that verifies here verifies on deploy.",
		Unsel:   "Select a key to encrypt or sign something with it, manage aliases, or schedule its deletion.",
		CTA:     "New key",
		CTAPath: "/kms/create",
	},
	"sm": {
		Short:   "No secrets yet",
		None:    "No secrets yet. A secret is encrypted at rest here and read through the same SDK call your code already makes, so nothing has to change between here and production.",
		Unsel:   "Select a secret to read it, edit it, or compare a version against the current one.",
		CTA:     "New secret",
		CTAPath: "/sm/create",
	},
	"ssm": {
		Short:   "No parameters yet",
		None:    "No parameters yet. Parameter Store is the plain-configuration half of the pair — hierarchical paths, versioned, and SecureString when a value needs encrypting.",
		Unsel:   "Select a parameter to read it, edit it, or compare versions.",
		CTA:     "New parameter",
		CTAPath: "/ssm/create",
	},
	"iam": {
		Short:   "No principals yet",
		None:    "No principals yet. IAM is off by default, so calls are allowed and simply recorded — the access log below still fills up, and it is what generates the policy your code actually needed.",
		Unsel:   "Select a principal to see what it can do, or read the access log to find out what was asked for.",
		CTA:     "New principal",
		CTAPath: "/iam/create",
	},
	// CloudFormation and API Gateway have no create form on purpose: things
	// arrive in them by being deployed. Their copy says so rather than offering
	// a button that does not exist.
	"cfn": {
		Short: "No stacks yet — they arrive by deploying",
		None:  "No stacks yet. Stacks appear here when you deploy — sam deploy, cdk deploy, Serverless, or the CloudFormation CLI all land in this list.",
		Unsel: "Select a stack to see its resources, its events, and the template it was deployed from.",
	},
	"apigw": {
		Short: "No APIs yet — they arrive by deploying",
		None:  "No APIs yet. APIs appear here when you deploy one with sam, cdk, Serverless or the API Gateway CLI — and once deployed, they serve real HTTP into your functions.",
		Unsel: "Select an API to see its routes and stages, or to send a request through it.",
	},
}

// emptyFor returns a service's empty-state copy, and a usable zero value for a
// service that has none rather than an empty box.
func emptyFor(svc string) emptyCopy {
	if c, ok := emptyCopyBySvc[svc]; ok {
		return c
	}
	return emptyCopy{Short: "Nothing here yet.", None: "Nothing here yet.", Unsel: "Select something from the list."}
}
