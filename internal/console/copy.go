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

// emptyCase is which of three things happens when a service's page is opened,
// and so which of its lines can ever be seen. Each service is assigned exactly
// one, and copy_reach_test.go drives every service to check the assignment and
// that no line is held that its case cannot show.
type emptyCase string

const (
	// caseUnselected: the list is shown and the detail pane waits for a pick.
	// This is the only case in which Unsel is ever on screen.
	caseUnselected emptyCase = "unselected"
	// caseFirst: the service opens its first resource whenever one exists, so
	// there is no "nothing selected" moment. Short and None show only while
	// there is nothing at all.
	caseFirst emptyCase = "first"
	// caseHome: the service's landing page is a page of its own (the metrics
	// page, a path query, the access log, the account page) and not a list with
	// a pane beside it.
	caseHome emptyCase = "home"
)

// emptyCopy is one service's voice in the states where it has nothing to show.
type emptyCopy struct {
	// Case says which lines below can be reached.
	Case emptyCase
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
		Case:    caseFirst,
		Short:   "No buckets yet",
		None:    "No buckets yet. A bucket is where objects live — and once one exists you can point notifications at a queue, a topic or a function, so writing a file becomes the thing that starts a workflow.",
		CTA:     "New bucket",
		CTAPath: "/s3/create",
	},
	"ddb": {
		Case:    caseFirst,
		Short:   "No tables yet",
		None:    "No tables yet. A table is the one store here that can also be a source: turn on a stream and every write can drive a Lambda.",
		CTA:     "New table",
		CTAPath: "/ddb/create",
	},
	"sqs": {
		Case:    caseFirst,
		Short:   "No queues yet",
		None:    "No queues yet, so nothing in this stack has anywhere to queue up. A queue is what lets a slow consumer fall behind without losing work — and what a dead-letter queue catches when it fails to keep up at all.",
		CTA:     "New queue",
		CTAPath: "/sqs/create",
	},
	"sns": {
		Case:    caseFirst,
		Short:   "No topics yet",
		None:    "No topics yet. A topic is how one event reaches several places at once — queues, functions, webhooks — each with its own filter policy deciding what it actually wants.",
		CTA:     "New topic",
		CTAPath: "/sns/create",
	},
	// The default bus always exists, so there is no state in which this service
	// has nothing to show and no line here that could be on screen.
	"eb": {
		Case: caseFirst,
	},
	"kinesis": {
		Case:    caseFirst,
		Short:   "No streams yet",
		None:    "No streams yet. A stream keeps records in order and keeps them after they are read, which is the difference between it and a queue.",
		CTA:     "New stream",
		CTAPath: "/kinesis/create",
	},
	"lambda": {
		Case:    caseHome,
		Short:   "No functions yet",
		None:    "No functions yet. New points a function at a build directory on disk — edit the code and re-invoke, no zip, no deploy.",
		CTA:     "New function",
		CTAPath: "/lambda/create",
	},
	"sfn": {
		Case:    caseFirst,
		Short:   "No state machines yet",
		None:    "No state machines yet. A state machine is the orchestration written down — which function runs after which, what a failure retries and what it gives up on — and an execution is one run of it you can read step by step.",
		CTA:     "New state machine",
		CTAPath: "/sfn/create",
	},
	"cw": {
		Case:    caseHome,
		Short:   "No alarms yet",
		CTA:     "New alarm",
		CTAPath: "/cw/create",
	},
	"logs": {
		Case:    caseUnselected,
		Short:   "No log groups yet",
		None:    "No log groups yet. A function writes one under /aws/lambda/<name> the first time it prints, and `aws logs tail --follow` reads the same lines you see here.",
		Unsel:   "Select a log group to tail it, filter it, or read one invocation's lines.",
		CTA:     "New log group",
		CTAPath: "/logs/create",
	},
	"kms": {
		Case:    caseFirst,
		Short:   "No keys yet",
		None:    "No keys yet. Keys here do real cryptography — encrypt, sign and HMAC against actual key material, so a signature that verifies here verifies on deploy.",
		CTA:     "New key",
		CTAPath: "/kms/create",
	},
	"sm": {
		Case:    caseUnselected,
		Short:   "No secrets yet",
		None:    "No secrets yet. A secret is encrypted at rest here and read through the same SDK call your code already makes, so nothing has to change between here and production.",
		Unsel:   "Select a secret to read it, edit it, or compare a version against the current one.",
		CTA:     "New secret",
		CTAPath: "/sm/create",
	},
	"ssm": {
		Case:    caseHome,
		Short:   "No parameters yet",
		None:    "No parameters yet. Parameter Store is the plain-configuration half of the pair — hierarchical paths, versioned, and SecureString when a value needs encrypting.",
		CTA:     "New parameter",
		CTAPath: "/ssm/create",
	},
	// IAM lands on the access log whether or not a principal exists, so it has
	// no empty state of its own either.
	"iam": {
		Case: caseHome,
	},
	"cfn": {
		Case:    caseHome,
		Short:   "No stacks yet",
		None:    "No stacks yet. Create one from a template here, or deploy one — sam deploy, cdk deploy, Serverless and the CloudFormation CLI all land in this list.",
		CTA:     "New stack",
		CTAPath: "/cfn/create",
	},
	"apigw": {
		Case:    caseFirst,
		Short:   "No APIs yet",
		None:    "No APIs yet. Create one here, or deploy one with sam, cdk, Serverless or the API Gateway CLI — and once it has a stage it serves real HTTP into your functions.",
		CTA:     "New API",
		CTAPath: "/apigw/create",
	},
}

// emptyFor returns a service's empty-state copy, and a usable zero value for a
// service that has none rather than an empty box.
func emptyFor(svc string) emptyCopy {
	if c, ok := emptyCopyBySvc[svc]; ok {
		return c
	}
	return emptyCopy{Short: "Nothing here yet.", None: "Nothing here yet."}
}
