package console

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The SDK-coverage ratchet.
//
// docs/api-support/*.md is a complete, well-maintained ledger of every
// operation each service implements and at what fidelity: F = functional (real
// local semantics), C = cosmetic (accepted, no local effect), S = stub (clean
// refusal). Nothing enforced it. It was referenced only by prose comments in
// the service packages, so the console could drift arbitrarily far from what
// the emulator can actually do and no test would notice.
//
// This is that test. It fails going OVER the floor and going UNDER it — the
// same shape as inlineBudget in cssguard_test.go — so the remaining distance
// is written down and burnt down instead of estimated.
//
// The docs are read, never written: this test only ever tells you the number.
//
// # What the bar actually is
//
// It was "every F-tier operation is reachable from the console", and that was
// never the rule anyone believed. Sixteen operations were exempt, and not one
// of the reasons said "not yet" — they said the operation has no business in a
// console: it is the deprecated spelling of a call already made, or the way
// the system writes rather than the way a person reads, or a fetch deliberately
// not made so a secret lives in fewer places.
//
// So the bar is stated as what it is: every operation a developer would
// INSPECT or REPAIR by hand is reachable. Everything else is exempt by
// CATEGORY, and the category carries the argument. A new operation is
// classified rather than argued about from scratch — which matters most for
// services yet to land, where the alternative is writing a fresh essay per
// operation to justify not building a form nobody would open.
//
// The note on each exemption stays. The category says which argument applies;
// the note says why it applies to this operation.

// uncovered is the burn-down list: F-tier operations with no console surface.
//
// An entry is a promise to either build the surface or move the operation to
// exempt with a reason. Deleting an entry without doing one of those makes the
// test fail, which is the point.
var uncovered = map[string][]string{
	// Complete — see exempt for the deliberate omissions.
	"sqs":         {},
	"kinesis":     {},
	"sns":         {},
	"kms":         {},
	"eventbridge": {},
	"ssm":         {},
	// BatchGetSecretValue is the one exemption — see exempt.
	"secretsmanager": {},
	// DescribeEndpoints is the one exemption — see exempt.
	"dynamodb":   {},
	"lambda":     {},
	"apigateway": {},
	// HTTP APIs: the routes, stages, invoke and settings tabs of the
	// apigw-http page, and the traffic classifier names the rest.
	"apigatewayv2": {},
	"s3":           {},
	"iam":          {},
	"sts":          {},
	// ListStackResources is the one exemption — see exempt.
	"cloudformation": {},
	// Complete, all 37 — the activities page is the worker, the task-result
	// form carries the heartbeat, and versions, aliases, Express, TestState,
	// redrive and Map Runs each have their panel. No exemptions.
	"stepfunctions": {},
	// Seven exemptions, each a twin of a call the console already makes —
	// see exempt.
	"logs": {},
}

// why an operation has no console surface. These are the four arguments the
// sixteen hand-written exemptions turned out to be making; naming them is what
// lets the next service classify an operation instead of writing an essay.
type why int

const (
	// redundant: the console reaches the same state another way — a
	// deprecated spelling, a paginated or batch twin, or an answer the
	// console already holds. A second call site would exercise the wire
	// without showing anything new.
	redundant why = iota
	// dataPlane: the operation is how the system writes, not how a person
	// reads or repairs. A form for it would fabricate state — log lines
	// nothing printed, metrics nothing measured — and misrepresent the run.
	dataPlane
	// withheld: deliberately not called so a value is fetched into fewer
	// places than it could be. The secrets trade: fewer fetch sites beats
	// fewer round trips for a store that holds plaintext.
	withheld
	// inert: writes configuration nothing local reads. It exists so a
	// Terraform or CloudFormation change applies instead of failing; an edit
	// form would only change values nothing here consults.
	inert
)

func (w why) String() string {
	switch w {
	case redundant:
		return "redundant"
	case dataPlane:
		return "data-plane"
	case withheld:
		return "withheld"
	case inert:
		return "inert"
	}
	return "unknown"
}

// exemption is a category and the note saying why it applies here.
type exemption struct {
	why  why
	note string
}

