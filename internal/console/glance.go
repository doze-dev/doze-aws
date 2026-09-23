package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The glance API: everything a terminal dashboard needs to render this stack's
// AWS page in ONE call — the service board, the attention line, and the wire
// (recent API calls). Designed to be polled every second or two while a
// dashboard row is selected: it reads the cached wiring graph, the in-memory
// traffic ring, and a handful of in-process list calls, nothing heavier.

type glanceService struct {
	Svc   string `json:"svc"`
	Label string `json:"label"`           // "4 buckets"
	State string `json:"state,omitempty"` // service-aware: depth, warm/cold, …
	Warn  bool   `json:"warn,omitempty"`
	Spark []int  `json:"spark,omitempty"` // calls per bucket over the last minute
	Calls int    `json:"calls"`           // calls in the last minute
}

type glanceAttention struct {
	Text string `json:"text"`
	Slug string `json:"slug,omitempty"` // console deep link, e.g. "sqs/emails-dlq"
}

type glanceWire struct {
	Seq    int64   `json:"seq"` // ring sequence — the dash anchors its scrollback on it
	T      string  `json:"t"`
	Svc    string  `json:"svc"`
	Action string  `json:"action"`
	Res    string  `json:"res,omitempty"`
	Code   int     `json:"code"`
	Millis float64 `json:"ms"`
	Err    bool    `json:"err,omitempty"`
}

type glanceResponse struct {
	Services  []glanceService   `json:"services"`
	Attention []glanceAttention `json:"attention,omitempty"`
	Wire      []glanceWire      `json:"wire,omitempty"`
	Rate      string            `json:"rate,omitempty"` // "42/min"
	Rate60    []int             `json:"rate60,omitempty"`
	Recorder  bool              `json:"recorder"` // false = capture off, wire is empty by design
	// Unwired and Nodes are additive: the wiring graph computes both on every
	// call and, until now, discarded them. "Nothing touches this resource" is
	// the highest-signal thing a local stack can tell you and it had no home.
	Unwired []glanceUnwired `json:"unwired,omitempty"`
	Nodes   int             `json:"nodes,omitempty"`
}

type glanceUnwired struct {
	Svc  string `json:"svc"`
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

const (
	glanceSparkBuckets = 8
	glanceWindow       = time.Minute
	glanceWireMax      = 60 // a short scrollback for the dash, not the full ring
)

func (c *Console) apiGlance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.glanceSnapshot(r.Context()))
}

// glanceSnapshot is the whole stack in one pass. Split out of apiGlance so the
// same computation can be rendered as JSON for the terminal dashboard and as
// HTML for the console's own deck — the alternative is two things that drift.
//
// Additions here must stay ADDITIVE: the sibling doze TUI decodes this into its
// own struct and ignores fields it does not know, so new fields are free and
// changed ones are not.
func (c *Console) glanceSnapshot(ctx context.Context) glanceResponse {
	resp := glanceResponse{}
	perSvc, perSvcTotal := c.glanceWire(&resp)

	// A service with nothing in it is omitted. The rail already lists all
	// thirteen with counts; a board repeating "0 buckets · 0 queues · 0 topics"
	// is noise, and it made "is this stack empty" unanswerable from here.
	add := func(key string, n int, label, state string, warn bool) {
		if n == 0 {
			return
		}
		resp.Services = append(resp.Services, glanceService{
			Svc: key, Label: label, State: state, Warn: warn,
			Spark: perSvc[key], Calls: perSvcTotal[key],
		})
	}

	// The order of these is the board's reading order. Each service that has
	// something to say beyond a count says it in its own method below; the
	// ones that are a count and nothing else stay here, because a method
	// wrapping two lines hides more than it explains.

	// s3
	buckets, _ := c.be.ListBuckets(ctx)
	add("s3", len(buckets), plural(len(buckets), "bucket"), "", false)

	c.glanceSQS(ctx, &resp, add)

	// sns
	topics, _ := c.be.ListTopics(ctx)
	subs := 0
	for _, t := range topics {
		subs += t.Subs
	}
	add("sns", len(topics), plural(len(topics), "topic"), plural(subs, "subscription"), false)

	// dynamodb
	tables, _ := c.be.ListTables(ctx)
	items := int64(0)
	for _, t := range tables {
		items += t.ItemCount
	}
	add("ddb", len(tables), plural(len(tables), "table"), plural(int(items), "item"), false)

	c.glanceEventBridge(ctx, add)
	c.glanceLambda(ctx, add)

	// kms
	if n, err := c.be.CountKeys(ctx); err == nil {
		add("kms", n, plural(n, "key"), "", false)
	}

	c.glanceSSM(ctx, add)

	// secrets manager
	secrets, _ := c.be.ListSecrets(ctx)
	add("sm", len(secrets), plural(len(secrets), "secret"), "", false)

	// The board stopped at nine services while apiCounts already counted
	// thirteen, so a stack whose only resources were streams or stacks looked
	// empty from here.
	if n, err := c.be.CountStreams(ctx); err == nil && n > 0 {
		add("kinesis", n, plural(n, "stream"), "", false)
	}
	if n, err := c.be.CountStacks(ctx); err == nil && n > 0 {
		add("cfn", n, plural(n, "stack"), "", false)
	}
	if n, err := c.be.CountRestAPIs(ctx); err == nil && n > 0 {
		add("apigw", n, plural(n, "API"), "", false)
	}

	c.glanceStepFunctions(ctx, add)

	if groups, err := c.be.ListLogGroups(ctx); err == nil && len(groups) > 0 {
		add("logs", len(groups), plural(len(groups), "log group"), "", false)
	}

	c.glanceCloudWatch(ctx, add)

	if n, err := c.be.CountPrincipals(ctx); err == nil && n > 0 {
		add("iam", n, plural(n, "principal"), "", false)
	}

	c.glanceUnwired(ctx, &resp)
	return resp
}

