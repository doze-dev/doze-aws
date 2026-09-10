package cloudwatch

// The alarm evaluator.
//
// # M out of N, over lagged periods
//
// An alarm examines the last EvaluationPeriods periods and alarms when
// DatapointsToAlarm of them breach. The window is LAGGED by one period: the
// period containing "now" is still being written to, and judging a partial
// period produces an alarm that flaps as observations arrive. AWS lags for
// the same reason.
//
// # Missing data is a fourth answer, not a zero
//
// A period with no observations is not a period that observed zero, so each
// one is resolved by TreatMissingData before the M-of-N count:
//
//	missing       the default — a missing period neither breaches nor clears
//	notBreaching  treated as good
//	breaching     treated as bad
//	ignore        the alarm keeps its current state, whatever the rest say
//
// When every examined period is missing and the treatment is `missing`, the
// alarm is INSUFFICIENT_DATA — which is what that state is for, and why it is
// not just a synonym for OK.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/doze-dev/doze-aws/internal/bg"
)

// evalInterval is how often alarms are re-examined. Ten seconds is far finer
// than the shortest useful period; the cost is a read per alarm, and the
// benefit is that a developer watching an alarm flip does not wait a minute
// to see it.
const evalInterval = 10 * time.Second

// datapointVerdict is what one period contributed.
type datapointVerdict int

const (
	verdictGood datapointVerdict = iota
	verdictBreaching
	verdictMissing
)

// breaches applies the comparison operator.
func breaches(op string, value, threshold float64) bool {
	switch op {
	case "GreaterThanOrEqualToThreshold":
		return value >= threshold
	case "GreaterThanThreshold":
		return value > threshold
	case "LessThanThreshold":
		return value < threshold
	case "LessThanOrEqualToThreshold":
		return value <= threshold
	}
	// An anomaly operator never reaches here — PutMetricAlarm refuses those —
	// so an unknown operator not breaching is the safe reading.
	return false
}

// evaluate decides what state an alarm should be in, and why. It returns the
// empty string when the alarm should not move.
func (s *Server) evaluate(a *alarm, now time.Time) (state, reason, data string) {
	period := time.Duration(a.Period) * time.Second
	// Lag by one period: the newest period is still filling.
	end := alignDown(now, period)
	start := end.Add(-time.Duration(a.EvaluationPeriods) * period)

	samples, err := s.readSamples(seriesKey(a.Namespace, a.MetricName, a.Dimensions), start, end)
	if err != nil {
		return "", "", ""
	}
	if a.Unit != "" {
		kept := samples[:0]
		for _, sm := range samples {
			if sm.Unit == a.Unit {
				kept = append(kept, sm)
			}
		}
		samples = kept
	}

	// Index the populated periods, then walk every period in the window so
	// the missing ones are visible as missing rather than absent.
	byStart := map[int64]*bucket{}
	for _, b := range bucketize(samples, start, end, period) {
		byStart[b.Start.UnixNano()] = b
	}

	st := stat{Name: a.Statistic, Pct: -1}
	if p, ok := parseStat(a.Statistic); ok {
		st = p
	}

	var verdicts []datapointVerdict
	var values []float64
	for i := range a.EvaluationPeriods {
		at := start.Add(time.Duration(i) * period)
		b, ok := byStart[at.UnixNano()]
		if !ok {
			verdicts = append(verdicts, verdictMissing)
			continue
		}
		v := b.value(st)
		values = append(values, v)
		if breaches(a.ComparisonOp, v, a.Threshold) {
			verdicts = append(verdicts, verdictBreaching)
		} else {
			verdicts = append(verdicts, verdictGood)
		}
	}

	treat := a.TreatMissingData
	if treat == "" {
		treat = "missing"
	}
	if treat == "ignore" {
		// The alarm keeps its state unless a real datapoint says otherwise,
		// so missing periods are dropped rather than resolved.
		kept := verdicts[:0]
		for _, v := range verdicts {
			if v != verdictMissing {
				kept = append(kept, v)
			}
		}
		verdicts = kept
		if len(verdicts) == 0 {
			return "", "", ""
		}
	}

	breaching, missing := 0, 0
	for _, v := range verdicts {
		switch v {
		case verdictBreaching:
			breaching++
		case verdictMissing:
			missing++
			switch treat {
			case "breaching":
				breaching++
			case "missing":
				// Neither breaches nor clears.
			}
		}
	}

	want := stateOK
	switch {
	case breaching >= a.DatapointsToAlarm:
		want = stateAlarm
	case treat == "missing" && missing == len(verdicts) && len(verdicts) > 0:
		// Nothing was observed at all, which is what INSUFFICIENT_DATA means.
		want = stateInsufficientData
	}
	if want == a.State {
		return "", "", ""
	}

	reason = fmt.Sprintf(
		"Threshold Crossed: %d out of the last %d datapoints %s the threshold (%v) "+
			"(minimum %d datapoint%s for OK -> ALARM transition).",
		breaching, len(verdicts), comparisonWords(a.ComparisonOp), a.Threshold,
		a.DatapointsToAlarm, plural(a.DatapointsToAlarm))
	if want == stateInsufficientData {
		reason = fmt.Sprintf(
			"Insufficient Data: %d datapoints were unknown.", missing)
	} else if want == stateOK {
		reason = fmt.Sprintf(
			"Threshold Crossed: %d out of the last %d datapoints were not %s the threshold (%v).",
			len(verdicts)-breaching, len(verdicts), comparisonWords(a.ComparisonOp), a.Threshold)
	}

	blob, _ := json.Marshal(map[string]any{
		"version":             "1.0",
		"queryDate":           now.UTC().Format(time.RFC3339Nano),
		"statistic":           a.Statistic,
		"period":              a.Period,
		"recentDatapoints":    values,
		"threshold":           a.Threshold,
		"evaluatedDatapoints": len(verdicts),
		"breachingDatapoints": breaching,
	})
	return want, reason, string(blob)
}

