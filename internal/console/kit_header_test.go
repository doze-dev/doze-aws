package console

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

// The kit's pieces are executed on their own here, so what they draw is held
// without a Stack and without a service's page in the way: a service that has
// not adopted a part of the kit is still held to the rest.

func kitTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("").Funcs(templateFuncs("/_console", awsident.Default())).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	return tmpl
}

func kitRender(t *testing.T, name string, data map[string]any) string {
	t.Helper()
	var b bytes.Buffer
	if err := kitTemplates(t).ExecuteTemplate(&b, name, data); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func headerData(extra map[string]any) map[string]any {
	d := map[string]any{
		"Svc": "sqs", "Title": "orders", "Prefix": "/_console",
		"Delete": map[string]any{"URL": "/_console/sqs/orders/delete-queue", "Confirm": "Delete it?", "Label": "Delete queue"},
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// Delete is the destructive action and it sits beside the name, in the header's
// own row, confirmed (decision D2). Anatomy's rule 3 ("under ···") is the
// board's own contradiction: 119 boards draw Delete beside the name. A primary
// action, when a service has one, is the solid button before it.
func TestTheHeaderKeepsDeleteBesideTheNameAndTakesAPrimary(t *testing.T) {
	out := kitRender(t, "res_header", headerData(map[string]any{
		"Primary": map[string]any{"Label": "Send a message", "Href": "/_console/sqs/orders?tab=messages"},
	}))
	title := regexp.MustCompile(`(?s)<div class="det-title".*?</div>\s*</div>`).FindString(out)
	if title == "" {
		t.Fatalf("no title row:\n%s", out)
	}
	del := strings.Index(title, `hx-post="/_console/sqs/orders/delete-queue"`)
	pri := strings.Index(title, "Send a message")
	if del < 0 {
		t.Fatalf("Delete is not in the title row, beside the name:\n%s", out)
	}
	if !strings.Contains(title, `hx-confirm="Delete it?"`) {
		t.Errorf("Delete is not confirmed:\n%s", title)
	}
	if pri < 0 || pri > del {
		t.Errorf("the primary should be the solid button before Delete (primary at %d, delete at %d):\n%s", pri, del, title)
	}
	if !regexp.MustCompile(`<a class="btn" href="[^"]*">[^<]*Send a message`).MatchString(title) {
		t.Errorf("the primary is not a solid button:\n%s", title)
	}
	if strings.Contains(out, "···") || strings.Contains(out, "class=\"menu") {
		t.Errorf("Delete is not under a ··· menu:\n%s", out)
	}
}

// The facts are a sentence or a strip, and the service says which: a service
// whose boards draw one sentence gives Sentence, and the 28 boards that draw
// the labelled boxes keep Facts. Neither given draws neither, and both given
// draws the sentence.
func TestTheHeaderDrawsTheFactsAsASentenceOrAStrip(t *testing.T) {
	sentence := []any{
		map[string]any{"N": 3, "NID": "sum-visible", "Text": "waiting", "Class": "b"},
		map[string]any{"N": 2, "NID": "sum-inflight", "Text": "in flight"},
		map[string]any{"Text": "hidden for 60 s on receive", "Class": "mut"},
		map[string]any{"Text": "dead letters to", "Class": "mut", "Link": "/_console/sqs/dlq", "LinkText": "dlq", "Tail": "after 3"},
	}
	facts := []any{map[string]any{"K": "Runtime", "V": "go1.x"}, map[string]any{"K": "Memory", "V": "128 MB", "Num": true}}

	t.Run("sentence", func(t *testing.T) {
		out := kitRender(t, "res_header", headerData(map[string]any{"Sentence": sentence}))
		if !strings.Contains(out, `class="sentence"`) {
			t.Fatalf("no sentence:\n%s", out)
		}
		if strings.Contains(out, "factstrip") {
			t.Errorf("a sentence is not a strip:\n%s", out)
		}
		// One bare separator between clauses, none inside a clause: the board
		// draws "dead letters to", the link and "after 3" as one clause.
		if n := strings.Count(out, `<span class="sep">·</span>`); n != 3 {
			t.Errorf("want 3 separators between 4 clauses, drew %d:\n%s", n, out)
		}
		for _, want := range []string{
			`<span id="sum-visible">3</span> waiting`, `<span id="sum-inflight">2</span> in flight`, "hidden for 60 s on receive",
			`<a class="s-link" href="/_console/sqs/dlq">dlq</a>`, "after 3",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the sentence lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("strip", func(t *testing.T) {
		out := kitRender(t, "res_header", headerData(map[string]any{"Facts": facts}))
		if !strings.Contains(out, `class="factstrip"`) || !strings.Contains(out, "Runtime") || !strings.Contains(out, "128 MB") {
			t.Errorf("a service that gives Facts keeps its strip:\n%s", out)
		}
		if strings.Contains(out, `class="sentence"`) {
			t.Errorf("no sentence was given:\n%s", out)
		}
	})
	t.Run("neither", func(t *testing.T) {
		out := kitRender(t, "res_header", headerData(nil))
		if strings.Contains(out, "factstrip") || strings.Contains(out, `class="sentence"`) {
			t.Errorf("nothing to say, and it says it:\n%s", out)
		}
		if !strings.Contains(out, "Delete queue") {
			t.Errorf("Delete went with the facts:\n%s", out)
		}
	})
	t.Run("both", func(t *testing.T) {
		out := kitRender(t, "res_header", headerData(map[string]any{"Sentence": sentence, "Facts": facts}))
		if !strings.Contains(out, `class="sentence"`) || strings.Contains(out, "factstrip") {
			t.Errorf("the sentence wins when a service gives both:\n%s", out)
		}
	})
}

// Board Narrow draws the sentence shorter at 1100 px and below: "0 waiting ·
// 2 in flight · hidden 60 s · dead letters after 3". The long sentence is the
// one in the markup; the words the short one drops are their own spans
// (class s-wide, which the stylesheet hides at 1100 and below), so the text
// reads whole at any width a stylesheet is not applied (and in every test and
// every screen reader that reads the long one), and a clause that is dropped
// whole takes its separator with it.
func TestTheSentenceDropsItsWideWordsAtNarrowWidths(t *testing.T) {
	out := kitRender(t, "res_header", headerData(map[string]any{"Sentence": []any{
		map[string]any{"N": 0, "NID": "w", "Text": "waiting"},
		map[string]any{"Text": "hidden{ for} 60 s{ on receive}", "Class": "s-mut"},
		map[string]any{"Text": "kept 4 days", "Wide": true},
		map[string]any{"Text": "dead letters{ to}", "Class": "s-mut", "Link": "/_console/sqs/dlq", "LinkText": "dlq", "LinkWide": true, "Tail": "after 3"},
	}}))
	for _, want := range []string{
		`hidden<span class="s-wide"> for</span> 60 s<span class="s-wide"> on receive</span>`,
		`<span class="sep s-wide">·</span><span class="s-clause s-wide">kept 4 days</span>`,
		`dead letters<span class="s-wide"> to</span></span><a class="s-link s-wide" href="/_console/sqs/dlq">dlq</a><span class="s-clause s-mut">after 3</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the sentence lacks %q:\n%s", want, out)
		}
	}
	plain := strings.Join(strings.Fields(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(out, " ")), " ")
	// The tags are spaces here, which is why this reads with spaces the page does not have.
	for _, want := range []string{"0 waiting", "hidden for 60 s on receive", "kept 4 days", "dead letters to dlq after 3"} {
		if !strings.Contains(strings.ReplaceAll(plain, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("the long sentence lost %q:\n%s", want, plain)
		}
	}
	// A clause with nothing to drop is what it always was.
	if !strings.Contains(out, `<span class="sep">·</span><span class="s-clause s-mut">hidden`) {
		t.Errorf("a clause that keeps everything changed its markup:\n%s", out)
	}
}

// The identifiers are not in the header unless a service asks for them: no
// board draws a chip row under the name.
func TestTheHeaderDrawsNoChipsUnlessAskedTo(t *testing.T) {
	out := kitRender(t, "res_header", headerData(nil))
	if strings.Contains(out, `class="chips"`) || strings.Contains(out, "copychip") {
		t.Errorf("chips with nothing asked:\n%s", out)
	}
	out = kitRender(t, "res_header", headerData(map[string]any{"Chips": []any{map[string]any{"Label": "arn", "Value": "arn:x"}}}))
	if !strings.Contains(out, "arn:x") {
		t.Errorf("a service that gives Chips keeps them:\n%s", out)
	}
}

// The neighbours strip is the board's row: "fed by [mark] name → this queue →
// read by [mark] name", every name a link with its service's mark, and when
// nothing is configured to read it, the corrected sentence (A18: the graph
// holds configuration, so it never says nothing feeds a resource).
func TestTheNeighboursStripIsTheBoardsRow(t *testing.T) {
	conn := Neighborhood{
		Upstream:   []Neighbor{{Svc: "sns", Name: "orders-topic", Kind: "sub", URL: "/sns/orders-topic"}},
		Downstream: []Neighbor{{Svc: "lambda", Name: "validator", Kind: "esm", URL: "/lambda/validator"}, {Svc: "sqs", Name: "orders-dlq", Kind: "redrive", URL: "/sqs/orders-dlq"}},
	}
	out := kitRender(t, "neighbours", map[string]any{"Prefix": "/_console", "Conn": conn, "Self": "orders", "This": "queue", "None": "no Lambda trigger reads this queue — wire one to close the loop"})
	flat := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(out, " ")
	flat = strings.Join(strings.Fields(flat), " ")
	if !strings.Contains(flat, "fed by orders-topic → this queue → read by validator") {
		t.Errorf("the row does not read as the board's:\n%s", flat)
	}
	if strings.Contains(flat, "orders-dlq") {
		t.Errorf("the dead-letter target is in the header's sentence, not read-by:\n%s", flat)
	}
	for _, svc := range []string{"sns", "lambda"} {
		if !regexp.MustCompile(`<a class="nb-link"[^>]*href="/_console/[^"]*"[^>]*><img class="aws-ic sm" src="/_console/static/aws/` + svc + `.svg"`).MatchString(out) {
			t.Errorf("the %s neighbour is not a link carrying its mark:\n%s", svc, out)
		}
	}
	if !strings.Contains(out, `class="conn"`) {
		t.Errorf("the strip lost its hook class:\n%s", out)
	}

	// Nothing reads it: the sentence, and never a claim about what feeds it.
	none := kitRender(t, "neighbours", map[string]any{"Prefix": "/_console", "Conn": Neighborhood{}, "Self": "orders", "This": "queue", "None": "no Lambda trigger reads this queue — wire one to close the loop"})
	nf := strings.Join(strings.Fields(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(none, " ")), " ")
	if !strings.Contains(nf, "this queue → no Lambda trigger reads this queue — wire one to close the loop") {
		t.Errorf("an unread queue's strip:\n%s", nf)
	}
	if strings.Contains(nf, "fed by") {
		t.Errorf("nothing is configured to feed it and the strip says so; the graph cannot know:\n%s", nf)
	}
	// (conn_strip_test.go holds the wording rule across every page; this is
	// the markup's half.)
}

// The dock's header is the board's: "The wire", what it is the wire of, a live
// pill; and a row is time, action, the resource or the reason, the status with
// the state's word whenever it is not a plain answer (D7).
func TestTheDockIsTheBoardsHeaderAndRows(t *testing.T) {
	rows := []trafficRow{
		{Time: "14:36:21.402", Action: "SendMessage", Resource: "orders", Status: 200, Millis: "1.2", State: "served"},
		{Time: "14:36:19.870", Action: "SendMessage", Resource: "orders", Status: 400, Millis: "0.9", IsErr: true, State: "refused", StateWord: "Refused",
			Refused: &Refusal{Code: "InvalidParameterValue", Message: "MessageDeduplicationId belongs to FIFO queues"}},
		{Time: "14:36:18.001", Action: "ReceiveMessage", Resource: "orders", Status: 403, Millis: "0.3", IsErr: true, State: "denied", StateWord: "Denied"},
		{Time: "14:36:17.500", Action: "StartMessageMoveTask", Resource: "orders", Status: 501, Millis: "0.2", IsErr: true, State: "notbuilt", StateWord: "Not built"},
		{Time: "14:36:16.100", Action: "PurgeQueue", Resource: "orders", Status: 500, Millis: "2.0", IsErr: true, State: "error", StateWord: "Failed"},
	}
	out := kitRender(t, "res_wire_rows", map[string]any{
		"Prefix": "/_console", "Svc": "sqs", "Resource": "orders", "Noun": "queue", "Hash": "7",
		"Rows": rows, "Enabled": true, "Total": 5,
		"Tally": []wireTally{{N: 1, Word: "refused"}},
	})
	head := regexp.MustCompile(`(?s)<div class="rw-h".*?</div>`).FindString(out)
	flat := strings.Join(strings.Fields(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(head, " ")), " ")
	if !strings.HasPrefix(flat, "The wire calls that touched this queue") {
		t.Errorf("the header should read \"The wire\", then what it is the wire of:\n%s", flat)
	}
	if !strings.Contains(head, `<span class="pill ok"><span class="live"></span>live</span>`) {
		t.Errorf("no live pill:\n%s", head)
	}
	if !strings.Contains(flat, "5 calls touched this queue · 1 refused") {
		t.Errorf("the collapsed form's sentence is missing:\n%s", flat)
	}
	if !strings.Contains(head, "data-wire-toggle") {
		t.Errorf("no show button for the collapsed form:\n%s", head)
	}
	for _, w := range []string{"Refused", "Denied", "Not built", "Failed"} {
		if !regexp.MustCompile(`<span class="tr-state">` + w + `</span>`).MatchString(out) {
			t.Errorf("the state %q is not said in a word:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "MessageDeduplicationId belongs to FIFO queues") {
		t.Errorf("a refusal's reason is not in its row:\n%s", out)
	}
	if n := strings.Count(out, `class="rw-row`); n != 5 {
		t.Errorf("want 5 rows, drew %d", n)
	}
	if strings.Contains(out, "On the wire") || strings.Contains(out, "newest first") {
		t.Errorf("the old caption is still there:\n%s", out)
	}
}
