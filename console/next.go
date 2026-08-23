// next.go — a second console, mounted alongside the first.
//
// This is the "colour the verb, not the noun" retake: time is the spine, detail
// pages are shaped by what a resource IS rather than by which AWS team owns it,
// and the whole chromatic budget is spent on what happened to a call rather than
// on telling fourteen services apart.
//
// It is deliberately a PARALLEL surface, not a replacement. It shares the same
// backend (so it reads the same real stack, never a fixture) and the same
// Recorder (so the wire is the same wire), and it registers its own routes on
// its own mux under its own prefix. Nothing in the existing console changes.
//
// Two structural notes, because they are load-bearing rather than incidental:
//
//   - Templates live in templates/next/ and styles in static/next.css. The
//     existing guard tests scan templates/*.html by suffix (a subdirectory has
//     no .html suffix, so it is skipped) and read routes out of console.go
//     specifically. Keeping this surface in its own files means it cannot
//     silently weaken those guards — but it also means it is NOT covered by
//     them, which is a debt to pay before this stops being a prototype.
//   - The nesting walk below duplicates trafficEntries rather than refactoring
//     it. That is on purpose while both consoles are live: sharing it would mean
//     editing the surface that currently ships.
package console

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/next/*.html
var nextTemplateFS embed.FS

// Next is the second console's http.Handler.
type Next struct {
	be     *backend
	mux    *http.ServeMux
	tmpl   *template.Template
	prefix string
	rec    *Recorder
}

// NewNext builds the parallel console over the same peers and recorder as New.
func NewNext(opts Options) (*Next, error) {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "/_next"
	}
	prefix = "/" + strings.Trim(prefix, "/")

	fm := templateFuncs(prefix)
	fm["barPct"] = barPct
	fm["msText"] = msText
	fm["depthPad"] = func(d int) string { return strconv.Itoa(16 + d*22) }

	tmpl, err := template.New("").Funcs(fm).ParseFS(nextTemplateFS, "templates/next/*.html")
	if err != nil {
		return nil, err
	}
	n := &Next{be: newBackend(opts.Peers), tmpl: tmpl, prefix: prefix, rec: opts.Recorder}
	n.routes()
	return n, nil
}

func (n *Next) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Same CSRF posture as the first console. This surface is read-only today,
	// so the check is cheap insurance rather than load-bearing — but it should
	// not be absent the day someone adds a POST.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if origin := r.Header.Get("Origin"); origin != "" && !originMatchesHost(origin, r.Host) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
	}
	n.mux.ServeHTTP(w, r)
}

func (n *Next) routes() {
	m := http.NewServeMux()
	p := n.prefix

	m.Handle("GET "+p+"/static/", http.StripPrefix(p+"/", cacheStatic(http.FileServerFS(staticFS))))

	m.HandleFunc("GET "+p+"/", n.wire)
	m.HandleFunc("GET "+p, n.wire)
	m.HandleFunc("GET "+p+"/wire/feed", n.wireFeed)
	m.HandleFunc("GET "+p+"/pipe/{name}", n.pipe)

	n.mux = m
}

// ---------------------------------------------------------------- the wire

// nextRow is one call on the wire. It carries State rather than a raw status
// because the whole visual thesis is that "doze refused this", "IAM denied
// this" and "the emulator broke" are three different events that every other
// console renders identically.
type nextRow struct {
	Seq      int64
	Time     string
	Service  string
	Action   string
	Resource string
	Status   int
	Millis   float64
	Depth    int
	Cascade  bool
	Via      string
	Refused  *Refusal
	State    string // served | refused | denied | error
	Comparable bool // false for long polls, which must not set the bar scale
}

// stateOf separates the three failure meanings. A 403 carrying an IAM refusal
// code is a DENIAL (a policy decided); any other 4xx is a REFUSAL (an input
// constraint decided); 5xx is the emulator itself failing. Conflating them is
// exactly what makes a cloud console unhelpful at 3am.
func stateOf(status int, ref *Refusal) string {
	switch {
	case status >= 500:
		return "error"
	case status == 403:
		return "denied"
	case ref != nil && (strings.Contains(ref.Code, "AccessDenied") || strings.Contains(ref.Code, "NotAuthorized")):
		return "denied"
	case status >= 400:
		return "refused"
	}
	return "served"
}

// comparable reports whether a call's duration should set the bar scale. A 20s
// long poll is not slow, it is waiting — and letting it set the scale flattens
// every real call to a nub. flint excludes approval gates for the same reason.
func comparable(action string, ms float64) bool {
	if ms >= 3000 && (action == "ReceiveMessage" || strings.HasPrefix(action, "GetRecords")) {
		return false
	}
	return true
}

func barPct(ms, max float64) string {
	if max <= 0 {
		return "2"
	}
	p := ms / max * 100
	if p < 2 {
		p = 2
	}
	if p > 100 {
		p = 100
	}
	return strconv.FormatFloat(p, 'f', 1, 64)
}

func msText(ms float64) string {
	switch {
	case ms >= 1000:
		return strconv.FormatFloat(ms/1000, 'f', 2, 64) + "s"
	case ms >= 10:
		return strconv.FormatFloat(ms, 'f', 0, 64) + "ms"
	}
	return strconv.FormatFloat(ms, 'f', 1, 64) + "ms"
}

// rows walks the ring newest-first and threads caused work under its cause, the
// same shape the first console renders. Kept separate from trafficEntries on
// purpose — see the package note at the top of this file.
func (n *Next) rows() ([]nextRow, float64) {
	if n.rec == nil {
		return nil, 0
	}
	raw := n.rec.Entries(0)

	known := map[int64]bool{}
	for _, e := range raw {
		known[e.Seq] = true
	}
	kids := map[int64][]TrafficEntry{}
	var roots []TrafficEntry
	for _, e := range raw {
		if e.Parent != 0 && known[e.Parent] {
			kids[e.Parent] = append(kids[e.Parent], e)
		} else {
			roots = append(roots, e)
		}
	}
	// Children in the order they happened — the order they fanned out, which is
	// the opposite of the ring's.
	for k := range kids {
		sort.Slice(kids[k], func(i, j int) bool { return kids[k][i].Seq < kids[k][j].Seq })
	}

	var out []nextRow
	var max float64
	var walk func(e TrafficEntry, depth int)
	walk = func(e TrafficEntry, depth int) {
		ref := e.Failure()
		r := nextRow{
			Seq: e.Seq, Time: e.At.Format("15:04:05.000"),
			Service: e.Service, Action: e.Action, Resource: e.Resource,
			Status: e.Status, Millis: e.Millis, Depth: depth,
			Cascade: e.Parent != 0, Via: e.Via, Refused: ref,
			State:      stateOf(e.Status, ref),
			Comparable: comparable(e.Action, e.Millis),
		}
		if r.Comparable && e.Millis > max {
			max = e.Millis
		}
		out = append(out, r)
		for _, k := range kids[e.Seq] {
			walk(k, depth+1)
		}
	}
	for _, e := range roots {
		walk(e, 0)
	}
	return out, max
}

type wireView struct {
	Prefix string
	Nav    []nextGroup
	Rows   []nextRow
	Max    float64
	Hash   string
	Live   bool
	Served int
	Bad    int
}

func (n *Next) wireData(ctx context.Context) wireView {
	rows, max := n.rows()
	v := wireView{Prefix: n.prefix, Nav: n.rail(ctx, "wire"), Rows: rows, Max: max, Live: n.rec != nil}
	for _, r := range rows {
		if r.State == "served" {
			v.Served++
		} else {
			v.Bad++
		}
	}
	if n.rec != nil {
		v.Hash = strconv.FormatInt(n.rec.LastSeq(), 10)
	}
	return v
}

func (n *Next) wire(w http.ResponseWriter, r *http.Request) {
	n.render(w, "shell", n.wireData(r.Context()))
}

func (n *Next) wireFeed(w http.ResponseWriter, r *http.Request) {
	if n.rec != nil && liveUnchanged(w, r, strconv.FormatInt(n.rec.LastSeq(), 10)) {
		return
	}
	n.render(w, "wire_rows", n.wireData(r.Context()))
}

// ---------------------------------------------------------------- the rail

type nextNav struct {
	Label string
	Href  string
	Meta  string
	Warn  bool
	On    bool
}

type nextGroup struct {
	Label string
	Items []nextNav
	Empty string
}

// rail is the thesis in six words: the groups are PHYSICS, not AWS's service
// catalogue. A queue and a stream are both pipes; a bucket and a table are both
// stores. Service survives as an adjective on the row, never as the address.
func (n *Next) rail(ctx context.Context, active string) []nextGroup {
	groups := []nextGroup{{
		Label: "Time",
		Items: []nextNav{{Label: "The wire", Href: n.prefix + "/", On: active == "wire", Meta: "live"}},
	}}

	pipes := nextGroup{Label: "Pipes", Empty: "no queues yet"}
	if qs, err := n.be.ListQueues(ctx); err == nil {
		for _, q := range qs {
			it := nextNav{
				Label: q.Name,
				Href:  n.prefix + "/pipe/" + q.Name,
				Meta:  fmt.Sprintf("%d·%d", q.Available, q.InFlight),
				On:    active == "pipe:"+q.Name,
			}
			if q.DLQ != "" && q.Available > 0 {
				it.Warn = true
			}
			pipes.Items = append(pipes.Items, it)
		}
	}
	groups = append(groups, pipes)

	stores := nextGroup{Label: "Stores", Empty: "no buckets yet"}
	if bs, err := n.be.ListBuckets(ctx); err == nil {
		for _, b := range bs {
			stores.Items = append(stores.Items, nextNav{Label: b.Name, Href: n.prefix + "/", Meta: "bucket"})
		}
	}
	groups = append(groups, stores)

	return groups
}

// ---------------------------------------------------------------- the PIPE

type pipeView struct {
	Prefix   string
	Nav      []nextGroup
	Name     string
	Visible  int
	InFlight int
	Delayed  int
	MaxRecv  int
	DLQ      string
	Sources  []string
	Head     []SQSMessage
	Total    int
	VisPct   string
	FlyPct   string
	DelPct   string
}

// pipe renders a queue as what it physically is: something with depth, a head
// you can look at without consuming, and somewhere it goes when it drains.
func (n *Next) pipe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ctx := r.Context()

	attrs, msgs, err := n.be.QueueDetail(ctx, name)
	if err != nil {
		http.Error(w, "queue not found: "+name, http.StatusNotFound)
		return
	}
	for i := range msgs {
		msgs[i].Sum = summarize(msgs[i].Body)
	}

	v := pipeView{
		Prefix:   n.prefix,
		Nav:      n.rail(ctx, "pipe:"+name),
		Name:     name,
		Visible:  atoi(attrs["ApproximateNumberOfMessages"]),
		InFlight: atoi(attrs["ApproximateNumberOfMessagesNotVisible"]),
		Delayed:  atoi(attrs["ApproximateNumberOfMessagesDelayed"]),
		DLQ:      sqsConfigOf(attrs).DLQ,
		Sources:  n.be.DLQSources(ctx, name),
		Head:     msgs,
	}
	v.MaxRecv = atoi(sqsConfigOf(attrs).MaxReceive)
	v.Total = v.Visible + v.InFlight + v.Delayed
	v.VisPct, v.FlyPct, v.DelPct = share(v.Visible, v.Total), share(v.InFlight, v.Total), share(v.Delayed, v.Total)

	n.render(w, "pipe_shell", v)
}

// share renders one segment of the depth gauge. An empty queue still draws a
// track, so the gauge reads as "nothing here" rather than as a missing element.
func share(part, total int) string {
	if total <= 0 {
		return "0"
	}
	return strconv.FormatFloat(float64(part)/float64(total)*100, 'f', 1, 64)
}

// ---------------------------------------------------------------- plumbing

func (n *Next) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var buf strings.Builder
	if err := n.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		// html/template writes its error INTO the output, so a broken template
		// 200s with the error in the body. Fail loudly instead.
		http.Error(w, "template "+name+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte(buf.String()))
}

var _ = time.Now
