package console

// The wire page's own data: the header's rate, the rows with their time lanes,
// "Needs a look" and "In this window" (boards Main, Wire-Inspect, Wire-Refused,
// Wire-Empty).
//
// Everything here is read from the recorder's ring, and from two reads of the
// product the ring cannot answer (a dead-letter queue that holds messages, an
// alarm that is firing). Nothing is estimated and nothing is kept beside the
// ring: a figure that is not in the ring or the product is not on the page.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// wirePulse is the stretch "a minute" means in the header's "live · 26 a
// minute": the same window the rail's dots use.
const wirePulse = time.Minute

// needsTTL is how long the product's half of "Needs a look" (dead-letter
// queues, alarms) is believed. The page polls every second and a half and each
// read lists every queue's attributes, so it is read at most this often, in
// every tab together.
const needsTTL = 3 * time.Second

// waited reports whether a call's duration is time spent waiting rather than
// working: a long poll that held the connection open until its wait ran out.
// The row says it in seconds, muted and with no lane, and the window's
// "Slowest" leaves it out: twenty seconds of an empty queue is not slow.
func waited(e TrafficEntry) bool {
	return e.Service == "sqs" && e.Action == "ReceiveMessage" && e.Millis >= 1000
}

// answerOf is the Answer column's text: the status alone for a served call, and
// the state in a word beside it for the rest, so the colour is never the only
// signal (D7). A function error is a 200 that says "it threw" instead: the
// status is true and is not what the reader needs.
func answerOf(status int, state, word string) string {
	switch state {
	case "served":
		return strconv.Itoa(status)
	case "threw":
		return "it threw"
	}
	return strconv.Itoa(status) + " " + strings.ToLower(word)
}

// actionLabel is the operation as a row says it. A Lambda invoke made by an
// event source mapping is recorded as "Invoke (event source)" and its row
// already says "via the queue's event source mapping", so the label is the
// operation: Invoke.
func actionLabel(action string) string {
	return strings.TrimSuffix(action, " (event source)")
}

// viaWords is what emitted a piece of work, in the words a reader would use.
// An event source mapping's invoke is recorded as via "sqs:<queue>", which is
// the queue's name for a reader who already sees the queue on the row above;
// what a reader wants is the mechanism.
func viaWords(e TrafficEntry) string {
	if e.Service == "lambda" {
		switch {
		case strings.HasPrefix(e.Via, "sqs:"):
			return "the queue's event source mapping"
		case strings.HasPrefix(e.Via, "dynamodb:"), strings.HasPrefix(e.Via, "kinesis:"):
			return "the stream's event source mapping"
		}
	}
	return e.Via
}

// whyOf is the sentence a failing row carries beside its resource: the service's
// own message for a refusal, and for a function error what the function raised
// ("KeyError: 'unit_price'"). Empty for a call that was served.
func whyOf(state string, ref *Refusal) string {
	if ref == nil {
		return ""
	}
	if state == "threw" && ref.Code != "" && ref.Message != "" {
		return ref.Code + ": " + ref.Message
	}
	if ref.Message != "" {
		return ref.Message
	}
	return ref.Code
}

// whyTitle is the code and the message together, for the row's hover.
func whyTitle(ref *Refusal) string {
	if ref == nil {
		return ""
	}
	if ref.Code != "" && ref.Message != "" {
		return ref.Code + ": " + ref.Message
	}
	return firstNonEmpty(ref.Message, ref.Code)
}

// wireStats is the right-hand "In this window": the ring, counted.
type wireStats struct {
	Calls    int    // every row the ring holds, cascade rows included
	Services int    // distinct services among them
	Refused  int    // refused, and refused here: the answers AWS would give too, or a limit of ours
	FnErrors int    // a function that raised
	Slowest  string // "212 ms · Invoke"; empty when nothing has run
	Rate     int    // calls that started in the last minute
}

// needItem is one callout in "Needs a look".
type needItem struct {
	Tone  string // warn | bad | deny | "" (the board's four colours)
	Title string
	Note  string
	Href  string // prefix-relative; empty for a callout that is not a link
}

type wireSvc struct {
	Key, Label string
	N          int
}

