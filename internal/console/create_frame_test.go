package console_test

// The create page's frame and the palette's typed create entry (boards Palette,
// Create, Sqs-03-Create, Sqs-04-Create-FIFO; plan/03-shell-fidelity.md 2.11,
// S9 and S10, decision D4). The browser specs drive the palette and the keys;
// these pin what the server writes: a name typed in the palette arrives in the
// create form, every create page that has a call to show shows it in the
// panel, and the palette's data says what each create makes.

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Every create page, by the path the catalogue gives it (and the one the
// catalogue cannot: a rule is made on a bus), with the name of the input that
// holds what the page calls the thing. A key's only name is its alias.
var createPages = []struct{ svc, path, field string }{
	{"s3", "/s3/create", "name"},
	{"ddb", "/ddb/create", "name"},
	{"sqs", "/sqs/create", "name"},
	{"sns", "/sns/create", "name"},
	{"eb", "/eb/create-bus", "name"},
	{"eb", "/eb/default/create-rule", "name"},
	{"kinesis", "/kinesis/create", "name"},
	{"lambda", "/lambda/create", "name"},
	{"apigw", "/apigw/create", "name"},
	{"sfn", "/sfn/create", "name"},
	{"logs", "/logs/create", "name"},
	{"cw", "/cw/create", "name"},
	{"kms", "/kms/create", "alias"},
	{"sm", "/sm/create", "name"},
	{"ssm", "/ssm/create", "name"},
	{"iam", "/iam/create", "name"},
	{"cfn", "/cfn/create", "name"},
}

// inputNamed is the first input tag of a page whose name attribute is field.
func inputNamed(page, field string) string {
	re := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(field) + `"[^>]*>`)
	return re.FindString(page)
}

// The palette offers "New queue called harbour-x" and follows it to the create
// page with ?name=harbour-x. The page has to take the name: the field holds it
// before any script runs, and a name with markup in it is escaped.
func TestEveryCreatePageTakesTheNameTheURLCarries(t *testing.T) {
	h, gw := newConsoleStack(t)
	// An alarm is made on a metric, and with none the page says so and has no
	// form (board Cw-03-Create-None): give it one to put the alarm on.
	putMetric(t, gw, `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts","Value":7,"Unit":"Count"}]}`)
	for _, p := range createPages {
		page := req(t, h, "GET", "/_console"+p.path+"?name=harbour-typed", nil).Body.String()
		in := inputNamed(page, p.field)
		if in == "" {
			t.Errorf("%s: no input named %q", p.path, p.field)
			continue
		}
		if !strings.Contains(in, `value="harbour-typed"`) {
			t.Errorf("%s: ?name=harbour-typed did not reach the %s field: %s", p.path, p.field, in)
		}
		// And without it the field is as it always was: empty.
		if plain := inputNamed(req(t, h, "GET", "/_console"+p.path, nil).Body.String(), p.field); strings.Contains(plain, `value="harbour-typed"`) {
			t.Errorf("%s: the name is remembered across requests", p.path)
		}
	}
}

func TestATypedNameIsEscapedInTheCreateForm(t *testing.T) {
	h, gw := newConsoleStack(t)
	putMetric(t, gw, `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts","Value":7,"Unit":"Count"}]}`)
	const evil = `x"><script>1</script>`
	for _, p := range createPages {
		page := req(t, h, "GET", "/_console"+p.path+"?name="+url.QueryEscape(evil), nil).Body.String()
		if strings.Contains(page, evil) {
			t.Errorf("%s: the typed name reached the page unescaped", p.path)
		}
	}
}

// What the palette needs to offer a typed create: the noun the create makes
// ("queue"), and the query parameter that carries the name. They come from the
// catalogue, so a service added to it is offered without a second list.
func TestThePaletteSaysWhatEachCreateMakes(t *testing.T) {
	h, gw := newConsoleStack(t)
	putMetric(t, gw, `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts","Value":7,"Unit":"Count"}]}`)
	var out struct {
		Acts []struct {
			S, N, U, K, W, P string
		} `json:"acts"`
	}
	rec := req(t, h, "GET", "/_console/api/palette", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Acts) != 16 {
		t.Errorf("the palette offers %d creates, the catalogue has 16", len(out.Acts))
	}
	want := map[string]string{
		"s3": "bucket", "ddb": "table", "sqs": "queue", "sns": "topic", "eb": "event bus", "kinesis": "stream",
		"lambda": "function", "apigw": "API", "sfn": "state machine", "logs": "log group", "cw": "alarm",
		"kms": "key", "sm": "secret", "ssm": "parameter", "iam": "principal", "cfn": "stack",
	}
	for _, a := range out.Acts {
		if a.W != want[a.S] {
			t.Errorf("%s: the create makes %q, want %q", a.S, a.W, want[a.S])
		}
		if a.P != "name" {
			t.Errorf("%s: the name travels in %q, want name", a.S, a.P)
		}
		// The URL the palette follows is the create page with the name added.
		page := req(t, h, "GET", a.U+"?"+a.P+"=harbour-typed", nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `value="harbour-typed"`) {
			t.Errorf("%s: %s?%s= is not a create page that takes the name", a.S, a.U, a.P)
		}
	}
}

