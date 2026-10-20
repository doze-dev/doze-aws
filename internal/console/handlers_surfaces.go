package console

import (
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
		"Wire": c.wireData(r), "Enabled": c.rec != nil, "Title": "Traffic",
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
	// While the feed is paused the page asks only how many calls it is missing:
	// ?after=<the newest call it holds>. A number, not a page, so a reader who
	// paused to read a call is not made to wait on a render for it.
	if after := r.URL.Query().Get("after"); after != "" {
		n := int64(0)
		if c.rec != nil {
			since, _ := strconv.ParseInt(after, 10, 64)
			n = int64(len(c.rec.Entries(since)))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(strconv.FormatInt(n, 10)))
		return
	}
	// The probe first: the idle tick (the common case) must not copy and format
	// the whole ring just to throw it away on a 204. It is the ring's counter, the
	// rate, and what "Needs a look" says, so the page changes when any of those do.
	v := c.wireProbe(r)
	if liveUnchanged(w, r, v.Hash) {
		return
	}
	v = c.withRows(v)
	// The poll returns the region (traffic_rows); the filter state and the paused
	// flag live on the outer wrapper the poll never touches.
	c.partial(w, "traffic_rows", map[string]any{"Wire": v, "Hash": v.Hash})
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
		if s := c.rec.Changes(); s > 0 {
			hash = strconv.FormatInt(s, 10)
		}
	}
	if liveUnchanged(w, r, hash) {
		return
	}
	var rows []trafficRow
	more, total := 0, 0
	var tally [5]int // refused, denied, not built, failed, threw
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
		case "threw":
			tally[4]++
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
func tallyOf(n [5]int) []wireTally {
	var out []wireTally
	for i, word := range [5]string{"refused", "denied", "not built", "failed", "threw"} {
		if n[i] > 0 {
			out = append(out, wireTally{N: n[i], Word: word})
		}
	}
	return out
}

// trafficClear empties the recorder ring and hands back the (now empty) page.
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
	Action   string // as the service names it; Label is what a row says
	Label    string
	Resource string
	Status   int
	Millis   string
	IsErr    bool
	// State is which outcome this call had: served, refused, refusedhere,
	// denied, notbuilt, error or threw. IsErr survives beside it because the
	// "errors" filter is a single boolean and does not care which kind.
	State string
	// StateWord is the state said in a word (Refused, Denied, Not built,
	// Failed), and empty for a served call. The row's colour carries the same
	// thing and cannot be the only thing that does.
	StateWord string
	// Answer is the Answer column: "200", "400 refused", "it threw".
	Answer string
	Body   string
	Curl   string
	Seq    int64
	// Refused is the parsed reason a 4xx/5xx was refused, or nil. Carried on
	// the row rather than looked up in the drawer because the reason belongs
	// where you are already scanning for the failure.
	Refused *Refusal
	// Why is that reason as a row says it beside the resource: the service's
	// message, or what the function raised.
	Why string
	// WhyTitle is the same with the code the service gave, for the hover: the
	// row says the sentence, and the code is what a caller matches on.
	WhyTitle string

	// Depth is how far this row nests under the call that caused it. Zero for
	// anything a client asked for directly.
	Depth int
	// Cascade marks internal work the gateway never saw — a bucket
	// notification, a topic fan-out, a rule dispatch.
	Cascade bool
	// Via names what emitted it, in words ("the queue's event source mapping"),
	// when that is not obvious from the action.
	Via string
	// Ref points at the resource's page, when the resource resolves to one.
	Ref resourceRef

	// Wait marks a long poll: its milliseconds were spent waiting, so the row
	// says seconds, muted, and draws no lane.
	Wait bool
	// LaneLeft and LaneWidth place the call's time bar, in percent of the
	// slowest call in the window; LaneBad paints a failed one.
	LaneLeft, LaneWidth int
	LaneBad             bool
}

