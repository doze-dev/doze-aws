package provision_test

import (
	"context"
	"slices"
	"testing"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/provision"
)

// namedStack holds one resource of each kind whose name AWS keeps unique and
// that a test can make without code or a role.
func namedStack() *provision.Stack {
	return &provision.Stack{
		Queues:     map[string]provision.Queue{"ex-queue": {}},
		Topics:     map[string]provision.Topic{"ex-topic": {}},
		Buckets:    map[string]provision.Bucket{"ex-bucket": {}},
		Tables:     map[string]provision.Table{"ex-table": {Key: "pk:S"}},
		Secrets:    map[string]provision.Secret{"ex-secret": {Value: "v"}},
		Parameters: map[string]provision.Parameter{"/ex/param": {Value: "v"}},
		LogGroups:  map[string]provision.LogGroup{"/ex/group": {}},
		Rules:      map[string]provision.Rule{"ex-rule": {Schedule: "rate(1 hour)"}},
		Keys:       map[string]provision.Key{"ex-key": {}},
		Dashboards: map[string]provision.Dashboard{"ex-dash": {Body: `{"widgets":[]}`}},
		Alarms: map[string]provision.Alarm{"ex-alarm": {
			Namespace: "App", MetricName: "Errors", Statistic: "Sum", Period: 60,
			EvaluationPeriods: 1, ComparisonOperator: "GreaterThanThreshold", Threshold: 1,
		}},
	}
}

// Existing sees nothing before the stack is applied and every resource after,
// and a lookalike name — a prefix of a real one — is not mistaken for it.
func TestExistingReportsWhatIsThere(t *testing.T) {
	ctx := context.Background()
	h, _, _ := liveStack(t)
	sf, id := namedStack(), awsident.Default()

	if got, err := provision.Existing(ctx, h, id, sf); err != nil || len(got) != 0 {
		t.Fatalf("before apply: Existing = %v, %v; want nothing", got, err)
	}
	if _, err := provision.Apply(ctx, h, sf, id); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := provision.Existing(ctx, h, id, sf)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for n := range provision.Names(sf) {
		want = append(want, n)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("after apply:\n got %v\nwant %v", got, want)
	}

	prefixes := &provision.Stack{
		LogGroups: map[string]provision.LogGroup{"/ex/gro": {}},
		Keys:      map[string]provision.Key{"ex-ke": {}},
		Alarms:    map[string]provision.Alarm{"ex-ala": {}},
	}
	if got, err := provision.Existing(ctx, h, id, prefixes); err != nil || len(got) != 0 {
		t.Errorf("prefixes of real names: Existing = %v, %v; want nothing", got, err)
	}
}