// Sqs-03 draws the keys tab, esc, ⌘K. The Create board draws ⌘⏎ create, tab,
// esc; ⌘K stays at the end, the one key every page answers to.
const (
	sqsCreateKeys   = "tab=next field|esc=cancel|⌘K=jump or act"
	otherCreateKeys = "⌘⏎=create|tab=next field|esc=cancel, nothing made|⌘K=jump or act"
)

func TestCreatePagesDeclareTheKeysTheirBoardsDraw(t *testing.T) {
	h := newConsole(t)
	for _, p := range createPages {
		want := otherCreateKeys
		if p.svc == "sqs" {
			want = sqsCreateKeys
		}
		got := declaredKeys(req(t, h, "GET", "/_console"+p.path, nil).Body.String())
		if len(got) == 0 || got[len(got)-1] != want {
			t.Errorf("%s declares %q, want last %q", p.path, got, want)
		}
	}
}

// The SQS create form had no preview; D4 gives every create page the call it
// makes. It sits in the right-hand column with the life of one message, inside
// the form (the preview reads the form it is in).
func TestTheSQSCreateFormShowsTheCallItMakes(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sqs/create", nil).Body.String()
	i := strings.Index(page, `class="cli cli-panel"`)
	if i < 0 {
		t.Fatal("the SQS create page has no cli preview")
	}
	form, side := strings.Index(page, "<form"), strings.Index(page, "q-create-side")
	if form < 0 || side < 0 || i < side || i < form {
		t.Errorf("the preview is not in the right-hand column of the form (form %d, column %d, preview %d)", form, side, i)
	}
	if !strings.Contains(html.UnescapeString(page), "sqs create-queue --queue-name") {
		t.Errorf("the preview is not the create-queue call")
	}
	if n := strings.Count(page, `class="cli cli-panel"`); n != 1 {
		t.Errorf("%d previews, want 1", n)
	}
}

// Every create page whose form has a preview says so in the panel's words, and
// the panel opens by default: the page is the place the call is made.
func TestTheCallPanelOpensOnACreatePage(t *testing.T) {
	h, gw := newConsoleStack(t)
	putMetric(t, gw, `{"Namespace":"Shop","MetricData":[{"MetricName":"Checkouts","Value":7,"Unit":"Count"}]}`)
	n := 0
	for _, p := range createPages {
		page := req(t, h, "GET", "/_console"+p.path, nil).Body.String()
		if !strings.Contains(page, `class="cli cli-panel"`) {
			continue
		}
		n++
		for _, want := range []string{"The call this makes", "Copy the command", "Made here, kept here"} {
			if p.svc == "sqs" && want == "Made here, kept here" {
				continue // Sqs-03 to Sqs-05 draw none: TestTheSQSCreatePageHasNoMadeHereKeptHereCard
			}
			if !strings.Contains(page, want) {
				t.Errorf("%s: the panel lacks %q", p.path, want)
			}
		}
	}
	if n < 12 {
		t.Errorf("only %d create pages show the call they make", n)
	}
}

// The Palette board draws a log group among the things "Go to" finds; the
// resource index the palette reads had none of the service's, so nothing there
// could be jumped to. Every row is a page that exists.
func TestThePaletteFindsLogGroups(t *testing.T) {
	h := newConsole(t)
	create(t, h, "/_console/logs/create", url.Values{"name": {"/harbour/checkout"}})
	var rows []struct{ S, N, U string }
	if err := json.Unmarshal(req(t, h, "GET", "/_console/api/resources", nil).Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.S == "logs" && r.N == "/harbour/checkout" {
			if page := req(t, h, "GET", r.U, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "/harbour/checkout") {
				t.Errorf("%s is not the log group's page (%d)", r.U, page.Code)
			}
			return
		}
	}
	t.Errorf("the log group is not in the palette's resources: %v", rows)
}

// The palette's markup (board Palette): a count beside the input, a footer
// with the sentence about an ARN and the legend of keys, and the label the
// board's input carries. Nothing in it is styled inline.
func TestThePaletteIsTheBoardsDialog(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	i := strings.Index(page, `id="palette"`)
	if i < 0 {
		t.Fatal("no palette")
	}
	j := strings.Index(page[i:], `id="confirm"`)
	pal := page[i : i+j]
	for _, want := range []string{
		`id="pal-count"`,
		`Paste an ARN or a queue URL and it goes straight to the resource.`,
		`<span class="k">↑</span>`, `<span class="k">↓</span>`, `<span class="k sep">⏎</span>`, `<span class="k sep">esc</span>`,
		`>move `, `>go `, `>close`,
		`Jump or act`,
	} {
		if !strings.Contains(pal, want) {
			t.Errorf("the palette lacks %s", want)
		}
	}
	if strings.Contains(pal, "style=") {
		t.Errorf("the palette is styled inline")
	}
}