func comparisonWords(op string) string {
	switch op {
	case "GreaterThanOrEqualToThreshold":
		return "were greater than or equal to"
	case "GreaterThanThreshold":
		return "were greater than"
	case "LessThanThreshold":
		return "were less than"
	case "LessThanOrEqualToThreshold":
		return "were less than or equal to"
	}
	return "crossed"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// evaluator re-examines every alarm on a tick.
func (s *Server) evaluator() {
	defer s.bg.Done()
	t := time.NewTicker(evalInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "cloudwatch: alarm evaluator", s.EvaluateNow)
		}
	}
}

// EvaluateNow runs one evaluation pass. Exported so a test drives the state
// machine directly rather than sleeping for a tick.
func (s *Server) EvaluateNow() {
	alarms, err := s.listAlarms()
	if err != nil {
		s.logf("cloudwatch: listing alarms: %v", err)
		return
	}
	now := s.now()
	for _, a := range alarms {
		want, reason, data := s.evaluate(a, now)
		if want == "" {
			continue
		}
		// A state put there by SetAlarmState is deliberate, so the first
		// evaluation after it does not immediately undo it. The flag clears
		// as soon as the metric would have produced the same state anyway.
		if a.StateSetManually {
			a.StateSetManually = false
			if err := s.putAlarm(a, historyEntry{}); err != nil {
				s.logf("cloudwatch: %v", err)
			}
			continue
		}
		s.transition(a, want, reason, data, now)
	}
}

// transition moves an alarm, records it, and fires the actions for the state
// it entered.
func (s *Server) transition(a *alarm, want, reason, data string, now time.Time) {
	prev := a.State
	a.State, a.StateReason, a.StateReasonData = want, reason, data
	a.StateUpdatedMs = now.UnixMilli()
	h := historyEntry{
		AlarmName: a.Name, Type: historyStateUpdate, AtMs: now.UnixMilli(),
		Summary: fmt.Sprintf("Alarm updated from %s to %s", prev, want),
		Data:    data,
	}
	if err := s.putAlarm(a, h); err != nil {
		s.logf("cloudwatch: storing alarm %s: %v", a.Name, err)
		return
	}
	s.logf("cloudwatch: alarm %s %s -> %s", a.Name, prev, want)
	s.fireActions(a, prev, want, reason, now)
}