// wireRows renders the ring as the wire shows it: newest first, with the work a
// call caused nested underneath it.
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
func wireRows(raw []TrafficEntry) []trafficRow {
	kids := map[int64][]TrafficEntry{}
	known := map[int64]bool{}
	for _, e := range raw {
		known[e.Seq] = true
	}
	// The lanes share one scale: the slowest call in the window, leaving out a
	// long poll's wait. A bar is how long a call took against the slowest one,
	// so two rows can be compared by eye, and the window's "Slowest" is the bar
	// that fills its lane.
	scale := 1.0
	for _, e := range raw {
		if !waited(e) && e.Millis > scale {
			scale = e.Millis
		}
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
	var emit func(e TrafficEntry, depth int, parentAt time.Time)
	emit = func(e TrafficEntry, depth int, parentAt time.Time) {
		row := rowOf(e, depth)
		if !row.Wait {
			offset := 0.0
			if depth > 0 {
				offset = float64(e.At.Sub(parentAt)) / float64(time.Millisecond)
			}
			left := int(offset / scale * 100)
			if left < 0 {
				left = 0
			}
			if left > 96 {
				left = 96
			}
			width := int(e.Millis/scale*100 + 0.5)
			if width < 2 {
				width = 2
			}
			if left+width > 100 {
				width = 100 - left
			}
			row.LaneLeft, row.LaneWidth = left, width
			row.LaneBad = row.State == "error" || row.State == "threw"
		}
		rows = append(rows, row)
		for _, k := range kids[e.Seq] {
			emit(k, depth+1, e.At)
		}
	}
	roots := make([]TrafficEntry, 0, len(raw))
	for _, e := range raw {
		// A cascade whose parent is still in the window is emitted with it. One
		// whose parent has aged out stands alone rather than disappearing.
		if e.Parent != 0 && known[e.Parent] {
			continue
		}
		roots = append(roots, e)
	}
	// Newest first by when the call STARTED, which is what the When column
	// says. The ring is in the order calls finished, and a long poll that waited
	// twenty seconds finishes last: ordered by the ring it would sit above calls
	// that happened after it began, under a time that is older than theirs.
	sort.SliceStable(roots, func(i, j int) bool {
		if !roots[i].At.Equal(roots[j].At) {
			return roots[i].At.After(roots[j].At)
		}
		return roots[i].Seq > roots[j].Seq
	})
	for _, e := range roots {
		emit(e, 0, e.At)
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

// millisCell is the ms column: milliseconds, or a long poll's wait in seconds.
func millisCell(e TrafficEntry) string {
	if waited(e) {
		return strconv.Itoa(int(e.Millis/1000+0.5)) + " s"
	}
	return fmtMillis(e.Millis)
}

func rowOf(e TrafficEntry, depth int) trafficRow {
	ref := e.Failure()
	state := callState(e.Status, ref, factsOfEntry(e))
	word := ""
	if state != "served" {
		word = stateCopyFor(state, "").Label
	}
	return trafficRow{
		Time:    clockMillis(e.At),
		Service: e.Service, Action: e.Action, Label: actionLabel(e.Action), Resource: e.Resource,
		Status: e.Status, Millis: millisCell(e), Wait: waited(e),
		IsErr: e.Status >= 400 || state == "threw", Body: e.ReqBody, Curl: e.Curl(), Seq: e.Seq,
		Refused: ref, State: state, StateWord: word,
		Answer: answerOf(e.Status, state, word), Why: whyOf(state, ref), WhyTitle: whyTitle(ref),
		Depth: depth, Cascade: e.IsCascade(), Via: viaWords(e),
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
	FnError  string // "Unhandled": a Lambda function raised (X-Amz-Function-Error)
}

// functionErrorHeader is how Lambda says an invoke was served and the function
// it ran was not: a 200 with this header (lambda/invoke.go sets it, with the
// value AWS gives, "Unhandled"). It is the only fact the wire has; the body is
// whatever the function returned.
const functionErrorHeader = "X-Amz-Function-Error"

func factsOf(h http.Header) wireFacts {
	return wireFacts{
		Denied:   h.Get(awshttp.DeniedHeader),
		NotBuilt: h.Get(awshttp.NotBuiltHeader),
		Local:    h.Get(awshttp.LocalLimitHeader),
		Stub:     h.Get(awshttp.LegacyStubHeader),
		FnError:  h.Get(functionErrorHeader),
	}
}

func factsOfEntry(e TrafficEntry) wireFacts {
	return wireFacts{Denied: e.Denied, NotBuilt: e.NotBuilt, Local: e.LocalLimit, Stub: e.LegacyStub, FnError: e.FnError}
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
//	threw        the call was served and the code it ran raised: a Lambda
//	             invoke answers 200 with X-Amz-Function-Error. Nothing the
//	             caller sent was wrong and nothing in doze-aws broke; the
//	             function is what to read.
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
	case f.FnError != "":
		return "threw"
	}
	return "served"
}

// serviceNotRunning is the value of X-Doze-Local-Limit the console's own
// transport stamps on its 503 for a service the stack does not run.
const serviceNotRunning = "service-not-running"

// detailOf is the fact stateCopyFor takes for a state, read from the stamps
// (and, for a function error, the resource the call named).
func detailOf(state string, f wireFacts, resource string) string {
	switch state {
	case "notbuilt":
		return f.NotBuilt
	case "refusedhere":
		return f.Local
	case "threw":
		return resource
	}
	return ""
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

// stateCopyFor is the words for a state. detail is the one fact the state's
// stamp carries beyond the state itself: "declined" or "deferred" for a not
// built operation (X-Doze-Not-Built), the stamp's value for a local limit
// (X-Doze-Local-Limit: "service-not-running" is the console's own answer for a
// service the stack was started without), and the function's name for one that
// threw. An empty detail gives the general sentence.
func stateCopyFor(state, detail string) stateView {
	switch state {
	case "notbuilt":
		why := "doze-aws does not build this operation, and says so rather than pretending. Nothing is wrong with the request."
		switch detail {
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
	case "threw":
		who := "your function"
		if detail != "" {
			who = "the function " + detail
		}
		return stateView{Label: "It threw",
			Why: "The call was served and " + who + " raised. Lambda answers 200 and flags the error in X-Amz-Function-Error; the body says what it threw. Nothing here is a bug in doze-aws."}
	case "refusedhere":
		if detail == serviceNotRunning {
			return stateView{Label: "Refused here",
				Why: "This stack was started without this service, so nothing answers for it. Start it with the service enabled; the request is not what to change."}
		}
		return stateView{Label: "Refused here",
			Why: "AWS would accept this. doze-aws refuses it because of a limit of its own."}
	}
	return stateView{Label: "Refused",
		Why: "doze-aws refused the request: it breaks a rule of the service. The input is what to change."}
}