// glanceAdd appends one service to the board, or does nothing when the service
// is empty. glanceSnapshot owns the rule; each section is handed the closure so
// it cannot forget it.
type glanceAdd func(key string, n int, label, state string, warn bool)

// glanceWire fills the wire tail and the call sparklines from the in-memory
// ring, and reports the per-service buckets the board hangs off. Recorder stays
// false when there is no recorder, so an empty wire reads as capture-off rather
// than as a quiet stack.
func (c *Console) glanceWire(resp *glanceResponse) (perSvc map[string][]int, perSvcTotal map[string]int) {
	perSvc = map[string][]int{}
	perSvcTotal = map[string]int{}
	if c.rec == nil {
		return perSvc, perSvcTotal
	}
	resp.Recorder = true
	now := time.Now()
	resp.Rate60 = make([]int, glanceSparkBuckets)
	total := 0
	bucket := func(at time.Time) (int, bool) {
		age := now.Sub(at)
		if age < 0 || age >= glanceWindow {
			return 0, false
		}
		// newest at the right edge
		i := glanceSparkBuckets - 1 - int(age*time.Duration(glanceSparkBuckets)/glanceWindow)
		if i < 0 {
			i = 0
		}
		return i, true
	}
	for _, e := range c.rec.Entries(0) {
		if len(resp.Wire) < glanceWireMax {
			resp.Wire = append(resp.Wire, glanceWire{
				Seq: e.Seq, T: e.At.Local().Format("15:04:05.0"), Svc: e.Service, Action: e.Action,
				Res: e.Resource, Code: e.Status, Millis: e.Millis, Err: e.Status >= 400,
			})
		}
		if i, ok := bucket(e.At); ok {
			total++
			resp.Rate60[i]++
			sp := perSvc[e.Service]
			if sp == nil {
				sp = make([]int, glanceSparkBuckets)
				perSvc[e.Service] = sp
			}
			sp[i]++
			perSvcTotal[e.Service]++
		}
	}
	resp.Rate = fmt.Sprintf("%d/min", total)
	return perSvc, perSvcTotal
}

// glanceSQS reports queue depth with dead letters counted apart, because a
// thousand messages waiting to be processed and a thousand that failed are
// opposite situations that a single total would report identically. A non-empty
// DLQ is also the one queue fact worth raising to Attention.
func (c *Console) glanceSQS(ctx context.Context, resp *glanceResponse, add glanceAdd) {
	queues, _ := c.be.ListQueues(ctx)
	depth, dlqDepth := 0, 0
	dlqNames := map[string]bool{}
	for _, q := range queues {
		if q.DLQ != "" {
			dlqNames[q.DLQ] = true
		}
	}
	var worstDLQ string
	for _, q := range queues {
		if dlqNames[q.Name] {
			dlqDepth += q.Available
			if q.Available > 0 && worstDLQ == "" {
				worstDLQ = q.Name
			}
			continue
		}
		depth += q.Available
	}
	state := ""
	if depth > 0 {
		state = plural(depth, "msg") + " queued"
	}
	if dlqDepth > 0 {
		if state != "" {
			state += " · "
		}
		state += "dlq " + strconv.Itoa(dlqDepth) + " ⚠"
	}
	add("sqs", len(queues), plural(len(queues), "queue"), state, dlqDepth > 0)
	if dlqDepth > 0 {
		resp.Attention = append(resp.Attention, glanceAttention{
			Text: worstDLQ + " holds " + plural(dlqDepth, "message"),
			Slug: "sqs/" + worstDLQ,
		})
	}
}

