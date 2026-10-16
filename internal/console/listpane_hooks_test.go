package console

// The list pane's hooks that no service page turns on yet: the Create board's
// ghost row, a count beside the heading for a service whose board draws one,
// and "New" dimmed on a create page. They are markup the shell offers, so they
// are tested as markup, by executing lp_head with the parameters a page would
// pass (plan/03-shell-fidelity.md 2.3).

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
)

func lpHeadHTML(t *testing.T, kv ...any) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(templateFuncs("/_console", awsident.Default())).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"Svc": "sqs", "Prefix": "/_console", "NewURL": "/_console/sqs/create", "Count": 4}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i].(string)] = kv[i+1]
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "lp_head", args); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTheListHeadHasNoCountUnlessAServiceAsksForOne(t *testing.T) {
	plain := lpHeadHTML(t)
	if strings.Contains(plain, "lp-ct") || !strings.Contains(plain, `<span class="lp-name">Queues</span>`) {
		t.Errorf("the heading is not the plural alone:\n%s", plain)
	}
	counted := lpHeadHTML(t, "ShowCount", true)
	if !strings.Contains(counted, `<span class="lp-ct" id="lph-sqs">4</span>`) {
		t.Errorf("a service that asks for the count does not get its slot:\n%s", counted)
	}
}

func TestTheGhostRowIsOnlyThereWhenAPageAsksForIt(t *testing.T) {
	if strings.Contains(lpHeadHTML(t), "lp-ghost") {
		t.Errorf("a ghost row nobody asked for")
	}
	ghost := lpHeadHTML(t, "Ghost", true)
	for _, want := range []string{`class="li on lp-ghost"`, `x-show="$store.ghost.name"`, `x-text="$store.ghost.name"`, `>new</span>`} {
		if !strings.Contains(ghost, want) {
			t.Errorf("the ghost row lacks %s:\n%s", want, ghost)
		}
	}
}

func TestNewIsDimmedOnTheCreatePage(t *testing.T) {
	if got := lpHeadHTML(t); strings.Contains(got, "is-current") {
		t.Errorf("New is dimmed on a list page:\n%s", got)
	}
	if got := lpHeadHTML(t, "Creating", true); !strings.Contains(got, `class="new-link is-current"`) || !strings.Contains(got, `aria-current="page"`) {
		t.Errorf("New is not dimmed on the create page:\n%s", got)
	}
}

// Every service the catalogue knows has the words its list pane and its key bar
// are written in; a missing plural would head a pane with its key.
func TestEveryCatalogueEntryHasItsPluralAndItsItem(t *testing.T) {
	for _, e := range catalog {
		if e.Plural == "" {
			t.Errorf("%s has no Plural", e.Key)
		}
		if itemFor(e.Key) == "item" {
			t.Errorf("%s has no Noun or Item for the key bar", e.Key)
		}
		if !strings.HasPrefix(listKeys(e.Key), "j k="+itemFor(e.Key)+"|") {
			t.Errorf("%s: %q", e.Key, listKeys(e.Key))
		}
	}
}

// At 1100 px and below the pane is a switcher (board Narrow, decision D5): its
// head carries the button and the count it is drawn from, and the rows and the
// filter sit in one menu wrapper that the column form lays out as if it were not
// there. The words are the service's own, from the catalogue.
func TestTheListHeadCarriesTheSwitcher(t *testing.T) {
	got := lpHeadHTML(t)
	for _, want := range []string{
		`class="lp-switch"`, `aria-haspopup="true"`, `aria-expanded="false"`,
		`data-item="queue"`, `data-all="Queues"`, `<span class="lp-sw-of"></span>`, `<div class="lp-menu">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the list head lacks %s:\n%s", want, got)
		}
	}
	// The foot closes the scroll area and the menu wrapper both.
	tmpl, err := template.New("").Funcs(templateFuncs("/_console", awsident.Default())).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "lp_foot", map[string]any{"Names": "[]"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(b.String(), "</div>") != strings.Count(b.String(), "<div")+2 {
		t.Errorf("the foot does not close the scroll area and the menu:\n%s", b.String())
	}
}
