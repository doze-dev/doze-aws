package console

// ---- CloudWatch ----
//
// What the console reads: the metrics something published, a sparkline of one
// of them, and the alarms watching them.
//
// The console speaks JSON 1.0 to CloudWatch, which is the AWS CLI's wire —
// deliberately, so the pane exercises the protocol most people's tooling uses
// rather than the CBOR one only the Go v2 SDK speaks.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const cwTarget = "GraniteServiceVersion20100801"

func (b *backend) cw(ctx context.Context, action string, in any) ([]byte, error) {
	return b.json10(ctx, cwTarget, action, in)
}

// Metric is one metric series: its identity, which is namespace, name and the
// exact set of dimensions. Two metrics with the same name and different
// dimensions are two metrics.
type Metric struct {
	Namespace  string
	Name       string
	Dimensions map[string]string
	// Label renders the dimension set for a row, e.g. "FunctionName=worker".
	Label string
	// Key identifies the series in a URL, so a row links to its own chart.
	Key string
}

// ListMetrics answers every series, sorted by namespace then name then
// dimension label so the browser is stable between renders.
func (b *backend) ListMetrics(ctx context.Context, namespace string) ([]Metric, error) {
	in := map[string]any{}
	if namespace != "" {
		in["Namespace"] = namespace
	}
	var out []Metric
	token := ""
	for {
		if token != "" {
			in["NextToken"] = token
		}
		body, err := b.cw(ctx, "ListMetrics", in)
		if err != nil {
			return nil, err
		}
		var res struct {
			Metrics []struct {
				Namespace, MetricName string
				Dimensions            []struct{ Name, Value string }
			}
			NextToken string
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, err
		}
		for _, m := range res.Metrics {
			met := Metric{Namespace: m.Namespace, Name: m.MetricName}
			if len(m.Dimensions) > 0 {
				met.Dimensions = map[string]string{}
				for _, d := range m.Dimensions {
					met.Dimensions[d.Name] = d.Value
				}
			}
			met.Label = dimensionLabel(met.Dimensions)
			met.Key = metricKey(met)
			out = append(out, met)
		}
		if res.NextToken == "" || res.NextToken == token {
			break
		}
		token = res.NextToken
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Label < out[j].Label
	})
	return out, nil
}

// dimensionLabel renders a dimension set for display, sorted so the same set
// always reads the same way.
func dimensionLabel(dims map[string]string) string {
	if len(dims) == 0 {
		return "no dimensions"
	}
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+dims[k])
	}
	return strings.Join(parts, ", ")
}

// metricKey encodes a series into one URL-safe string, and parseMetricKey
// reads it back. The separators are characters AWS forbids in a namespace,
// metric name or dimension name, so a key cannot be forged by naming a
// metric cleverly.
func metricKey(m Metric) string {
	parts := []string{m.Namespace, m.Name}
	keys := make([]string, 0, len(m.Dimensions))
	for k := range m.Dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"~"+m.Dimensions[k])
	}
	return strings.Join(parts, "|")
}

func parseMetricKey(key string) Metric {
	parts := strings.Split(key, "|")
	if len(parts) < 2 {
		return Metric{}
	}
	m := Metric{Namespace: parts[0], Name: parts[1]}
	for _, p := range parts[2:] {
		k, v, ok := strings.Cut(p, "~")
		if !ok {
			continue
		}
		if m.Dimensions == nil {
			m.Dimensions = map[string]string{}
		}
		m.Dimensions[k] = v
	}
	m.Label = dimensionLabel(m.Dimensions)
	m.Key = key
	return m
}

// dimensionPairs renders the map as the API's list of Name/Value structures.
func dimensionPairs(dims map[string]string) []any {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"Name": k, "Value": dims[k]})
	}
	return out
}

