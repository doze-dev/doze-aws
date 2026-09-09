package console

// A ratchet on the ratchet.
//
// TestSDKCoverage establishes "the console can reach this operation" by
// looking for the operation's name as a quoted string literal anywhere in the
// console's non-test Go source. For the JSON and Query services that is exact
// — the literal IS the call:
//
//	b.json11(ctx, "AWSEvents", "CreateApiDestination", ...)
//
// For the REST services it is not a call at all. The console reaches S3,
// Lambda and both API Gateways by building paths — PUT /bucket?policy,
// POST /v2/apis/{id}/routes — and never spells the operation. Those names
// appear in exactly one place: traffic.go, the lookup table that labels rows
// in the request log for a human to read.
//
// So 162 F-tier operations count as "reachable" on the strength of a display
// string. Delete the whole public-access feature — handler, client and the
// toggle in s3.html — and TestSDKCoverage still passes at 521/521, because
// traffic.go still contains "PutPublicAccessBlock".
//
// Excluding traffic.go outright would be worse: it would report all 162 as
// unreachable when most of them do have surfaces, and the burn-down list
// would become fiction in the other direction. The blind spot is real and it
// is not cheaply closable, so the thing to do is measure it and stop it
// growing quietly. That is this file.
//
// What actually protects those features is elsewhere and should stay there:
// the render guards, the per-feature console tests, and the Playwright specs.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// trafficOnlyFloor is how many F-tier operations have no evidence of console
// reachability except traffic.go naming them.
//
// It is a floor in both directions, like inlineBudget. Going UP means a new
// operation was declared reachable on the strength of a log label, which is
// the failure this file exists to catch. Going DOWN means someone gave one
// real evidence, which is good and should shorten the number.
const trafficOnlyFloor = 163

// trafficOnlyByService is the same number, attributable. Every one of these
// is a REST service; that is the whole pattern, and a JSON service appearing
// here would mean something quite different — an operation the console names
// only in the log and never calls.
var trafficOnlyByService = map[string]int{
	"apigateway":   53,
	"s3":           48,
	"apigatewayv2": 35,
	"lambda":       27,
}

var opLiteral = regexp.MustCompile(`"([A-Z][A-Za-z0-9]*)"`)

// literalsIn is consoleCalls, over a chosen set of files.
func literalsIn(t *testing.T, skip func(string) bool) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || skip(f) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range opLiteral.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}

// TestTrafficTableIsNotSilentlyLoadBearing pins the blind spot.
func TestTrafficTableIsNotSilentlyLoadBearing(t *testing.T) {
	all := literalsIn(t, func(string) bool { return false })
	real := literalsIn(t, func(f string) bool { return f == "traffic.go" })

	docs, err := filepath.Glob("../docs/api-support/*.md")
	if err != nil || len(docs) == 0 {
		t.Skipf("no api-support ledger next to the console (%v)", err)
	}

	total := 0
	got := map[string]int{}
	for _, doc := range docs {
		svc := strings.TrimSuffix(filepath.Base(doc), ".md")
		n := 0
		for _, op := range fTierOps(t, doc) {
			if all[op] && !real[op] {
				n++
				total++
			}
		}
		if n > 0 {
			got[svc] = n
		}
	}

	if total != trafficOnlyFloor {
		var lines []string
		for svc, n := range got {
			lines = append(lines, svc+"="+strconv.Itoa(n))
		}
		sort.Strings(lines)
		t.Errorf("operations reachable only via traffic.go = %d, floor %d (%s).\n"+
			"Up means an operation was declared reachable on the strength of a log label.\n"+
			"Down means one was given real evidence — shorten the floor.",
			total, trafficOnlyFloor, strings.Join(lines, " "))
	}

	for svc, want := range trafficOnlyByService {
		if got[svc] != want {
			t.Errorf("%s: %d operations rest on traffic.go alone, floor %d", svc, got[svc], want)
		}
	}
	for svc, n := range got {
		if _, listed := trafficOnlyByService[svc]; !listed {
			t.Errorf("%s has %d operations resting on traffic.go alone and is not a listed REST service — "+
				"a JSON service here means the console names the operation in the log and never calls it", svc, n)
		}
	}
}