// exempt is for operations that are deliberately not called. This is a
// different claim from uncovered: uncovered says "not yet", exempt says "and
// here is why it never will be".
var exempt = map[string]map[string]exemption{
	"eventbridge": {
		"UpdateEventBus": {inert, "the three members it writes — Description, KmsKeyIdentifier and " +
			"DeadLetterConfig — are stored and reported back but inert locally, and the console " +
			"does not set them at create time either. It exists so a Terraform or CloudFormation " +
			"change to aws_cloudwatch_event_bus applies instead of answering InvalidAction; an " +
			"edit form would only change values nothing here reads"},
	},
	"logs": {
		"ListLogGroups":    {redundant, "the newer twin of DescribeLogGroups, which the list pane reads; it adds account-wide and pattern filters a single local account never needs"},
		"GetLogEvents":     {redundant, "one stream forwards or backwards; FilterLogEvents with a stream name, which the tail makes, reads the same lines and is what the CLI calls"},
		"PutLogEvents":     {dataPlane, "Lambda writes it for every invocation; a console form that writes lines nothing printed would be a lie about what ran"},
		"CreateLogStream":  {dataPlane, "a stream is created by the first PutLogEvents on it, which is how every stream here comes to exist"},
		"TagLogGroup":      {redundant, "the deprecated spelling of TagResource, which the tags panel calls"},
		"UntagLogGroup":    {redundant, "the deprecated spelling of UntagResource, which the tags panel calls"},
		"ListTagsLogGroup": {redundant, "the deprecated spelling of ListTagsForResource, which the tags panel calls"},
	},
	"cloudformation": {
		"ListStackResources": {redundant, "the paginated twin of DescribeStackResources, " +
			"which the resources tab already reads and which locally returns " +
			"every resource in one response. The list variant exists for stacks " +
			"past the describe call's 100-resource cap; a second call site " +
			"rendering the same rows would exercise the wire without showing " +
			"anything new."},
	},
	"sqs": {
		"GetQueueUrl": {redundant, "the console builds the URL from base + account + name " +
			"(backend.queueURL), which is exact and saves a round trip on every " +
			"render. Calling it would be a request whose answer we already know."},
	},
	"secretsmanager": {
		"BatchGetSecretValue": {withheld, "the console shows one secret at a time, so a " +
			"batch read would fetch plaintext values it does not display. For a " +
			"secrets store that is a worse trade than a round trip: the fewer " +
			"places a value is fetched into, the fewer places it can leak."},
	},
	"dynamodb": {
		"DescribeEndpoints": {redundant, "returns a canned endpoint list — where to connect. " +
			"The console proves that answer on every page it renders: it is " +
			"already talking to the endpoint the operation would describe, so a " +
			"surface for it would display a fact the connection itself asserts."},
	},
	"ssm": {
		"GetParameters": {withheld, "the batch get by explicit names. The console reads " +
			"parameters one at a time, and the multi-parameter view it does have " +
			"— a path listing — rides GetParametersByPath. Fetching a list of " +
			"values (SecureStrings included) for a view that does not exist is " +
			"the BatchGetSecretValue trade again: fewer fetch sites beats fewer " +
			"round trips for a store that holds secrets."},
	},
	"kms": {
		"ListKeyPolicies": {redundant, "a key has exactly one policy and it is named default, " +
			"on AWS as here; the key page reads it with GetKeyPolicy. Listing " +
			"the one name would be a request whose answer we already know."},
	},
	"kinesis": {
		"DescribeStream": {redundant, "the console reads DescribeStreamSummary + ListShards " +
			"instead. AWS caps DescribeStream's inline shard list and paginates it " +
			"with HasMoreShards; doze-aws returns every shard and always says " +
			"false. Writing the console against the emulator's generosity would " +
			"make it wrong against the service it imitates."},
		"DescribeStreamConsumer": {redundant, "ListStreamConsumers already returns all four " +
			"fields it would (name, ARN, status, creation time), and the consumers " +
			"table shows them. Describing one would be a second call for data " +
			"already on screen."},
	},
}

// docRow matches a row of an "| Operation | Tier | Notes |" table. Only those
// tables: s3.md and sqs.md also carry "| Input | Status |" tables, and a
// lenient parser would ingest "RedrivePolicy — target exists" as an operation.
var (
	opTableHead = regexp.MustCompile(`(?i)^\|\s*Operation\s*\|\s*Tier\s*\|`)
	tableRow    = regexp.MustCompile(`^\|([^|]*)\|([^|]*)\|`)
	opToken     = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
)

// fTierOps reads one service's ledger and returns its functional operations.
//
// A cell listing several operations ("TagQueue / UntagQueue / ListQueueTags")
// is split. A cell where ANY token fails to look like an operation name is
// treated as prose and contributes nothing — that is what stops
// "Mobile push (Platform applications/endpoints)" in sns.md from producing an
// operation called "Mobile".
func fTierOps(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var ops []string
	inTable := false
	for _, line := range strings.Split(string(b), "\n") {
		if opTableHead.MatchString(line) {
			inTable = true
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			inTable = false
			continue
		}
		if !inTable {
			continue
		}
		m := tableRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		tier := strings.TrimSpace(strings.ReplaceAll(m[2], "*", ""))
		// "C→F" and "S→F" mean "today C, planned F". Today is what counts.
		if i := strings.Index(tier, "→"); i >= 0 {
			tier = strings.TrimSpace(tier[:i])
		}
		if tier != "F" {
			continue
		}
		cell := strings.NewReplacer("`", "", "*", "").Replace(m[1])
		var toks []string
		ok := true
		for _, tok := range strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == ',' }) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			// A bare verb is a fragment of a bundled row ("Put/Get/Update/
			// List/DeleteFunctionEventInvokeConfig"), never an operation — only
			// the token carrying the full name checks anything. Left in, the
			// fragments got "covered" by whatever stray literal said "Get".
			// (Publish IS a real op — SNS — so it is not in the set.)
			if verbFragments[tok] {
				continue
			}
			if !opToken.MatchString(tok) {
				ok = false
				break
			}
			toks = append(toks, tok)
		}
		if ok {
			ops = append(ops, toks...)
		}
	}
	return ops
}

