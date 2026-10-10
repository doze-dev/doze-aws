package console_test

import (
	"net/http"
	"strings"
	"testing"

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

func TestTheAPIGatewayPanelCoversBothKindsOfAPI(t *testing.T) {
	h := newConsole(t)
	body := req(t, h, "GET", "/_console/info/apigw", nil).Body.String()
	for _, want := range []string{"REST APIs", "HTTP APIs"} {
		if !strings.Contains(body, want) {
			t.Errorf("the panel does not carry a %q section", want)
		}
	}
}
