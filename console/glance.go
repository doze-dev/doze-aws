package console

import (
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
}

const (
	glanceSparkBuckets = 8
	glanceWindow       = time.Minute
	glanceWireMax      = 60 // a short scrollback for the dash, not the full ring
)

func (c *Console) apiGlance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	svc := func(key, label, state string, warn bool) {
		resp.Services = append(resp.Services, glanceService{
			Svc: key, Label: label, State: state, Warn: warn,
			Spark: perSvc[key], Calls: perSvcTotal[key],
		})
	}

	// s3
	buckets, _ := c.be.ListBuckets(ctx)
	svc("s3", plural(len(buckets), "bucket"), "", false)

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
	svc("sqs", plural(len(queues), "queue"), state, dlqDepth > 0)
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
	svc("sns", plural(len(topics), "topic"), plural(subs, "subscription"), false)

	// dynamodb
	tables, _ := c.be.ListTables(ctx)
	items := int64(0)
	for _, t := range tables {
		items += t.ItemCount
	}
	svc("ddb", plural(len(tables), "table"), plural(int(items), "item"), false)

	// eventbridge
	buses, _ := c.be.ListBuses(ctx)
	rules := 0
	for _, b := range buses {
		rules += b.Rules
	}
	svc("eb", plural(len(buses), "bus")+" · "+plural(rules, "rule"), "", false)

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
	svc("lambda", plural(len(fns), "function"), lamState, false)

	// kms / ssm / secrets — cheap counts
	if n, err := c.be.CountKeys(ctx); err == nil {
		svc("kms", plural(n, "key"), "", false)
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
	svc("ssm", plural(len(params), "param"), st, false)
	secrets, _ := c.be.ListSecrets(ctx)
	svc("sm", plural(len(secrets), "secret"), "", false)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