// glanceEventBridge counts rules, and buses beyond the default one.
//
// The default bus always exists — nobody made it — so counting it makes a
// fresh stack look populated. EventBridge is on the board when there is
// something in it: another bus, or a rule.
func (c *Console) glanceEventBridge(ctx context.Context, add glanceAdd) {
	buses, _ := c.be.ListBuses(ctx)
	rules := 0
	for _, b := range buses {
		rules += b.Rules
	}
	n := rules
	if len(buses) > 1 {
		n += len(buses) - 1
	}
	add("eb", n, plural(len(buses), "bus")+" · "+plural(rules, "rule"), "", false)
}

// glanceLambda puts the scale-to-zero story on the board: one warm function
// makes the stack warm, and the countdown to sleep is the part worth seeing.
func (c *Console) glanceLambda(ctx context.Context, add glanceAdd) {
	fns, _ := c.be.ListFunctions(ctx)
	state := ""
	for _, f := range fns {
		rt := c.be.LambdaRuntime(ctx, f.Name)
		if rt.Warm {
			state = "warm"
			if left := rt.SleepLeft(); left > 0 {
				state += " · sleeps in " + shortDur(left)
			}
			break
		}
	}
	if state == "" && len(fns) > 0 {
		state = "cold · wakes on invoke"
	}
	add("lambda", len(fns), plural(len(fns), "function"), state, false)
}

// glanceSSM counts parameters, calling out the encrypted ones.
func (c *Console) glanceSSM(ctx context.Context, add glanceAdd) {
	params, _ := c.be.ListParameters(ctx)
	secure := 0
	for _, p := range params {
		if strings.EqualFold(p.Type, "SecureString") {
			secure++
		}
	}
	state := ""
	if secure > 0 {
		state = plural(secure, "SecureString")
	}
	add("ssm", len(params), plural(len(params), "param"), state, false)
}

// glanceStepFunctions counts running executions, which is the state worth a
// glance: one stuck on a task token looks exactly like one that is busy.
func (c *Console) glanceStepFunctions(ctx context.Context, add glanceAdd) {
	sms, err := c.be.ListStateMachines(ctx)
	if err != nil || len(sms) == 0 {
		return
	}
	running := 0
	for _, m := range sms {
		if execs, err := c.be.ListExecutions(ctx, m.ARN, "RUNNING"); err == nil {
			running += len(execs)
		}
	}
	state := ""
	if running > 0 {
		state = plural(running, "running execution")
	}
	add("sfn", len(sms), plural(len(sms), "state machine"), state, false)
}

// glanceCloudWatch counts alarms. An alarm in ALARM is the one thing on this
// page worth reading first, so it warns.
func (c *Console) glanceCloudWatch(ctx context.Context, add glanceAdd) {
	alarms, err := c.be.ListAlarms(ctx)
	if err != nil || len(alarms) == 0 {
		return
	}
	firing := 0
	for _, a := range alarms {
		if a.State == "ALARM" {
			firing++
		}
	}
	note := ""
	if firing > 0 {
		note = plural(firing, "alarm") + " firing"
	}
	add("cw", len(alarms), plural(len(alarms), "alarm"), note, firing > 0)
}

// glanceUnwired lists resources nothing is wired to. layoutFlows computes this
// on every Neighbors() call and used to throw it away once the flows page was
// deleted.
func (c *Console) glanceUnwired(ctx context.Context, resp *glanceResponse) {
	g := c.be.graphCached(ctx)
	if g.NodeCount == 0 {
		return
	}
	resp.Nodes = g.NodeCount
	for _, n := range g.Unwired {
		resp.Unwired = append(resp.Unwired, glanceUnwired{
			Svc: n.Svc, Name: n.Name, Slug: strings.TrimPrefix(n.URL, "/"),
		})
	}
}
func shortDur(secs int) string {
	switch {
	case secs >= 3600:
		return fmt.Sprintf("%dh%dm", secs/3600, secs%3600/60)
	case secs >= 60:
		return fmt.Sprintf("%dm", secs/60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
