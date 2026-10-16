package console

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// ---- Traffic ----

func (c *Console) traffic(w http.ResponseWriter, r *http.Request) {
	// The wire is registered as a SUBTREE ("GET {prefix}/"), so without this
	// check every mistyped or stale URL under the console silently rendered the
	// home page with "The wire" lit in the rail — the console answering "where
	// am I" with a confident lie. Anything that is not the wire's own path is a
	// miss, and says so.
	if p := strings.TrimSuffix(r.URL.Path, "/"); p != strings.TrimSuffix(c.prefix, "/") && p != c.prefix+"/traffic" {
		c.notFound(w, r)
		return
	}
	c.render(w, r, "traffic", map[string]any{
		"Entries": c.trafficEntries(0), "Enabled": c.rec != nil, "Title": "Traffic",
		"Deck": c.deckData(r),
	})
}

// notFound renders a miss at 404, with the closest real section guessed from
// the path. A stale bookmark is the common cause, so naming a destination beats
// naming the failure.
func (c *Console) notFound(w http.ResponseWriter, r *http.Request) {
	miss := strings.TrimPrefix(r.URL.Path, c.prefix)
	seg := strings.Trim(miss, "/")
	if i := strings.Index(seg, "/"); i > 0 {
		seg = seg[:i]
	}
	guess, guessName := c.guessSection(seg)
	w.WriteHeader(http.StatusNotFound)
	c.render(w, r, "notfound", map[string]any{
		"Title": "Not found", "Miss": miss, "Guess": guess, "GuessName": guessName,
	})
}