// Series is one metric's values over a window, ready to plot.
type Series struct {
	Metric Metric
	Stat   string
	Period int
	Points []Point
	// Min, Max and Last describe the window, so the pane can label the chart
	// without the template doing arithmetic.
	Min, Max, Last float64
	// Path is the SVG polyline for the sparkline, rendered server-side —
	// there is no charting library in the console and this needs none.
	Path string
	// Dots marks the individual observations of a sparse series.
	Dots   []Dot
	Width  int
	Height int
}

// Dot is one observation's position in the chart's viewBox.
type Dot struct{ X, Y float64 }

type Point struct {
	At    time.Time
	Value float64
}

// MetricSeries reads one series over the last window, via GetMetricData —
// the modern operation, and the one an SDK uses for a chart.
func (b *backend) MetricSeries(ctx context.Context, m Metric, stat string, window time.Duration) (Series, error) {
	if stat == "" {
		stat = "Sum"
	}
	// A period that gives roughly 60 points across the window, rounded to a
	// minute, so a chart is readable at any window length.
	period := int(window.Seconds()) / 60
	if period < 60 {
		period = 60
	}
	period = (period / 60) * 60
	end := time.Now()
	start := end.Add(-window)

	metric := map[string]any{"Namespace": m.Namespace, "MetricName": m.Name}
	if len(m.Dimensions) > 0 {
		metric["Dimensions"] = dimensionPairs(m.Dimensions)
	}
	// RFC3339Nano, not RFC3339. RFC3339 truncates the fractional second, so an
	// EndTime of 18:42:30.412 is sent as 18:42:30 — and the window then ENDS
	// before a datapoint published in that same second, which drops the newest
	// point off the chart for up to a second after it was written. That is
	// exactly the moment someone is looking.
	body, err := b.cw(ctx, "GetMetricData", map[string]any{
		"StartTime": start.UTC().Format(time.RFC3339Nano),
		"EndTime":   end.UTC().Format(time.RFC3339Nano),
		"MetricDataQueries": []any{map[string]any{
			"Id":         "m1",
			"MetricStat": map[string]any{"Metric": metric, "Period": period, "Stat": stat},
		}},
	})
	if err != nil {
		return Series{}, err
	}
	var res struct {
		MetricDataResults []struct {
			Timestamps []time.Time
			Values     []float64
		}
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return Series{}, err
	}
	s := Series{Metric: m, Stat: stat, Period: period}
	if len(res.MetricDataResults) > 0 {
		r := res.MetricDataResults[0]
		for i := range r.Timestamps {
			if i < len(r.Values) {
				s.Points = append(s.Points, Point{At: r.Timestamps[i], Value: r.Values[i]})
			}
		}
	}
	sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].At.Before(s.Points[j].At) })
	s.summarise()
	return s, nil
}

// summarise fills in the range and the sparkline path.
func (s *Series) summarise() {
	s.Width, s.Height = 720, 120
	if len(s.Points) == 0 {
		return
	}
	s.Min, s.Max = math.Inf(1), math.Inf(-1)
	for _, p := range s.Points {
		s.Min = math.Min(s.Min, p.Value)
		s.Max = math.Max(s.Max, p.Value)
	}
	s.Last = s.Points[len(s.Points)-1].Value
	// A flat series still gets a line, drawn through the middle: scaling a
	// zero range would divide by zero, and drawing nothing would read as "no
	// data" when the truth is "no change".
	span := s.Max - s.Min
	if span == 0 {
		span = 1
		s.Min -= 0.5
	}
	first, last := s.Points[0].At, s.Points[len(s.Points)-1].At
	across := last.Sub(first).Seconds()
	if across <= 0 {
		across = 1
	}
	const pad = 8
	var b strings.Builder
	for i, p := range s.Points {
		x := float64(pad) + (p.At.Sub(first).Seconds()/across)*float64(s.Width-2*pad)
		y := float64(s.Height-pad) - ((p.Value-s.Min)/span)*float64(s.Height-2*pad)
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
		// A sparse series also gets a dot per observation. A polyline through
		// ONE point has zero area and draws nothing at all — publish a single
		// metric, open the chart, and the panel looks broken rather than
		// sparse. Dots say how many observations there actually are, which is
		// the honest thing to show when there are few.
		if len(s.Points) <= sparseDots {
			s.Dots = append(s.Dots, Dot{X: x, Y: y})
		}
	}
	s.Path = b.String()
}

