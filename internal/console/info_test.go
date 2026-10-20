package console_test

import (
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/docs"
	"github.com/doze-dev/doze-aws/internal/console"
)

// Every service in the rail has a ledger behind its fidelity button. The panel
// said "No ledger for this service" for CloudWatch, because the console's key
// (cw) is not the ledger's (cloudwatch), and showed an HTTP API the REST
// operations. A key that maps to nothing is the failure this test is for.
func TestEveryRailServiceHasALedger(t *testing.T) {
	h := newConsole(t)
	for _, e := range console.RegisteredServices() {
		t.Run(e.Key, func(t *testing.T) {
			rec := req(t, h, "GET", "/_console/info/"+e.Key, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, "No ledger for this service") {
				t.Fatalf("the fidelity panel for %q has no ledger behind it", e.Key)
			}
			if !strings.Contains(body, "functional") {
				t.Errorf("no functional operations listed:\n%s", body)
			}
		})
	}
}

// The ledger's own name for the section a console key covers, where it is not
// the key (info.go's infoSvc says the same; this is written out so that a
// change to one fails here).
var ledgerOf = map[string][]string{
	"ddb": {"dynamodb"}, "eb": {"eventbridge"}, "sm": {"secretsmanager"},
	"cfn": {"cloudformation"}, "sfn": {"stepfunctions"}, "cw": {"cloudwatch"},
	"apigw": {"apigateway", "apigatewayv2"},
}

// "Differences from AWS" (boards Fidelity, Kms-19): under every service's
// table, the ledger's own bullets, whole, in the order the ledger has them.
// A service whose panel shows none has a ledger the dialog cannot read.
func TestEveryServicesPanelCarriesItsDifferencesFromTheLedger(t *testing.T) {
	h := newConsole(t)
	for _, e := range console.RegisteredServices() {
		t.Run(e.Key, func(t *testing.T) {
			body := req(t, h, "GET", "/_console/info/"+e.Key, nil).Body.String()
			if !strings.Contains(body, "Differences from AWS") {
				t.Fatalf("no \"Differences from AWS\" in the panel:\n%s", body)
			}
			keys := ledgerOf[e.Key]
			if keys == nil {
				keys = []string{e.Key}
			}
			// Each bullet is found after the one before it, across the ledgers a
			// panel carries (API Gateway's two share a bullet or two).
			pos := 0
			for _, k := range keys {
				ds := docs.Differences(k)
				if len(ds) == 0 {
					t.Fatalf("the ledger %q has no differences to show", k)
				}
				for _, d := range ds {
					want := strings.TrimSpace(d.Lead)
					if want == "" {
						want = strings.TrimSpace(d.Text)
					}
					at := strings.Index(body[pos:], html.EscapeString(want))
					if at < 0 {
						t.Errorf("%s: the difference %q is not in the panel, or is out of the ledger's order", k, want)
						continue
					}
					pos += at + 1
				}
			}
		})
	}
}

// The panel is lazy: the ledger is read when a dialog is opened, and what the
// page carries until then is the button and the frame, not a table.
func TestAServicePageCarriesNoLedgerUntilTheDialogIsOpened(t *testing.T) {
	h := newConsole(t)
	page := req(t, h, "GET", "/_console/sqs", nil).Body.String()
	for _, nope := range []string{"Differences from AWS", "CreateQueue", "ReceiveMessage"} {
		if strings.Contains(page, nope) {
			t.Errorf("the SQS list page already holds %q: the ledger is not lazy", nope)
		}
	}
}

// The dialog is titled for the service it describes, in the name the rail
// prints (board Fidelity: "SQS · what is real here"), on every service's page.
func TestEveryServicesDialogIsTitledForIt(t *testing.T) {
	h := newConsole(t)
	title := regexp.MustCompile(`<h3 class="fid-title">([^<]*)</h3>`)
	for _, e := range console.RegisteredServices() {
		t.Run(e.Key, func(t *testing.T) {
			rec := req(t, h, "GET", "/_console"+e.Path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			m := title.FindStringSubmatch(rec.Body.String())
			if m == nil {
				t.Fatalf("no dialog title on %s", e.Path)
			}
			if want := html.EscapeString(e.Label) + " · what is real here"; m[1] != want {
				t.Errorf("title = %q, want %q", m[1], want)
			}
		})
	}
}

func TestTheAPIGatewayPanelCoversBothKindsOfAPI(t *testing.T) {
	h := newConsole(t)
	body := req(t, h, "GET", "/_console/info/apigw", nil).Body.String()
	for _, want := range []string{"REST APIs", "HTTP APIs"} {
		if !strings.Contains(body, want) {
			t.Errorf("the panel does not carry a %q section", want)
		}
	}
}
