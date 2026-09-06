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

	// The wire + per-service call sparklines, from the in-memory ring.
	perSvc := map[string][]int{}
	perSvcTotal := map[string]int{}
	if c.rec != nil {
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
	}

	// A service with nothing in it is omitted. The rail already lists all
	// thirteen with counts; a board repeating "0 buckets · 0 queues · 0 topics"
	// is noise, and it made "is this stack empty" unanswerable from here.
	svc := func(key string, n int, label, state string, warn bool) {
		if n == 0 {
			return
		}
		resp.Services = append(resp.Services, glanceService{
			Svc: key, Label: label, State: state, Warn: warn,
			Spark: perSvc[key], Calls: perSvcTotal[key],
		})
	}

	// s3
	buckets, _ := c.be.ListBuckets(ctx)
	svc("s3", len(buckets), plural(len(buckets), "bucket"), "", false)

	// sqs — depths and dead letters come from the same attrs fetch
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
	svc("sqs", len(queues), plural(len(queues), "queue"), state, dlqDepth > 0)
	if dlqDepth > 0 {
		resp.Attention = append(resp.Attention, glanceAttention{
			Text: worstDLQ + " holds " + plural(dlqDepth, "message"),
			Slug: "sqs/" + worstDLQ,
		})
	}

	// sns
	topics, _ := c.be.ListTopics(ctx)
	subs := 0
	for _, t := range topics {
		subs += t.Subs
	}
	svc("sns", len(topics), plural(len(topics), "topic"), plural(subs, "subscription"), false)

	// dynamodb
	tables, _ := c.be.ListTables(ctx)
	items := int64(0)
	for _, t := range tables {
		items += t.ItemCount
	}
	svc("ddb", len(tables), plural(len(tables), "table"), plural(int(items), "item"), false)

	// eventbridge
	buses, _ := c.be.ListBuses(ctx)
	rules := 0
	for _, b := range buses {
		rules += b.Rules
	}
	// The default bus always exists — nobody made it — so counting it makes a
	// fresh stack look populated. EventBridge is on the board when there is
	// something in it: another bus, or a rule.
	ebN := rules
	if len(buses) > 1 {
		ebN += len(buses) - 1
	}
	svc("eb", ebN, plural(len(buses), "bus")+" · "+plural(rules, "rule"), "", false)

	// lambda — the scale-to-zero story belongs on the board
	fns, _ := c.be.ListFunctions(ctx)
	lamState := ""
	for _, f := range fns {
		rt := c.be.LambdaRuntime(ctx, f.Name)
		if rt.Warm {
			lamState = "warm"
			if left := rt.SleepLeft(); left > 0 {
				lamState += " · sleeps in " + shortDur(left)
			}
			break
		}
	}
	if lamState == "" && len(fns) > 0 {
		lamState = "cold · wakes on invoke"
	}
	svc("lambda", len(fns), plural(len(fns), "function"), lamState, false)

	// kms / ssm / secrets — cheap counts
	if n, err := c.be.CountKeys(ctx); err == nil {
		svc("kms", n, plural(n, "key"), "", false)
	}
	params, _ := c.be.ListParameters(ctx)
	secure := 0
	for _, p := range params {
		if strings.EqualFold(p.Type, "SecureString") {
			secure++
		}
	}
	st := ""
	if secure > 0 {
		st = plural(secure, "SecureString")
	}
	svc("ssm", len(params), plural(len(params), "param"), st, false)
	secrets, _ := c.be.ListSecrets(ctx)
	svc("sm", len(secrets), plural(len(secrets), "secret"), "", false)

	// The board stopped at nine services while apiCounts already counted
	// thirteen, so a stack whose only resources were streams or stacks looked
	// empty from here.
	if n, err := c.be.CountStreams(ctx); err == nil && n > 0 {
		svc("kinesis", n, plural(n, "stream"), "", false)
	}
	if n, err := c.be.CountStacks(ctx); err == nil && n > 0 {
		svc("cfn", n, plural(n, "stack"), "", false)
	}
	if n, err := c.be.CountRestAPIs(ctx); err == nil && n > 0 {
		svc("apigw", n, plural(n, "API"), "", false)
	}
	// Step Functions: a running execution is the state worth a glance, since
	// one that is stuck on a task token looks exactly like one that is busy.
	if sms, err := c.be.ListStateMachines(ctx); err == nil && len(sms) > 0 {
		running := 0
		for _, m := range sms {
			if execs, err := c.be.ListExecutions(ctx, m.ARN, "RUNNING"); err == nil {
				running += len(execs)
			}
		}
		st := ""
		if running > 0 {
			st = plural(running, "running execution")
		}
		svc("sfn", len(sms), plural(len(sms), "state machine"), st, false)
	}
	if groups, err := c.be.ListLogGroups(ctx); err == nil && len(groups) > 0 {
		svc("logs", len(groups), plural(len(groups), "log group"), "", false)
	}
	if n, err := c.be.CountPrincipals(ctx); err == nil && n > 0 {
		svc("iam", n, plural(n, "principal"), "", false)
	}

	// Unwired: resources nothing is wired to. Computed by layoutFlows on every
	// Neighbors() call and thrown away since the flows page was deleted.
	if g := c.be.graphCached(ctx); g.NodeCount > 0 {
		resp.Nodes = g.NodeCount
		for _, n := range g.Unwired {
			resp.Unwired = append(resp.Unwired, glanceUnwired{
				Svc: n.Svc, Name: n.Name, Slug: strings.TrimPrefix(n.URL, "/"),
			})
		}
	}
	return resp
}

// shortDur renders a countdown compactly: "6m", "45s", "1h2m".
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
