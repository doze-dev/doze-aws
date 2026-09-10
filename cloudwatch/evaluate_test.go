package cloudwatch

import (
	"testing"
	"time"
)

func TestBreaches(t *testing.T) {
	cases := []struct {
		op    string
		value float64
		want  bool
	}{
		{"GreaterThanThreshold", 11, true},
		{"GreaterThanThreshold", 10, false},
		{"GreaterThanOrEqualToThreshold", 10, true},
		{"LessThanThreshold", 9, true},
		{"LessThanThreshold", 10, false},
		{"LessThanOrEqualToThreshold", 10, true},
		// An anomaly operator is refused at PutMetricAlarm; not breaching is
		// the safe reading if one somehow reaches here.
		{"GreaterThanUpperThreshold", 1e9, false},
	}
	for _, c := range cases {
		if got := breaches(c.op, c.value, 10); got != c.want {
			t.Errorf("breaches(%s, %v, 10) = %v, want %v", c.op, c.value, got, c.want)
		}
	}
}

// The state machine, driven directly. Each case publishes into a fixed
// window, so the periods examined are exactly the ones described.
func TestEvaluateStateMachine(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// The window is the EvaluationPeriods complete periods ending at now:
	// end = alignDown(now), start = end - N×period. With now exactly on a
	// boundary N periods after base, that is minutes 0, 1 and 2 — the ones
	// the values below are published into. Move now a minute later and the
	// window slides off the data, which is how the first draft of this test
	// managed to fail against correct code.
	now := base.Add(3 * time.Minute)

	cases := []struct {
		name    string
		values  []float64 // one per minute from base; NaN-free, nil means no data
		gaps    []int     // minute offsets deliberately left empty
		treat   string
		toAlarm int
		want    string
	}{
		{
			name: "every period breaches", values: []float64{20, 20, 20},
			toAlarm: 3, want: stateAlarm,
		},
		{
			name: "no period breaches", values: []float64{1, 1, 1},
			toAlarm: 3, want: stateOK,
		},
		{
			// M of N: two breaches is enough when DatapointsToAlarm is 2.
			name: "two of three breaches with M=2", values: []float64{20, 1, 20},
			toAlarm: 2, want: stateAlarm,
		},
		{
			name: "two of three breaches with M=3", values: []float64{20, 1, 20},
			toAlarm: 3, want: stateOK,
		},
		{
			// Nothing observed at all is what INSUFFICIENT_DATA is for. It is
			// not a synonym for OK, and treating it as one would hide an
			// outage as health.
			name: "no data at all", values: nil, gaps: []int{0, 1, 2},
			toAlarm: 3, want: stateInsufficientData,
		},
		{
			name: "missing periods treated as breaching", values: nil,
			gaps: []int{0, 1, 2}, treat: "breaching", toAlarm: 3, want: stateAlarm,
		},
		{
			name: "missing periods treated as not breaching", values: nil,
			gaps: []int{0, 1, 2}, treat: "notBreaching", toAlarm: 3, want: stateOK,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(Options{DataDir: t.TempDir(), Clock: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			var data []datum
			for i, v := range c.values {
				data = append(data, datum{
					Namespace: "T", MetricName: "M", Value: v, Resolution: 60,
					Timestamp: base.Add(time.Duration(i) * time.Minute),
				})
			}
			if len(data) > 0 {
				if err := s.putSamples(data); err != nil {
					t.Fatal(err)
				}
			}

			a := &alarm{
				Name: "a", Namespace: "T", MetricName: "M", Statistic: "Sum",
				Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: c.toAlarm,
				ComparisonOp: "GreaterThanThreshold", Threshold: 10,
				TreatMissingData: c.treat, State: "",
			}
			got, reason, _ := s.evaluate(a, now)
			if got != c.want {
				t.Errorf("state = %q, want %q (reason: %s)", got, c.want, reason)
			}
		})
	}
}

// The window is lagged by one period, so a partial period cannot flip an
// alarm as its observations arrive.
func TestEvaluateLagsTheNewestPeriod(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// A breach lands in the minute containing "now", which is still filling.
	now := base.Add(90 * time.Second)

	s, err := New(Options{DataDir: t.TempDir(), Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.putSamples([]datum{{
		Namespace: "T", MetricName: "M", Value: 100, Resolution: 60,
		Timestamp: base.Add(70 * time.Second), // inside the partial period
	}}); err != nil {
		t.Fatal(err)
	}
	a := &alarm{
		Name: "a", Namespace: "T", MetricName: "M", Statistic: "Sum",
		Period: 60, EvaluationPeriods: 1, DatapointsToAlarm: 1,
		ComparisonOp: "GreaterThanThreshold", Threshold: 10,
		State: stateOK,
	}
	if got, _, _ := s.evaluate(a, now); got == stateAlarm {
		t.Error("a partial period flipped the alarm; the window must lag by one")
	}
}

// "ignore" means the alarm holds whatever it has when there is nothing to
// judge, which is different from every other treatment.
func TestEvaluateIgnoreHoldsTheState(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := base.Add(3 * time.Minute)
	s, err := New(Options{DataDir: t.TempDir(), Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := &alarm{
		Name: "a", Namespace: "T", MetricName: "M", Statistic: "Sum",
		Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: 1,
		ComparisonOp: "GreaterThanThreshold", Threshold: 10,
		TreatMissingData: "ignore", State: stateAlarm,
	}
	// No samples at all, so every period is missing.
	if got, _, _ := s.evaluate(a, now); got != "" {
		t.Errorf("ignore moved the alarm to %q; it should hold %q", got, a.State)
	}
}

// An alarm already in the state the metric implies does not re-transition,
// which is what stops an action firing on every tick.
func TestEvaluateDoesNotRepeatAState(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := base.Add(3 * time.Minute)
	s, err := New(Options{DataDir: t.TempDir(), Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.putSamples([]datum{{
		Namespace: "T", MetricName: "M", Value: 100, Resolution: 60,
		Timestamp: base,
	}}); err != nil {
		t.Fatal(err)
	}
	a := &alarm{
		Name: "a", Namespace: "T", MetricName: "M", Statistic: "Sum",
		Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: 1,
		ComparisonOp: "GreaterThanThreshold", Threshold: 10,
		State: stateAlarm, // already there
	}
	if got, _, _ := s.evaluate(a, now); got != "" {
		t.Errorf("an alarm already in ALARM re-transitioned to %q", got)
	}
}

func TestClassifyAction(t *testing.T) {
	cases := []struct {
		arn  string
		kind actionTarget
		name string
	}{
		{"arn:aws:sns:us-east-1:000000000000:alerts", targetSNS, "alerts"},
		{"arn:aws:lambda:us-east-1:000000000000:function:responder", targetLambda, "responder"},
		{"arn:aws:automate:us-east-1:ec2:stop", targetUnknown, ""},
		{"arn:aws:autoscaling:us-east-1:000000000000:scalingPolicy:x", targetUnknown, ""},
		{"", targetUnknown, ""},
	}
	for _, c := range cases {
		kind, name := classifyAction(c.arn)
		if kind != c.kind || name != c.name {
			t.Errorf("classifyAction(%q) = (%v, %q), want (%v, %q)",
				c.arn, kind, name, c.kind, c.name)
		}
	}
}