// wireView is everything the wire page and its live region draw.
type wireView struct {
	Prefix, Endpoint, Region string
	Enabled                  bool
	FirstRun                 bool
	Rows                     []trafficRow
	Stats                    wireStats
	Needs                    []needItem
	Services                 []wireSvc
	RefusedN                 int
	Kept                     int // trafficCap, said on the page
	Hash                     string
	LastSeq                  int64
	Env                      string // the first-run block

	dlq, alarms []needItem // the product's half of Needs, read by the probe
}

// pulse counts the calls that started in the last minute. It is the figure the
// header's pill and the rail's "live" both read.
func (rec *Recorder) pulse(now time.Time) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := rec.head
	if rec.full {
		n = trafficCap
	}
	count := 0
	for i := 0; i < n; i++ {
		if d := now.Sub(rec.buf[i].At); d >= 0 && d < wirePulse {
			count++
		}
	}
	return count
}

// statsOf counts a window of entries.
func statsOf(raw []TrafficEntry, now time.Time) wireStats {
	s := wireStats{Calls: len(raw)}
	svcs := map[string]bool{}
	var slow TrafficEntry
	for _, e := range raw {
		svcs[e.Service] = true
		switch callState(e.Status, e.Failure(), factsOfEntry(e)) {
		case "refused", "refusedhere":
			s.Refused++
		case "threw":
			s.FnErrors++
		}
		if d := now.Sub(e.At); d >= 0 && d < wirePulse {
			s.Rate++
		}
		if !waited(e) && e.Millis > slow.Millis {
			slow = e
		}
	}
	s.Services = len(svcs)
	if slow.Millis > 0 {
		s.Slowest = fmtMillis(slow.Millis) + " ms · " + actionLabel(slow.Action)
	}
	return s
}

// servicesOf is the filter menu: the services that appear in the window, in the
// rail's order, each with its calls.
func servicesOf(raw []TrafficEntry) []wireSvc {
	n := map[string]int{}
	for _, e := range raw {
		n[e.Service]++
	}
	var out []wireSvc
	seen := map[string]bool{}
	for _, e := range catalog {
		if n[e.Key] > 0 {
			out = append(out, wireSvc{Key: e.Key, Label: e.Label, N: n[e.Key]})
			seen[e.Key] = true
		}
	}
	// A service the rail does not list (sts, http) still has calls, and a menu
	// that left them out could not filter to them.
	var rest []string
	for k := range n {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, wireSvc{Key: k, Label: strings.ToUpper(k), N: n[k]})
	}
	return out
}

// ringNeeds is the half of "Needs a look" the ring answers: refusals and a
// function that raised, over the last 500 calls.
func ringNeeds(raw []TrafficEntry, prefix string) []needItem {
	var refused, refusedHere, denied, failed, notBuilt int
	fnCount := 0
	fnNames := map[string]int{}
	for _, e := range raw {
		switch callState(e.Status, e.Failure(), factsOfEntry(e)) {
		case "refused":
			refused++
		case "refusedhere":
			refusedHere++
		case "denied":
			denied++
		case "error":
			failed++
		case "notbuilt":
			notBuilt++
		case "threw":
			fnCount++
			fnNames[e.Resource]++
		}
	}
	var out []needItem
	// Said once per kind, in the order the states are taught. Each sentence is the
	// state's own (stateCopyFor): a refusal is the answer AWS would give, a
	// refusal "here" is the one AWS would not, a denial is a policy's.
	if failed > 0 {
		out = append(out, needItem{Tone: "bad", Title: callsThat(failed, "failed"),
			Note: "doze-aws broke rather than refused. That is worth reporting."})
	}
	if denied > 0 {
		out = append(out, needItem{Tone: "warn", Title: callsWere(denied, "denied"),
			Note: "A policy decided it, not the input. The access log has the policy.", Href: "/iam"})
	}
	if refused > 0 {
		out = append(out, needItem{Tone: "deny", Title: callsWere(refused, "refused"),
			Note: "The same request would fail on AWS."})
	}
	if refusedHere > 0 {
		out = append(out, needItem{Tone: "deny", Title: callsWere(refusedHere, "refused here"),
			Note: "AWS would accept it. This is a limit of doze-aws."})
	}
	if notBuilt > 0 {
		out = append(out, needItem{Title: callsThat(notBuilt, "asked for an operation that is not built"),
			Note: "doze-aws does not build these operations. Nothing is wrong with the requests."})
	}
	if fnCount > 0 {
		names := make([]string, 0, len(fnNames))
		for k := range fnNames {
			names = append(names, k)
		}
		sort.Strings(names)
		title := names[0] + " answered with an error"
		if len(names) > 1 {
			title = strconv.Itoa(len(names)) + " functions answered with an error"
		}
		times := "1 time"
		if fnCount != 1 {
			times = strconv.Itoa(fnCount) + " times"
		}
		out = append(out, needItem{Title: title,
			Note: "An Invoke came back with a function error " + times + " in the last " + strconv.Itoa(trafficCap) + " calls."})
	}
	return out
}

