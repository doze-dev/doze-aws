package dozetest

// The model-derived case fixtures every rejection-parity suite replays.
//
// `dzaudit cases <service>` derives, from AWS's own service model, every
// constraint a request can violate — a length, a pattern, an enum, a required
// member — and writes one case per violation. A suite replays each against a
// baseline it has first proved the service accepts, because a request refused
// for the WRONG reason looks exactly like a pass.
//
// Seventeen suites loaded those files with the same twenty lines, differing
// only in the filename. They share this instead. What the suites do with the
// cases afterwards is genuinely their own: they count skipped-for-state,
// skipped-for-a-peer, un-auditable, not-expressible-on-this-wire and
// out-of-scope differently, because those mean different things per service.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/doze-dev/doze-aws/internal/auditkit"
)

// Case is one model-derived mutation of a baseline request.
//
// Target is the X-Amz-Target action for the JSON protocols and empty for the
// query and REST ones, so a suite that does not need it simply ignores it.
type Case struct {
	Operation   string           `json:"operation"`
	Target      string           `json:"target"`
	Path        string           `json:"path"`
	Why         string           `json:"why"`
	Value       any              `json:"value"`
	ValueRepeat *auditkit.Repeat `json:"value_repeat,omitempty"`
	Constraint  string           `json:"constraint"`
}

// LoadCases reads testdata/<file> and materialises every padded value.
func LoadCases(t testing.TB, file string) []Case {
	t.Helper()
	cs := decodeCases[Case](t, file)
	for i := range cs {
		cs[i].Value = auditkit.Materialize(cs[i].Value, cs[i].ValueRepeat)
	}
	return cs
}

// LoadCasesInto is LoadCases for the four suites that extend Case with a field
// of their own — an HTTP binding (apigateway, lambda, s3) or the required
// members a shape declares (dynamodb). base says where the embedded Case is.
func LoadCasesInto[T any](t testing.TB, file string, base func(*T) *Case) []T {
	t.Helper()
	cs := decodeCases[T](t, file)
	for i := range cs {
		c := base(&cs[i])
		c.Value = auditkit.Materialize(c.Value, c.ValueRepeat)
	}
	return cs
}

// decodeCases reads the fixture, refusing an empty one.
//
// A max-length case stores the SHAPE of its padding rather than the run
// itself: written out, those runs were 35 MB of the 37.5 MB of fixtures.
func decodeCases[T any](t testing.TB, file string) []T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var cs []T
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatalf("%s holds no cases: the audit would pass vacuously", file)
	}
	return cs
}