// consoleCalls is every quoted string literal in the console's non-test Go
// source. Reachability is "the operation name appears as a literal", which is
// deliberately permissive: sqsBatch takes its action as a PARAMETER, so a
// pattern matching only `b.sqs(ctx, "X"` reports DeleteMessageBatch as
// unreachable when it is called on the line above. A false negative here would
// send someone to build a surface that already exists.
func consoleCalls(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	lit := regexp.MustCompile(`"([A-Z][A-Za-z0-9]*)"`)
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range lit.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}

func TestSDKCoverage(t *testing.T) {
	calls := consoleCalls(t)
	docs, err := filepath.Glob("../docs/api-support/*.md")
	if err != nil || len(docs) == 0 {
		t.Skipf("no api-support ledger next to the console (%v)", err)
	}

	total, reached := 0, 0
	byCategory := map[why]int{}
	for _, doc := range docs {
		svc := strings.TrimSuffix(filepath.Base(doc), ".md")
		var missing []string
		for _, op := range fTierOps(t, doc) {
			total++
			if calls[op] {
				reached++
				continue
			}
			if ex, ok := exempt[svc][op]; ok {
				byCategory[ex.why]++
				continue
			}
			missing = append(missing, op)
		}
		sort.Strings(missing)

		// Only services with a declared floor are enforced. The rest are
		// reported so the number is visible without failing the build for work
		// that has not been scheduled.
		want, enforced := uncovered[svc]
		if !enforced {
			if len(missing) > 0 {
				t.Logf("%-16s %d F-tier operations still unreachable", svc, len(missing))
			}
			continue
		}
		sort.Strings(want)
		if strings.Join(missing, ",") != strings.Join(want, ",") {
			t.Errorf("%s coverage moved.\n  now unreachable: %v\n  uncovered[%q]:   %v\n"+
				"If you closed the gap, shorten the list. If you added an operation, "+
				"either give it a surface or add it to exempt with a reason.",
				svc, missing, svc, want)
		}
	}
	// Exempt operations are no longer folded into "reachable". Counting them
	// as reached made the headline number the one that only ever goes up, and
	// obscured the thing worth watching: which categories the console is
	// deliberately not serving, and whether one of them is quietly growing.
	exemptTotal := 0
	for _, n := range byCategory {
		exemptTotal += n
	}
	if reached+exemptTotal != total {
		t.Errorf("%d reachable + %d exempt != %d F-tier operations: something is counted twice or not at all",
			reached, exemptTotal, total)
	}
	t.Logf("F-tier operations: %d, of which %d reachable from the console and %d exempt by category",
		total, reached, exemptTotal)
	for _, w := range []why{redundant, dataPlane, withheld, inert} {
		if byCategory[w] > 0 {
			t.Logf("  %-10s %d", w, byCategory[w])
		}
	}
}

// TestExemptionsAreReal keeps the escape hatch honest: an exemption for an
// operation the docs no longer list as functional is a stale excuse, and an
// operation that is BOTH exempt and called is an exemption someone outgrew.
func TestExemptionsAreReal(t *testing.T) {
	calls := consoleCalls(t)
	for svc, ops := range exempt {
		doc := filepath.Join("..", "docs", "api-support", svc+".md")
		if _, err := os.Stat(doc); err != nil {
			t.Errorf("exempt[%q] has no ledger at %s", svc, doc)
			continue
		}
		listed := map[string]bool{}
		for _, op := range fTierOps(t, doc) {
			listed[op] = true
		}
		for op, ex := range ops {
			if ex.note == "" {
				t.Errorf("exempt[%q][%q] has no note: the category says which argument applies, the note says why it applies here", svc, op)
			}
			if ex.why.String() == "unknown" {
				t.Errorf("exempt[%q][%q] has no category", svc, op)
			}
			if !listed[op] {
				t.Errorf("exempt[%q][%q] is not an F-tier operation in the ledger — stale exemption", svc, op)
			}
			if calls[op] {
				t.Errorf("exempt[%q][%q] is exempt but the console calls it — drop the exemption", svc, op)
			}
		}
	}
}

// verbFragments are the bare verbs a bundled ledger row splits into.
var verbFragments = map[string]bool{
	"Put": true, "Get": true, "Update": true, "List": true, "Delete": true, "Create": true,
}