// callsThat is "1 call failed" / "3 calls failed": a count of calls and what
// each did, for a state that is a verb and not a participle.
func callsThat(n int, did string) string {
	if n == 1 {
		return "1 call " + did
	}
	return strconv.Itoa(n) + " calls " + did
}

// callsWere is "1 call was refused" / "3 calls were refused".
func callsWere(n int, what string) string {
	if n == 1 {
		return "1 call was " + what
	}
	return strconv.Itoa(n) + " calls were " + what
}

// productNeeds is the half the ring cannot answer: a dead-letter queue that
// holds messages, and an alarm that is firing. Each is read from the service,
// so a stack with neither says nothing about them.
func (c *Console) productNeeds(ctx context.Context) (dlq, alarms []needItem) {
	c.be.graphMu.Lock()
	if !c.be.wireAt.IsZero() && time.Since(c.be.wireAt) < needsTTL {
		d, a := c.be.wireDLQ, c.be.wireAlarms
		c.be.graphMu.Unlock()
		return d, a
	}
	c.be.graphMu.Unlock()

	if queues, err := c.be.ListQueues(ctx); err == nil {
		// What a message waits for before it lands in a dead-letter queue is the
		// maxReceiveCount of the queue that names it, so it is read from there.
		maxBy := map[string]int{}
		for _, q := range queues {
			if q.DLQ != "" && q.MaxReceive > 0 {
				if cur, ok := maxBy[q.DLQ]; !ok || q.MaxReceive < cur {
					maxBy[q.DLQ] = q.MaxReceive
				}
			}
		}
		for _, q := range queues {
			if !q.IsDLQ || q.Available == 0 {
				continue
			}
			n := needItem{Tone: "warn", Title: q.Name + " holds " + plural(q.Available, "message"), Href: "/sqs/" + seg(q.Name)}
			if m := maxBy[q.Name]; m > 0 {
				n.Note = "Messages land here after " + plural(m, "failed receive") + "."
			}
			dlq = append(dlq, n)
		}
	}
	if list, err := c.be.ListAlarms(ctx); err == nil {
		var firing []Alarm
		for _, a := range list {
			if a.State == "ALARM" {
				firing = append(firing, a)
			}
		}
		if len(firing) > 0 {
			title := "1 alarm is firing"
			if len(firing) != 1 {
				title = strconv.Itoa(len(firing)) + " alarms are firing"
			}
			parts := make([]string, 0, len(firing))
			for _, a := range firing {
				p := a.Name
				if !a.UpdatedAt.IsZero() {
					p += ", since " + clockMinute(a.UpdatedAt)
				}
				parts = append(parts, p)
			}
			alarms = append(alarms, needItem{Tone: "bad", Title: title, Note: strings.Join(parts, "; ") + ".", Href: "/cw"})
		}
	}
	c.be.graphMu.Lock()
	c.be.wireAt, c.be.wireDLQ, c.be.wireAlarms = time.Now(), dlq, alarms
	c.be.graphMu.Unlock()
	return dlq, alarms
}

// dlqCap bounds how many dead-letter queues the panel names one by one: a stack
// with forty of them has a worse problem than this panel can say.
const dlqCap = 3