// sparseDots is where a chart stops marking individual observations. Above it
// the dots merge into the line and stop carrying information.
const sparseDots = 30

// Alarm is one metric alarm as the console shows it.
type Alarm struct {
	Name        string
	Description string
	State       string // OK | ALARM | INSUFFICIENT_DATA
	StateReason string
	Updated     string
	Namespace   string
	MetricName  string
	Dimensions  map[string]string
	DimLabel    string
	Statistic   string
	Period      int
	Evaluation  int
	Datapoints  int
	Operator    string
	Threshold   float64
	Missing     string
	Enabled     bool
	Actions     []string
	OKActions   []string
	NoData      []string
	ARN         string
	// MetricKey links the alarm to the chart of the metric it watches.
	MetricKey string
}

func (b *backend) ListAlarms(ctx context.Context) ([]Alarm, error) {
	body, err := b.cw(ctx, "DescribeAlarms", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		MetricAlarms []cwAlarmWire
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	out := make([]Alarm, 0, len(res.MetricAlarms))
	for _, a := range res.MetricAlarms {
		out = append(out, a.toAlarm())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// cwAlarmWire is the API's alarm shape, kept separate from the view type so
// the field names stay the API's.
type cwAlarmWire struct {
	AlarmName, AlarmDescription  string
	AlarmArn                     string
	StateValue, StateReason      string
	StateUpdatedTimestamp        time.Time
	Namespace, MetricName        string
	Statistic, ExtendedStatistic string
	ComparisonOperator           string
	TreatMissingData             string
	Period, EvaluationPeriods    int
	DatapointsToAlarm            int
	Threshold                    float64
	ActionsEnabled               bool
	AlarmActions, OKActions      []string
	InsufficientDataActions      []string
	Dimensions                   []struct{ Name, Value string }
}

func (w cwAlarmWire) toAlarm() Alarm {
	a := Alarm{
		Name: w.AlarmName, Description: w.AlarmDescription, ARN: w.AlarmArn,
		State: w.StateValue, StateReason: w.StateReason,
		Namespace: w.Namespace, MetricName: w.MetricName,
		Statistic: w.Statistic, Period: w.Period,
		Evaluation: w.EvaluationPeriods, Datapoints: w.DatapointsToAlarm,
		Operator: w.ComparisonOperator, Threshold: w.Threshold,
		Missing: w.TreatMissingData, Enabled: w.ActionsEnabled,
		Actions: w.AlarmActions, OKActions: w.OKActions, NoData: w.InsufficientDataActions,
	}
	if w.ExtendedStatistic != "" {
		a.Statistic = w.ExtendedStatistic
	}
	if !w.StateUpdatedTimestamp.IsZero() {
		a.Updated = w.StateUpdatedTimestamp.Local().Format("2006-01-02 15:04:05")
	}
	if len(w.Dimensions) > 0 {
		a.Dimensions = map[string]string{}
		for _, d := range w.Dimensions {
			a.Dimensions[d.Name] = d.Value
		}
	}
	a.DimLabel = dimensionLabel(a.Dimensions)
	a.MetricKey = metricKey(Metric{Namespace: a.Namespace, Name: a.MetricName, Dimensions: a.Dimensions})
	return a
}

// AlarmsForMetric answers which alarms watch one series, which is how the
// chart says "this metric has an alarm on it".
func (b *backend) AlarmsForMetric(ctx context.Context, m Metric) ([]Alarm, error) {
	in := map[string]any{"Namespace": m.Namespace, "MetricName": m.Name}
	if len(m.Dimensions) > 0 {
		in["Dimensions"] = dimensionPairs(m.Dimensions)
	}
	body, err := b.cw(ctx, "DescribeAlarmsForMetric", in)
	if err != nil {
		return nil, err
	}
	var res struct {
		MetricAlarms []cwAlarmWire
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	out := make([]Alarm, 0, len(res.MetricAlarms))
	for _, a := range res.MetricAlarms {
		out = append(out, a.toAlarm())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AlarmHistoryEntry is one line of an alarm's history.
type AlarmHistoryEntry struct {
	When    string
	Type    string
	Summary string
}

func (b *backend) AlarmHistory(ctx context.Context, name string) ([]AlarmHistoryEntry, error) {
	body, err := b.cw(ctx, "DescribeAlarmHistory", map[string]any{
		"AlarmName": name, "MaxRecords": 50, "ScanBy": "TimestampDescending",
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		AlarmHistoryItems []struct {
			Timestamp       time.Time
			HistoryItemType string
			HistorySummary  string
		}
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	out := make([]AlarmHistoryEntry, 0, len(res.AlarmHistoryItems))
	for _, h := range res.AlarmHistoryItems {
		out = append(out, AlarmHistoryEntry{
			When:    h.Timestamp.Local().Format("2006-01-02 15:04:05"),
			Type:    h.HistoryItemType,
			Summary: h.HistorySummary,
		})
	}
	return out, nil
}

// PutAlarm creates or replaces an alarm. A percentile statistic travels as
// ExtendedStatistic, which is the split the API makes.
func (b *backend) PutAlarm(ctx context.Context, a Alarm) error {
	in := map[string]any{
		"AlarmName":          a.Name,
		"Namespace":          a.Namespace,
		"MetricName":         a.MetricName,
		"ComparisonOperator": a.Operator,
		"Threshold":          a.Threshold,
		"Period":             a.Period,
		"EvaluationPeriods":  a.Evaluation,
	}
	if strings.HasPrefix(a.Statistic, "p") {
		in["ExtendedStatistic"] = a.Statistic
	} else {
		in["Statistic"] = a.Statistic
	}
	if a.Description != "" {
		in["AlarmDescription"] = a.Description
	}
	if a.Missing != "" {
		in["TreatMissingData"] = a.Missing
	}
	if a.Datapoints > 0 {
		in["DatapointsToAlarm"] = a.Datapoints
	}
	if len(a.Dimensions) > 0 {
		in["Dimensions"] = dimensionPairs(a.Dimensions)
	}
	if len(a.Actions) > 0 {
		in["AlarmActions"] = a.Actions
	}
	if len(a.OKActions) > 0 {
		in["OKActions"] = a.OKActions
	}
	_, err := b.cw(ctx, "PutMetricAlarm", in)
	return err
}

func (b *backend) DeleteAlarm(ctx context.Context, name string) error {
	_, err := b.cw(ctx, "DeleteAlarms", map[string]any{"AlarmNames": []string{name}})
	return err
}

// SetAlarmState flips an alarm by hand, which is how a developer tests the
// thing the alarm notifies without waiting for the metric to breach.
func (b *backend) SetAlarmState(ctx context.Context, name, state, reason string) error {
	if reason == "" {
		reason = "Set from the doze-aws console"
	}
	_, err := b.cw(ctx, "SetAlarmState", map[string]any{
		"AlarmName": name, "StateValue": state, "StateReason": reason,
	})
	return err
}

// SetAlarmActions enables or disables an alarm's actions without deleting it.
func (b *backend) SetAlarmActions(ctx context.Context, name string, enabled bool) error {
	action := "DisableAlarmActions"
	if enabled {
		action = "EnableAlarmActions"
	}
	_, err := b.cw(ctx, action, map[string]any{"AlarmNames": []string{name}})
	return err
}

// Namespaces groups the metric list for the browser's first column.
func Namespaces(metrics []Metric) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range metrics {
		if !seen[m.Namespace] {
			seen[m.Namespace] = true
			out = append(out, m.Namespace)
		}
	}
	sort.Strings(out)
	return out
}