// TestAPIGWV2ActionNames tests the table the ratchet leans on.
//
// apigwV2Action is ~50 lines mapping method and path onto an operation name,
// it is the sole evidence of console reachability for 35 apigatewayv2
// operations, and it was 0% covered. A typo in it is invisible: the request
// log shows the wrong name, and the ratchet counts the operation as reachable
// anyway because the literal is still there.
func TestAPIGWV2ActionNames(t *testing.T) {
	for _, c := range []struct {
		method string
		path   string // after the /v2/ prefix
		want   string
	}{
		{"GET", "apis", "GetApis"},
		{"POST", "apis", "CreateApi"},
		{"GET", "apis/a1", "GetApi"},
		{"PATCH", "apis/a1", "UpdateApi"},
		{"DELETE", "apis/a1", "DeleteApi"},
		{"DELETE", "apis/a1/cors", "DeleteCorsConfiguration"},

		{"GET", "apis/a1/routes", "GetRoutes"},
		{"POST", "apis/a1/routes", "CreateRoute"},
		{"GET", "apis/a1/routes/r1", "GetRoute"},
		{"PATCH", "apis/a1/routes/r1", "UpdateRoute"},
		{"DELETE", "apis/a1/routes/r1", "DeleteRoute"},

		{"GET", "apis/a1/integrations", "GetIntegrations"},
		{"POST", "apis/a1/integrations", "CreateIntegration"},
		{"GET", "apis/a1/integrations/i1", "GetIntegration"},
		{"PATCH", "apis/a1/integrations/i1", "UpdateIntegration"},
		{"DELETE", "apis/a1/integrations/i1", "DeleteIntegration"},

		{"GET", "apis/a1/authorizers", "GetAuthorizers"},
		{"POST", "apis/a1/authorizers", "CreateAuthorizer"},
		{"GET", "apis/a1/authorizers/z1", "GetAuthorizer"},
		{"PATCH", "apis/a1/authorizers/z1", "UpdateAuthorizer"},
		{"DELETE", "apis/a1/authorizers/z1", "DeleteAuthorizer"},

		{"GET", "apis/a1/deployments", "GetDeployments"},
		{"POST", "apis/a1/deployments", "CreateDeployment"},
		{"GET", "apis/a1/deployments/d1", "GetDeployment"},
		{"PATCH", "apis/a1/deployments/d1", "UpdateDeployment"},
		{"DELETE", "apis/a1/deployments/d1", "DeleteDeployment"},

		{"GET", "apis/a1/stages", "GetStages"},
		{"POST", "apis/a1/stages", "CreateStage"},
		{"GET", "apis/a1/stages/prod", "GetStage"},
		{"PATCH", "apis/a1/stages/prod", "UpdateStage"},
		{"DELETE", "apis/a1/stages/prod", "DeleteStage"},
		{"DELETE", "apis/a1/stages/prod/accesslogsettings", "DeleteAccessLogSettings"},
		{"DELETE", "apis/a1/stages/prod/routesettings/GET%20%2Fitems", "DeleteRouteSettings"},
		{"DELETE", "apis/a1/stages/prod/cache/authorizers", "ResetAuthorizersCache"},

		{"GET", "tags/arn", "GetTags"},
		{"POST", "tags/arn", "TagResource"},
		{"DELETE", "tags/arn", "UntagResource"},
	} {
		segs := strings.Split(strings.Trim(c.path, "/"), "/")
		if got := apigwV2Action(c.method, segs); got != c.want {
			t.Errorf("%s /v2/%s = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// TestAPIGWV2ActionNamesTheRefusedFamilies: the families answered 501 still
// get a readable label rather than a blank cell in the request log.
func TestAPIGWV2ActionNamesTheRefusedFamilies(t *testing.T) {
	for _, c := range []struct {
		method, path, want string
	}{
		{"GET", "domainnames", "GET domain name"},
		{"POST", "vpclinks", "POST VPC link"},
		{"GET", "apis/a1/models", "GET models"},
		{"GET", "apis/a1/exports", "GET exports"},
		{"GET", "apis/a1/routingrules", "GET routingrules"},
	} {
		segs := strings.Split(strings.Trim(c.path, "/"), "/")
		if got := apigwV2Action(c.method, segs); got != c.want {
			t.Errorf("%s /v2/%s = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// TestAPIGWV2ActionFallsBackReadably: an unmapped shape must still produce
// something, not an empty label.
func TestAPIGWV2ActionFallsBackReadably(t *testing.T) {
	for _, path := range []string{"somethingelse", "apis/a1/unknownsub"} {
		segs := strings.Split(path, "/")
		if got := apigwV2Action("GET", segs); got == "" {
			t.Errorf("/v2/%s produced an empty label", path)
		}
	}
}