// needsALook is the panel, in the board's order: a dead-letter queue, an alarm,
// what was refused, a function that raised.
func needsALook(dlq, alarms []needItem, raw []TrafficEntry, prefix string) []needItem {
	var out []needItem
	if len(dlq) > dlqCap {
		out = append(out, dlq[:dlqCap]...)
		out = append(out, needItem{Tone: "warn", Title: "and " + plural(len(dlq)-dlqCap, "more dead-letter queue") + " hold messages", Href: "/sqs"})
	} else {
		out = append(out, dlq...)
	}
	out = append(out, alarms...)
	return append(out, ringNeeds(raw, prefix)...)
}

// wireSig folds what is on the page into the live poll's probe, so the poll
// answers 204 until something the page shows has changed. The ring's own
// counter moves when a call lands (and with it everything the ring says); the
// rate moves as calls age out of the minute; the panel's product half moves
// when a queue fills or an alarm fires.
func wireSig(changes int64, rate int, first bool, dlq, alarms []needItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d.%d.%t", changes, rate, first)
	for _, n := range dlq {
		b.WriteString("|" + n.Title + "|" + n.Note)
	}
	for _, n := range alarms {
		b.WriteString("|" + n.Title + "|" + n.Note)
	}
	return contentHash(b.String())[:12]
}

// wireProbe is the live poll's question: has anything the page shows changed?
// It is cheap on purpose (a counter, a count, and the product's reads, which are
// cached), because the page asks every second and a half and most answers are
// "no".
func (c *Console) wireProbe(r *http.Request) wireView {
	v := wireView{
		Prefix: c.prefix, Endpoint: endpointHost(r), Region: c.id.RegionName(),
		Enabled: c.rec != nil, Kept: trafficCap,
	}
	if c.rec == nil {
		// Capture is off: no ring, but a dead-letter queue and a firing alarm are
		// still facts of the product.
		v.dlq, v.alarms = c.productNeeds(r.Context())
		v.Needs = needsALook(v.dlq, v.alarms, nil, c.prefix)
		return v
	}
	changes := c.rec.Changes()
	// A stack nobody has touched: nothing exists and nothing has ever been
	// recorded. Both halves, because someone who cleared the wire is not new,
	// and neither is someone who deleted everything and is watching traffic.
	// Asked only while nothing has been recorded: the glance reads every service.
	if changes == 0 {
		g := c.glanceSnapshot(r.Context())
		v.FirstRun = len(g.Services) == 0
	}
	v.dlq, v.alarms = c.productNeeds(r.Context())
	v.Hash = wireSig(changes, c.rec.pulse(time.Now()), v.FirstRun, v.dlq, v.alarms)
	return v
}

// wireData is the whole page: the probe, and then the ring read once for the
// rows, the window's figures and the panel.
func (c *Console) wireData(r *http.Request) wireView {
	return c.withRows(c.wireProbe(r))
}

// withRows completes a probe into the page's view.
func (c *Console) withRows(v wireView) wireView {
	if c.rec == nil {
		return v
	}
	raw := c.rec.Entries(0)
	v.LastSeq = c.rec.LastSeq()
	v.Stats = statsOf(raw, time.Now())
	v.Needs = needsALook(v.dlq, v.alarms, raw, c.prefix)
	if v.FirstRun {
		v.Env = firstRunEnv(v.Endpoint, v.Region)
	}
	v.Rows = wireRows(raw)
	v.Services = servicesOf(raw)
	for _, row := range v.Rows {
		if row.State == "refused" || row.State == "refusedhere" {
			v.RefusedN++
		}
	}
	return v
}

// firstRunEnv is the block the first-run wire offers to copy: what an SDK reads
// to talk to this instance. The credentials are the placeholders every SDK
// accepts here, and the endpoint and region are this instance's.
func firstRunEnv(endpoint, region string) string {
	return "export AWS_ENDPOINT_URL=http://" + endpoint + "\n" +
		"export AWS_ACCESS_KEY_ID=test\n" +
		"export AWS_SECRET_ACCESS_KEY=test\n" +
		"export AWS_REGION=" + region
}

// wireLive is whether the rail calls the wire "live": some call started in the
// last minute. Otherwise it is "quiet", which is also what a stack nothing has
// called says.
func (c *Console) wireLive() bool {
	return c.rec != nil && c.rec.pulse(time.Now()) > 0
}

// wireNote is the word the rail prints beside "The wire".
func (c *Console) wireNote() string {
	if c.wireLive() {
		return "live"
	}
	return "quiet"
}