// failPage draws a failed page load inside the shell, so the rail is still
// there and the way back is one click: the bare error fragment it replaces was
// a document with no chrome. The list pane is not drawn: it is built from the
// service's own listing, which may be the very call that failed.
func (c *Console) failPage(w http.ResponseWriter, r *http.Request, err error) {
	f := failView(err)
	seg := strings.Trim(strings.TrimPrefix(r.URL.Path, c.prefix), "/")
	if i := strings.Index(seg, "/"); i > 0 {
		seg = seg[:i]
	}
	back, backName := c.guessSection(seg)
	status, heading := http.StatusBadRequest, "That could not be opened"
	// "Not found" is only the page's heading and status, so it is read from how
	// the service said it: most name it in the code, and CloudFormation answers
	// ValidationError with "Stack with id X does not exist".
	msg := strings.ToLower(f.Message)
	if strings.Contains(f.Code, "NotFound") || strings.Contains(f.Code, "NoSuch") ||
		strings.Contains(f.Code, "NonExistent") || strings.Contains(f.Code, "DoesNotExist") ||
		strings.Contains(msg, "does not exist") || strings.Contains(msg, "not found") {
		status, heading = http.StatusNotFound, "Not found"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	c.render(w, r, "fail_page", map[string]any{
		"Title": heading, "Heading": heading, "Failure": f, "Back": back, "BackName": backName,
		"Path": strings.TrimPrefix(r.URL.Path, c.prefix),
	})
}

// guessSection turns a missed path segment into a destination. It matches three
// ways, in order: the section key, a common noun for what the section holds, and
// a shared prefix — which is what catches an ordinary typo like /lamda.
func (c *Console) guessSection(seg string) (url, name string) {
	seg = strings.ToLower(seg)
	if seg == "" {
		return "", ""
	}
	if key, ok := sectionNouns[strings.TrimSuffix(seg, "s")]; ok {
		if e, ok := catalogOf(key); ok {
			return c.prefix + "/" + e.Key, e.Label
		}
	}
	for _, e := range append(append([]svcEntry{}, catalog...), surfaces...) {
		if e.Key == seg {
			return c.prefix + "/" + e.Key, e.Label
		}
	}
	if len(seg) >= 3 {
		for _, e := range append(append([]svcEntry{}, catalog...), surfaces...) {
			if strings.HasPrefix(e.Key, seg[:3]) || strings.HasPrefix(strings.ToLower(e.Label), seg[:3]) {
				return c.prefix + "/" + e.Key, e.Label
			}
		}
	}
	return "", ""
}

// sectionNouns maps what a person calls the thing onto the service that holds
// it. Someone who lands on /queues was not guessing at a service name.
var sectionNouns = map[string]string{
	"queue": "sqs", "topic": "sns", "subscription": "sns",
	"bucket": "s3", "object": "s3", "table": "ddb", "item": "ddb",
	"function": "lambda", "fn": "lambda", "stream": "kinesis", "shard": "kinesis",
	"rule": "eb", "bus": "eb", "event": "eb",
	"secret": "sm", "parameter": "ssm", "param": "ssm", "key": "kms",
	"stack": "cfn", "api": "apigw", "route": "apigw",
	"role": "iam", "user": "iam", "policy": "iam", "principal": "iam",
}

func (c *Console) trafficFeed(w http.ResponseWriter, r *http.Request) {
	// Cheap probe first: the idle tick (the common case) must not copy and
	// format the whole ring just to throw it away on a 204.
	hash := "0"
	if c.rec != nil {
		if s := c.rec.LastSeq(); s > 0 {
			hash = strconv.FormatInt(s, 10)
		}
	}
	if liveUnchanged(w, r, hash) {
		return
	}
	entries := c.trafficEntries(0)
	// Poll returns only the rows region (traffic_rows); the filter state lives on
	// the outer wrapper the poll never touches.
	c.partial(w, "traffic_rows", map[string]any{"Entries": entries, "Hash": hash, "Endpoint": endpointHost(r)})
}

// resWireRows is how many of a resource's calls its dock shows. The dock is a
// glance; the wire is the list.
const resWireRows = 8

// trafficFor renders the wire dock under a resource: the recorded calls made
// to that one resource, newest first. svc is the console's service key, which
// is the label the recorder files a call under, and res is the name it records
// (a queue's name, a bucket's).
func (c *Console) trafficFor(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc, res := q.Get("svc"), q.Get("res")
	hash := "0"
	if c.rec != nil {
		if s := c.rec.LastSeq(); s > 0 {
			hash = strconv.FormatInt(s, 10)
		}
	}
	if liveUnchanged(w, r, hash) {
		return
	}
	var rows []trafficRow
	more, total := 0, 0
	var tally [4]int // refused, denied, not built, failed
	count := func(state string) {
		switch state {
		case "refused", "refusedhere":
			tally[0]++
		case "denied":
			tally[1]++
		case "notbuilt":
			tally[2]++
		case "error":
			tally[3]++
		}
	}
	if c.rec != nil {
		for _, e := range c.rec.Entries(0) { // newest first
			if e.Service != svc || e.Resource != res {
				continue
			}
			total++
			if len(rows) == resWireRows {
				more++
				count(callState(e.Status, e.Failure(), factsOfEntry(e)))
				continue
			}
			row := rowOf(e, 0)
			rows = append(rows, row)
			count(row.State)
		}
	}
	c.partial(w, "res_wire_rows", map[string]any{
		"Svc": svc, "Resource": res, "Noun": q.Get("noun"), "Hash": hash,
		"Rows": rows, "More": more, "Enabled": c.rec != nil,
		"Total": total, "Tally": tallyOf(tally),
	})
}

// wireTally is one clause of the dock's collapsed sentence: how many of the
// calls were in a state that is not a plain answer, said in that state's word.
type wireTally struct {
	N    int
	Word string
}

// tallyOf names the counts in the order the states are taught (D7), leaving
// out the ones nothing is in.
func tallyOf(n [4]int) []wireTally {
	var out []wireTally
	for i, word := range [4]string{"refused", "denied", "not built", "failed"} {
		if n[i] > 0 {
			out = append(out, wireTally{N: n[i], Word: word})
		}
	}
	return out
}

// trafficEntry renders the inspector drawer for one recorded call.
func (c *Console) trafficEntry(w http.ResponseWriter, r *http.Request) {
	seq, _ := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
	if c.rec == nil {
		http.Error(w, "traffic capture is off", http.StatusNotFound)
		return
	}
	e, ok := c.rec.Get(seq)
	if !ok {
		c.partial(w, "traffic_drawer_gone", map[string]any{})
		return
	}
	req := e.ReqBody
	if strings.Contains(e.CT, "json") {
		req = prettyJSON(req)
	}
	resp := e.RespBody
	if strings.Contains(e.RespCT, "json") {
		resp = prettyJSON(resp)
	}
	c.partial(w, "traffic_drawer", map[string]any{
		"E": e, "Req": req, "Resp": resp,
		// The key bar offers → only when the call names a resource the console
		// has a page for, and calls it by its kind ("go to the queue").
		"Link": resourceURL(e.Service, e.Resource).Path, "Noun": nounFor(e.Service),
		"Millis": fmtMillis(e.Millis),
		"Time":   clockMillis(e.At),
		"State":  callState(e.Status, e.Failure(), factsOfEntry(e)),
		"Copy":   stateCopyFor(callState(e.Status, e.Failure(), factsOfEntry(e)), e.NotBuilt),
	})
}

// trafficClear empties the recorder ring and hands back the (now empty) rows.
func (c *Console) trafficClear(w http.ResponseWriter, r *http.Request) {
	if c.rec != nil {
		c.rec.Clear()
	}
	c.trafficFeed(w, r)
}

// trafficRow is a display-ready traffic entry.
type trafficRow struct {
	Time     string
	Service  string
	Action   string
	Resource string
	Status   int
	Millis   string
	IsErr    bool
	// State is which of the four outcomes this call had: served, refused,
	// denied or error. IsErr survives beside it because the "errors only"
	// filter is a single boolean and does not care which kind.
	State string
	// StateWord is the state said in a word (Refused, Denied, Not built,
	// Failed), and empty for a served call. The row's colour carries the same
	// thing and cannot be the only thing that does.
	StateWord string
	Body      string
	Curl      string
	Seq       int64
	// Refused is the parsed reason a 4xx/5xx was refused, or nil. Carried on
	// the row rather than looked up in the drawer because the reason belongs
	// where you are already scanning for the failure.
	Refused *Refusal

	// Depth is how far this row nests under the call that caused it. Zero for
	// anything a client asked for directly.
	Depth int
	// Cascade marks internal work the gateway never saw — a bucket
	// notification, a topic fan-out, a rule dispatch.
	Cascade bool
	// Via names what emitted it, when that is not obvious from the action.
	Via string
	// Ref points at the resource's page, when the resource resolves to one.
	Ref resourceRef
}

// trafficEntries renders the ring as the wire shows it: newest first, with the
// work a call caused nested underneath it.
//
// Ordering is the whole difficulty. The ring is newest-first, so a parent —
// which by definition was recorded earlier — sits BELOW its children, and a
// cascade read that way is backwards: you meet the effects before the cause.
// So groups are ordered by the parent's recency and read top-down within a
// group, cause first.
//
// A child can also be stored before its parent, because a notification
// delivered on its own goroutine can finish before the request that triggered
// it returns. Grouping by sequence number rather than by position is what makes
// that harmless.
func (c *Console) trafficEntries(since int64) []trafficRow {
	if c.rec == nil {
		return nil
	}
	raw := c.rec.Entries(since)

	kids := map[int64][]TrafficEntry{}
	known := map[int64]bool{}
	for _, e := range raw {
		known[e.Seq] = true
	}
	for _, e := range raw {
		if e.Parent != 0 && known[e.Parent] {
			kids[e.Parent] = append(kids[e.Parent], e)
		}
	}
	// Children read in the order they happened, which is the order they fanned
	// out — the opposite of the ring's.
	for k := range kids {
		sort.Slice(kids[k], func(i, j int) bool { return kids[k][i].Seq < kids[k][j].Seq })
	}

	rows := make([]trafficRow, 0, len(raw))
	var emit func(e TrafficEntry, depth int)
	emit = func(e TrafficEntry, depth int) {
		rows = append(rows, rowOf(e, depth))
		for _, k := range kids[e.Seq] {
			emit(k, depth+1)
		}
	}
	for _, e := range raw {
		// A cascade whose parent is still in the window is emitted with it. One
		// whose parent has aged out stands alone rather than disappearing.
		if e.Parent != 0 && known[e.Parent] {
			continue
		}
		emit(e, 0)
	}
	return rows
}

// fmtMillis is a duration as the wire column shows it: two significant
// places under ten milliseconds, whole milliseconds above. Full float
// precision ("0.215416") made every row a different width and said nothing a
// person reads a latency for.
func fmtMillis(ms float64) string {
	switch {
	case ms < 10:
		return strconv.FormatFloat(ms, 'f', 2, 64)
	case ms < 100:
		return strconv.FormatFloat(ms, 'f', 1, 64)
	}
	return strconv.FormatFloat(ms, 'f', 0, 64)
}

func rowOf(e TrafficEntry, depth int) trafficRow {
	ref := e.Failure()
	state := callState(e.Status, ref, factsOfEntry(e))
	word := ""
	if state != "served" {
		word = stateCopyFor(state, e.NotBuilt).Label
	}
	return trafficRow{
		Time:    clockMillis(e.At),
		Service: e.Service, Action: e.Action, Resource: e.Resource,
		Status: e.Status, Millis: fmtMillis(e.Millis),
		IsErr: e.Status >= 400, Body: e.ReqBody, Curl: e.Curl(), Seq: e.Seq,
		Refused: ref, State: state, StateWord: word,
		Depth: depth, Cascade: e.IsCascade(), Via: e.Via,
		// Every row names a resource and none of them was a link, on the
		// console's busiest surface. Pure string work, so it costs nothing at
		// five hundred rows a poll.
		Ref: resourceURL(e.Service, e.Resource),
	}
}

// wireFacts are the provenance headers a service stamped on a response.
type wireFacts struct {
	Denied   string // "iam"
	NotBuilt string // "declined" | "deferred"
	Local    string // "1"
	Stub     string // "1": a legacy stub table's answer
}

func factsOf(h http.Header) wireFacts {
	return wireFacts{
		Denied:   h.Get(awshttp.DeniedHeader),
		NotBuilt: h.Get(awshttp.NotBuiltHeader),
		Local:    h.Get(awshttp.LocalLimitHeader),
		Stub:     h.Get(awshttp.LegacyStubHeader),
	}
}

func factsOfEntry(e TrafficEntry) wireFacts {
	return wireFacts{Denied: e.Denied, NotBuilt: e.NotBuilt, Local: e.LocalLimit, Stub: e.LegacyStub}
}

// callState separates the ways a call fails to be served. They are different
// events and they send you to different places, so each is decided from a fact
// the service stamped and not from a status or the spelling of a code:
//
//	notbuilt     the operation is one doze-aws does not build. Nothing is
//	             wrong with the request, and nothing broke.
//	denied       a policy evaluated and said no. The request was well formed;
//	             the fix is the policy. (A 403 alone is not this: S3's
//	             BlockPublicPolicy and an object-lock refusal are 403s no
//	             policy decided.)
//	error        doze-aws itself broke: a 5xx that is not a refusal. A bug here
//	             rather than in the caller.
//	refusedhere  a 4xx for a limit that is doze-aws's own: AWS would accept it.
//	refused      a 4xx: the request breaks a rule of the service.
//
// A 501, or the answer of an older stub table (stamped X-Doze-Legacy-Stub by
// the service, whatever code it carries), is notbuilt. This read the code and
// the message's spelling once, which called a real UnsupportedOperationException
// a stub and CloudFormation's stubbed ValidationError a refusal of the input.
// The stamp goes when the model sweep's baseline has no legacystub entries.
func callState(status int, ref *Refusal, f wireFacts) string {
	switch {
	case f.NotBuilt != "" || f.Stub != "" || status == http.StatusNotImplemented:
		return "notbuilt"
	case f.Denied != "":
		return "denied"
	case f.Local != "" && status >= 400:
		return "refusedhere"
	case status >= 500:
		return "error"
	case status >= 400:
		return "refused"
	}
	return "served"
}

// stateView is the words for a failure state, written once. Label is the word
// as well as the colour (the state must read without it); Why is the sentence
// under it; Link, when set, is where the fix lives.
type stateView struct {
	Label    string
	Why      string
	LinkPath string // prefix-relative
	LinkText string
}

func stateCopyFor(state, notBuilt string) stateView {
	switch state {
	case "notbuilt":
		why := "doze-aws does not build this operation, and says so rather than pretending. Nothing is wrong with the request."
		switch notBuilt {
		case "declined":
			why = "doze-aws declines this operation: building it would mean pretending to something that is not here. Nothing is wrong with the request."
		case "deferred":
			why = "doze-aws has not built this operation yet. Nothing is wrong with the request."
		}
		return stateView{Label: "Not built", Why: why}
	case "denied":
		return stateView{Label: "Denied",
			Why:      "A policy decided this, not the input: the request was well formed. The fix is the policy.",
			LinkPath: "/iam", LinkText: "Access log"}
	case "error":
		return stateView{Label: "Failed",
			Why: "This is doze-aws failing rather than refusing. A 5xx here is a bug in the emulator, not in the call, and is worth reporting."}
	case "refusedhere":
		return stateView{Label: "Refused here",
			Why: "AWS would accept this. doze-aws refuses it because of a limit of its own."}
	}
	return stateView{Label: "Refused",
		Why: "doze-aws refused the request: it breaks a rule of the service. The input is what to change."}
}

// ---- The deck ----

// deckView is the stack in two rows: what needs attention, and what exists.
type deckView struct {
	Prefix    string
	Services  []glanceService
	Attention []glanceAttention
	Unwired   []glanceUnwired
	Rate      string
	Recorder  bool
	// FirstRun is a property of the STACK, not a memory of the user: nothing
	// exists and nothing has ever been recorded. Both halves are needed —
	// someone who cleared the wire is not new, and someone who deleted
	// everything and is watching traffic is not new either. It self-heals: one
	// API call or one bucket ends it forever, which is why it needs no
	// persistence to detect.
	FirstRun bool
	Endpoint string
	Hash     string
	// Updated is when this snapshot was taken. A deck that polls silently gives
	// no way to tell "nothing is happening" from "nothing is arriving".
	Updated string
}

func (c *Console) deckData(r *http.Request) deckView {
	g := c.glanceSnapshot(r.Context())
	v := deckView{
		Prefix: c.prefix, Services: g.Services, Attention: g.Attention,
		Unwired: g.Unwired, Rate: g.Rate, Recorder: g.Recorder,
		Endpoint: endpointHost(r),
	}
	// Suppress unwired entirely when NOTHING is wired. On a stack where no
	// resource is connected to any other, "6 wired to nothing" is not an
	// attention signal, it is a description of a stack nobody has wired yet.
	if g.Nodes > 0 && len(g.Unwired) == g.Nodes {
		v.Unwired = nil
	}
	total := 0
	for _, s := range g.Services {
		total += s.Calls
	}
	v.FirstRun = len(g.Services) == 0 && (c.rec == nil || c.rec.LastSeq() == 0)
	v.Hash = deckHash(g)
	v.Updated = clock(time.Now())
	return v
}

// deckHash is the live-poll probe. The spark buckets are included deliberately:
// on an idle stack — exactly when the 204 matters — they are all zero and the
// hash is stable, and when there IS traffic the wire below is already morphing.
func deckHash(g glanceResponse) string {
	h := fnv.New64a()
	for _, s := range g.Services {
		fmt.Fprintf(h, "%s|%s|%s|%v|%v;", s.Svc, s.Label, s.State, s.Warn, s.Spark)
	}
	for _, a := range g.Attention {
		fmt.Fprintf(h, "a:%s;", a.Slug)
	}
	fmt.Fprintf(h, "u:%d;r:%s", len(g.Unwired), g.Rate)
	return strconv.FormatUint(h.Sum64(), 36)
}

func (c *Console) deck(w http.ResponseWriter, r *http.Request) {
	v := c.deckData(r)
	if liveUnchanged(w, r, v.Hash) {
		return
	}
	// partial takes a map; the deck is a struct because it has enough shape to
	// deserve one. One line of adaptation beats loosening the helper.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.tmpl.ExecuteTemplate(w, "deck", v); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
