package console

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
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

// guessSection turns a missed path segment into a destination. It matches three
// ways, in order: the section key, a common noun for what the section holds, and
// a shared prefix — which is what catches an ordinary typo like /lamda.
func (c *Console) guessSection(seg string) (url, name string) {
	seg = strings.ToLower(seg)
	if seg == "" {
		return "", ""
	}
	if key, ok := sectionNouns[strings.TrimSuffix(seg, "s")]; ok {
		for _, s := range railSections {
			if s.Key == key {
				return c.prefix + "/" + s.Key, s.Name
			}
		}
	}
	for _, s := range railSections {
		if s.Key == seg {
			return c.prefix + "/" + s.Key, s.Name
		}
	}
	if len(seg) >= 3 {
		for _, s := range railSections {
			if strings.HasPrefix(s.Key, seg[:3]) || strings.HasPrefix(strings.ToLower(s.Name), seg[:3]) {
				return c.prefix + "/" + s.Key, s.Name
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

// railSections is the console's own list of destinations, used to turn a miss
// into a suggestion. Ordered as the rail is.
var railSections = []struct{ Key, Name string }{
	{"s3", "S3"}, {"ddb", "DynamoDB"},
	{"sqs", "SQS"}, {"sns", "SNS"}, {"eb", "EventBridge"}, {"kinesis", "Kinesis"},
	{"lambda", "Lambda"}, {"apigw", "API Gateway"},
	{"kms", "KMS"}, {"sm", "Secrets Manager"}, {"ssm", "Parameter Store"},
	{"iam", "IAM"}, {"cfn", "CloudFormation"}, {"connect", "Connect"},
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
		"Millis": strconv.FormatFloat(e.Millis, 'f', -1, 64),
		"Time":   e.At.Local().Format("15:04:05.000"),
		"State":  callState(e.Status, e.Failure()),
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
	State    string
	Body     string
	Curl     string
	Seq      int64
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

func rowOf(e TrafficEntry, depth int) trafficRow {
	ref := e.Failure()
	return trafficRow{
		Time:    e.At.Local().Format("15:04:05.000"),
		Service: e.Service, Action: e.Action, Resource: e.Resource,
		Status: e.Status, Millis: strconv.FormatFloat(e.Millis, 'f', -1, 64),
		IsErr: e.Status >= 400, Body: e.ReqBody, Curl: e.Curl(), Seq: e.Seq,
		Refused: ref, State: callState(e.Status, ref),
		Depth:   depth, Cascade: e.IsCascade(), Via: e.Via,
	}
}

// callState separates the three ways a call fails to be served. They are
// genuinely different events and they send you to different places: a REFUSAL
// means the request was malformed and the constraint that caught it is named on
// the row; a DENIAL means the request was fine and a policy said no; an ERROR
// means doze-aws itself broke, which is a bug here rather than in the caller.
func callState(status int, ref *Refusal) string {
	switch {
	case status >= 500:
		return "error"
	case status == 403:
		return "denied"
	case ref != nil && (strings.Contains(ref.Code, "AccessDenied") ||
		strings.Contains(ref.Code, "NotAuthorized") ||
		strings.Contains(ref.Code, "AuthorizationError")):
		return "denied"
	case status >= 400:
		return "refused"
	}
	return "served"
}
