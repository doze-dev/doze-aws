package console

// The wire's inspector (boards Wire-Inspect and Wire-Refused): one call, opened.
//
// A call that was served has three tabs: what it was sent, the answer, and what
// it caused. A call that was not served has no tabs; its page is the reason,
// what it was sent, the resource it was sent to, and the same operation in the
// ledger, because the question a refusal asks is "what do I change", and all
// four answers are what changes it.
//
// Everything on it is read from the recorded call, from the ledger (SUPPORT.md,
// the table the fidelity panel reads) and, for a queue, from the queue. Nothing
// is composed from a guess: a sentence that explains a refusal is said only
// when the facts it states are the facts.

import (
	"html"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/doze-dev/doze-aws/docs"
)

// trafficEntry renders the inspector for one recorded call.
func (c *Console) trafficEntry(w http.ResponseWriter, r *http.Request) {
	seq, _ := strconv.ParseInt(r.URL.Query().Get("seq"), 10, 64)
	if c.rec == nil {
		http.Error(w, "traffic capture is off", http.StatusNotFound)
		return
	}
	e, ok := c.rec.Get(seq)
	if !ok {
		c.partial(w, "traffic_drawer_gone", map[string]any{"Kept": trafficCap})
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
	failure := e.Failure()
	state := callState(e.Status, failure, factsOfEntry(e))
	copyV := stateCopyFor(state, detailOf(state, factsOfEntry(e), e.Resource))
	ref := resourceURL(e.Service, e.Resource)
	data := map[string]any{
		"E": e, "Req": req, "Resp": resp,
		"ReqHTML": wireJSON(req), "RespHTML": wireJSON(resp),
		// The key bar offers → only when the call names a resource the console
		// has a page for, and calls it by its kind ("go to the queue").
		"Link": ref.Path, "Noun": nounFor(e.Service),
		"Millis": fmtMillis(e.Millis),
		"Time":   clockMillis(e.At),
		"State":  state,
		"Copy":   copyV,
		"Answer": answerOf(e.Status, state, copyV.Label),
		"Label":  actionLabel(e.Action),
		// The operation as the SDK names it: the target header when there is one
		// (AmazonSQS.SendMessage), else the action the recorder read.
		"Operation": firstNonEmpty(e.Target, e.Action),
		"CurlShown": e.curlShown(),
		"Failure":   failure,
		"Ledger":    ledgerFor(e.Service, actionLabel(e.Action)),
		"Caused":    c.caused(e.Seq),
	}
	if failure != nil {
		data["CopyError"] = strings.TrimSpace(strings.Trim(failure.Code+": "+failure.Message, ": "))
	}
	data["Context"] = c.resourceContext(r, e, ref, state, failure)
	c.partial(w, "traffic_drawer", data)
}

// causedView is one piece of work a call set off, as the "What it caused" card
// shows it.
type causedView struct {
	Seq      int64
	Service  string
	Label    string
	Resource string
	Millis   string
	Via      string
	Detail   string
	State    string
	Answer   string
}

// caused is the work doze-aws did itself because of one call: the rows nested
// under it on the wire, in the order they fanned out. What a function calls from
// inside, with an SDK, is its own call and is not here.
func (c *Console) caused(seq int64) []causedView {
	var out []causedView
	for _, k := range c.rec.Entries(0) {
		if k.Parent != seq {
			continue
		}
		f := k.Failure()
		st := callState(k.Status, f, factsOfEntry(k))
		out = append(out, causedView{
			Seq: k.Seq, Service: k.Service, Label: actionLabel(k.Action), Resource: k.Resource,
			Millis: millisCell(k), Via: viaWords(k), Detail: printedLines(k.Detail),
			State: st, Answer: answerOf(k.Status, st, stateCopyFor(st, "").Label),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// printedLines is what a function wrote of its own in a Lambda log tail: the
// START, END and REPORT lines Lambda puts around an invocation are left out.
// The card under the call that caused an invoke shows this ("what the function
// printed is on its row"); the inspector's Logs section shows the whole tail.
func printedLines(tail string) string {
	var out []string
	for _, l := range strings.Split(tail, "\n") {
		if strings.HasPrefix(l, "START RequestId: ") || strings.HasPrefix(l, "END RequestId: ") || strings.HasPrefix(l, "REPORT RequestId: ") {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// curlShown is the command as the inspector prints it. The body is already on
// the page above it, so the command prints its first member and an ellipsis;
// "Copy as curl" copies the whole command, which is what Curl returns.
func (e TrafficEntry) curlShown() string {
	full := e.Curl()
	body := e.ReqBody
	if body == "" {
		return full
	}
	i := strings.LastIndex(full, "\n  --data '")
	if i < 0 {
		return full
	}
	short := body
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		// {"QueueUrl": …}: the first key, then what was cut.
		if k := firstJSONKey(body); k != "" {
			short = `{"` + k + `": …}`
		}
	} else if len(short) > 48 {
		short = short[:48] + "…"
	}
	return full[:i] + "\n  --data '" + strings.ReplaceAll(short, "'", `'\''`) + "'"
}

// firstJSONKey is the first member name of a JSON object, read without decoding
// the whole body (it may be cut at the recorder's cap and not parse).
func firstJSONKey(body string) string {
	s := strings.TrimSpace(body)
	i := strings.Index(s, `"`)
	if i < 0 {
		return ""
	}
	j := i + 1
	for j < len(s) && s[j] != '"' {
		if s[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(s) {
		return ""
	}
	return s[i+1 : j]
}

// wireJSON colours a pretty-printed JSON document the way the boards draw a
// body: keys, strings and numbers apart. The text is escaped and nothing but the
// spans is added, so a body cannot inject markup. A body that is not JSON comes
// back escaped and plain.
func wireJSON(s string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	t := strings.TrimSpace(s)
	if t[0] != '{' && t[0] != '[' {
		return template.HTML(html.EscapeString(s))
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			tok := s[i : j+1]
			k := j + 1
			for k < len(s) && (s[k] == ' ') {
				k++
			}
			cls := "s"
			if k < len(s) && s[k] == ':' {
				cls = "kw"
			}
			b.WriteString(`<span class="` + cls + `">` + html.EscapeString(tok) + `</span>`)
			i = j + 1
		case ch == '-' || (ch >= '0' && ch <= '9'):
			j := i
			for j < len(s) && strings.IndexByte("-+.eE0123456789", s[j]) >= 0 {
				j++
			}
			b.WriteString(`<span class="n">` + html.EscapeString(s[i:j]) + `</span>`)
			i = j
		case strings.HasPrefix(s[i:], "true"), strings.HasPrefix(s[i:], "false"), strings.HasPrefix(s[i:], "null"):
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			b.WriteString(`<span class="n">` + s[i:j] + `</span>`)
			i = j
		default:
			b.WriteString(html.EscapeString(string(ch)))
			i++
		}
	}
	return template.HTML(b.String())
}

// ---- the ledger card ----

type ledgerView struct {
	Op, Tier, TierLabel, Note, Svc, Label string
}

var (
	ledgerMu    sync.Mutex
	ledgerIndex = map[string]map[string]docs.Row{} // ledger key -> operation -> row
)

// ledgerOps indexes one ledger section by operation, built the first time a
// call to that service is opened. A row bundles operations ("SendMessage /
// SendMessageBatch"), so each token is its own entry.
func ledgerOps(key string) map[string]docs.Row {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if m, ok := ledgerIndex[key]; ok {
		return m
	}
	m := map[string]docs.Row{}
	for _, row := range docs.Rows(key) {
		for _, tok := range strings.FieldsFunc(row.Ops, func(r rune) bool { return r == '/' || r == ',' }) {
			tok = strings.TrimSpace(tok)
			if tok != "" && !strings.ContainsAny(tok, " ()") {
				if _, dup := m[tok]; !dup {
					m[tok] = row
				}
			}
		}
	}
	ledgerIndex[key] = m
	return m
}

// ledgerFor is the ledger's row for an operation of a service, or nil when the
// ledger does not name it (a cascade hop, a path-routed service whose operation
// the recorder could not name). The card is drawn only for a row that exists.
func ledgerFor(svc, op string) *ledgerView {
	if op == "" {
		return nil
	}
	label := svc
	if e, ok := catalogOf(svc); ok {
		label = e.Label
	}
	for _, sec := range infoSectionsFor(svc) {
		if row, ok := ledgerOps(sec)[op]; ok {
			return &ledgerView{Op: op, Tier: row.Tier, TierLabel: row.TierLabel(), Note: row.Note, Svc: svc, Label: label}
		}
	}
	return nil
}

// infoSectionsFor names the ledger sections a console service key covers.
func infoSectionsFor(svc string) []string {
	secs, ok := infoSvc[svc]
	if !ok {
		return []string{svc}
	}
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, s.Key)
	}
	return out
}

// ---- the resource the call was sent to ----

// resCtx is the "The queue it was sent to" card.
type resCtx struct {
	Noun, Name, Path, Svc string
	Pill, Note            string
	Sentence              string
}

// resourceContext reads what the call's own resource says about the refusal.
// Only a queue has more to say today: whether it is standard or FIFO is the fact
// the two refusals about FIFO attributes turn on. Any other resource that has a
// page is shown as a link, with no claim about it.
func (c *Console) resourceContext(r *http.Request, e TrafficEntry, ref resourceRef, state string, f *Refusal) *resCtx {
	if ref.Path == "" || e.IsCascade() {
		return nil
	}
	out := &resCtx{Noun: nounFor(e.Service), Name: ref.Name, Path: ref.Path, Svc: e.Service}
	if e.Service != "sqs" || out.Noun == "" {
		return out
	}
	attrs, err := c.be.queueAttrs(r.Context(), ref.Name)
	if err != nil {
		return out
	}
	fifo := attrs["FifoQueue"] == "true"
	msg := ""
	if f != nil {
		msg = f.Message
	}
	failed := state == "refused" || state == "refusedhere"
	if fifo {
		out.Pill = "FIFO"
		out.Note = "content-based deduplication " + map[bool]string{true: "on", false: "off"}[attrs["ContentBasedDeduplication"] == "true"]
		// The sentence is said only of a refusal that names the thing it is about.
		if failed && strings.Contains(msg, "MessageGroupId") {
			out.Sentence = "This queue is FIFO, so every send names a MessageGroupId, and this request did not."
		}
		return out
	}
	out.Pill = "standard"
	out.Note = "has no FIFO attributes"
	if failed && strings.Contains(msg, "FIFO") {
		out.Sentence = "The request names a parameter that only FIFO queues take, and this queue is standard."
	}
	return out
}
