package cloudwatch

// The alarm control plane.

import (
	"slices"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// maxEvaluationWindow is AWS's ceiling on EvaluationPeriods × Period. A day
// of periods is the most an alarm may look back over.
const maxEvaluationWindow = 86400

// putMetricAlarm creates or replaces an alarm.
//
// The checks here are the ones the model cannot state: that the action targets
// exist locally, that the evaluation window is within AWS's ceiling, and that
// DatapointsToAlarm does not exceed the periods being examined. Each is a
// deploy-time failure on AWS, which makes it worth catching here.
func (s *Server) putMetricAlarm(req *request) (any, *awshttp.APIError) {
	name := req.params.Str("AlarmName")
	if name == "" {
		return nil, errMissingParameter("The parameter AlarmName is required.")
	}
	if req.params.Has("Metrics") {
		return nil, errf("InvalidParameterValueException",
			"doze-aws does not evaluate metric math: alarm %s uses Metrics. "+
				"A single metric with MetricName, Namespace, Statistic and Period is "+
				"supported, which is what CDK emits for a threshold alarm.", name)
	}
	if id := req.params.Str("ThresholdMetricId"); id != "" {
		return nil, errf("InvalidParameterValueException",
			"alarm %s is an anomaly-detection alarm (ThresholdMetricId %s); "+
				"doze-aws has no trained band to compare against.", name, id)
	}
	// Refused rather than ignored. A warm-up suppresses an alarm while the
	// resource it watches settles; accepting the field and evaluating anyway
	// would fire an alarm the caller asked to be held back, which is worse
	// than saying the feature is not here.
	if req.params.Has("WarmUpConfiguration") {
		return nil, errf("InvalidParameterValueException",
			"alarm %s sets WarmUpConfiguration; doze-aws evaluates every period "+
				"from the moment the alarm exists and has no warm-up to honour.", name)
	}

	op := req.params.Str("ComparisonOperator")
	if op == "" {
		return nil, errMissingParameter("The parameter ComparisonOperator is required.")
	}
	if anomalyOperators[op] {
		return nil, errf("InvalidParameterValueException",
			"the comparison operator %s belongs to an anomaly-detection alarm, "+
				"which doze-aws does not evaluate.", op)
	}
	ns, metric := req.params.Str("Namespace"), req.params.Str("MetricName")
	if ns == "" || metric == "" {
		return nil, errMissingParameter(
			"An alarm needs Namespace and MetricName; doze-aws does not evaluate Metrics.")
	}
	statistic := req.params.Str("Statistic")
	if ext := req.params.Str("ExtendedStatistic"); ext != "" {
		statistic = ext
	}
	if statistic == "" {
		return nil, errMissingParameter(
			"The parameter Statistic or ExtendedStatistic is required.")
	}
	if _, ok := parseStat(statistic); !ok {
		return nil, errInvalidParameter("The value %s is not a valid statistic.", statistic)
	}

	period := req.params.Int("Period", 0)
	if period <= 0 {
		return nil, errMissingParameter("The parameter Period is required.")
	}
	if aerr := checkPeriod(period); aerr != nil {
		return nil, aerr
	}
	evalPeriods := req.params.Int("EvaluationPeriods", 0)
	if evalPeriods <= 0 {
		return nil, errMissingParameter("The parameter EvaluationPeriods is required.")
	}
	if period*evalPeriods > maxEvaluationWindow {
		return nil, errInvalidParameter(
			"The evaluation window (Period × EvaluationPeriods = %d seconds) exceeds "+
				"the maximum of %d.", period*evalPeriods, maxEvaluationWindow)
	}
	toAlarm := req.params.Int("DatapointsToAlarm", evalPeriods)
	if toAlarm > evalPeriods {
		return nil, errInvalidParameter(
			"DatapointsToAlarm (%d) must not exceed EvaluationPeriods (%d).",
			toAlarm, evalPeriods)
	}
	threshold, hasThreshold := req.params.Float("Threshold")
	if !hasThreshold {
		return nil, errMissingParameter("The parameter Threshold is required.")
	}
	treat := req.params.Str("TreatMissingData")
	if treat != "" && !slices.Contains(treatMissingValues, treat) {
		return nil, errInvalidParameter(
			"The value %s is not a valid TreatMissingData; expected one of %s.",
			treat, strings.Join(treatMissingValues, ", "))
	}

	dims := map[string]string{}
	for _, d := range req.params.List("Dimensions") {
		n, v := d.Str("Name"), d.Str("Value")
		if n == "" || v == "" {
			return nil, errInvalidParameter("The dimension Name and Value must both be set.")
		}
		dims[n] = v
	}

	alarmActions, aerr := checkActions(req.params.Strs("AlarmActions"))
	if aerr != nil {
		return nil, aerr
	}
	okActions, aerr := checkActions(req.params.Strs("OKActions"))
	if aerr != nil {
		return nil, aerr
	}
	insufficient, aerr := checkActions(req.params.Strs("InsufficientDataActions"))
	if aerr != nil {
		return nil, aerr
	}

	now := s.now()
	prev, err := s.getAlarm(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	a := &alarm{
		Name: name, Description: req.params.Str("AlarmDescription"),
		Namespace: ns, MetricName: metric, Dimensions: dims,
		Statistic: statistic, Unit: req.params.Str("Unit"),
		Period: period, EvaluationPeriods: evalPeriods, DatapointsToAlarm: toAlarm,
		ComparisonOp: op, Threshold: threshold, TreatMissingData: treat,
		ActionsEnabled: true,
		AlarmActions:   alarmActions, OKActions: okActions, InsufficientData: insufficient,
		State: stateInsufficientData, StateReason: "Unchecked: Initial alarm creation",
		StateUpdatedMs: now.UnixMilli(), UpdatedMs: now.UnixMilli(),
	}
	s.stamp(a)
	if req.params.Has("ActionsEnabled") {
		a.ActionsEnabled = req.params.Bool("ActionsEnabled")
	}
	a.Tags = tagsFromParams(req.params, "Tags")
	// Replacing an alarm keeps its state: AWS does not reset an alarm to
	// INSUFFICIENT_DATA because its description changed.
	summary := "Alarm " + name + " created"
	if prev != nil {
		a.State, a.StateReason = prev.State, prev.StateReason
		a.StateReasonData, a.StateUpdatedMs = prev.StateReasonData, prev.StateUpdatedMs
		summary = "Alarm " + name + " updated"
		// Tags belong to the resource, not to the put: a re-put that does not
		// mention them keeps what TagResource added, as on AWS.
		if len(a.Tags) == 0 {
			a.Tags = prev.Tags
		}
	}
	h := historyEntry{AlarmName: name, Type: historyConfigUpdate,
		AtMs: now.UnixMilli(), Summary: summary}
	if err := s.putAlarm(a, h); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

// checkActions refuses an action target doze-aws cannot reach. Accepting one
// and never firing it would be the worse failure: an alarm that looks wired
// up and silently does nothing.
func checkActions(arns []string) ([]string, *awshttp.APIError) {
	for _, arn := range arns {
		if kind, _ := classifyAction(arn); kind == targetUnknown {
			return nil, errf("InvalidParameterValueException",
				"doze-aws can notify an SNS topic or invoke a Lambda function; "+
					"%s is neither, and an action it cannot deliver would be an "+
					"alarm that looks wired up and does nothing.", arn)
		}
	}
	return arns, nil
}

func (s *Server) describeAlarms(req *request) (any, *awshttp.APIError) {
	all, err := s.listAlarms()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	names := req.params.Strs("AlarmNames")
	prefix := req.params.Str("AlarmNamePrefix")
	state := req.params.Str("StateValue")
	actionPrefix := req.params.Str("ActionPrefix")

	// AWS answers composite alarms in a separate member. doze-aws has none,
	// so it is always empty rather than absent — a caller ranging over it
	// should find a list.
	out := describeAlarmsResult{MetricAlarms: []alarmView{}, CompositeAlarms: []alarmView{}}

	// Three filters select things doze-aws never holds, and each answers
	// nothing rather than everything. Accepting one and ignoring it would be
	// worse than refusing it: a caller asking for the children of a composite
	// alarm would receive every metric alarm in the account and believe it.
	if req.params.Str("ChildrenOfAlarmName") != "" || req.params.Str("ParentsOfAlarmName") != "" {
		return out, nil
	}
	if types := req.params.Strs("AlarmTypes"); len(types) > 0 && !slices.Contains(types, "MetricAlarm") {
		return out, nil
	}

	for _, a := range all {
		if len(names) > 0 && !slices.Contains(names, a.Name) {
			continue
		}
		if prefix != "" && !strings.HasPrefix(a.Name, prefix) {
			continue
		}
		if state != "" && a.State != state {
			continue
		}
		if actionPrefix != "" && !hasActionWithPrefix(a, actionPrefix) {
			continue
		}
		out.MetricAlarms = append(out.MetricAlarms, viewOf(a))
	}
	return out, nil
}

// hasActionWithPrefix reports whether any of an alarm's actions starts with
// the prefix — AWS's ActionPrefix filter, which is how "every alarm that
// notifies this topic" is asked.
func hasActionWithPrefix(a *alarm, prefix string) bool {
	for _, group := range [][]string{a.AlarmActions, a.OKActions, a.InsufficientData} {
		for _, arn := range group {
			if strings.HasPrefix(arn, prefix) {
				return true
			}
		}
	}
	return false
}

// describeAlarmsForMetric answers which alarms watch one metric, which is how
// a console shows "this metric has an alarm on it".
func (s *Server) describeAlarmsForMetric(req *request) (any, *awshttp.APIError) {
	ns, metric := req.params.Str("Namespace"), req.params.Str("MetricName")
	if ns == "" || metric == "" {
		return nil, errMissingParameter("Namespace and MetricName are required.")
	}
	dims := map[string]string{}
	for _, d := range req.params.List("Dimensions") {
		if n, v := d.Str("Name"), d.Str("Value"); n != "" && v != "" {
			dims[n] = v
		}
	}
	wantSig := dimensionSignature(dims)

	all, err := s.listAlarms()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := describeAlarmsForMetricResult{MetricAlarms: []alarmView{}}
	for _, a := range all {
		if a.Namespace != ns || a.MetricName != metric {
			continue
		}
		// Dimensions narrow when given; AWS matches the exact set, since an
		// alarm on {Stage:prod} is not an alarm on the undimensioned metric.
		if len(dims) > 0 && dimensionSignature(a.Dimensions) != wantSig {
			continue
		}
		if st := req.params.Str("Statistic"); st != "" && a.Statistic != st {
			continue
		}
		if p := req.params.Int("Period", 0); p > 0 && a.Period != p {
			continue
		}
		out.MetricAlarms = append(out.MetricAlarms, viewOf(a))
	}
	return out, nil
}

func (s *Server) deleteAlarmsAction(req *request) (any, *awshttp.APIError) {
	names := req.params.Strs("AlarmNames")
	if len(names) == 0 {
		return nil, errMissingParameter("The parameter AlarmNames is required.")
	}
	// AWS deletes what it can and reports ResourceNotFound only when nothing
	// matched, so a partially-stale list still cleans up.
	found := false
	for _, n := range names {
		a, err := s.getAlarm(n)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		if a != nil {
			found = true
		}
	}
	if !found {
		return nil, errf("ResourceNotFound", "No alarms matched the names given.")
	}
	if err := s.deleteAlarms(names); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return nil, nil
}

// setAlarmState flips an alarm by hand, which is how a developer tests what
// happens downstream without waiting for the metric to move. It fires the
// actions, as on AWS — that being the whole point of it.
func (s *Server) setAlarmState(req *request) (any, *awshttp.APIError) {
	name := req.params.Str("AlarmName")
	state := req.params.Str("StateValue")
	reason := req.params.Str("StateReason")
	if name == "" || state == "" || reason == "" {
		return nil, errMissingParameter("AlarmName, StateValue and StateReason are required.")
	}
	if !slices.Contains([]string{stateOK, stateAlarm, stateInsufficientData}, state) {
		return nil, errInvalidParameter(
			"The value %s is not a valid alarm state.", state)
	}
	a, err := s.getAlarm(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if a == nil {
		return nil, errf("ResourceNotFound", "Alarm %s does not exist.", name)
	}
	// Marked manual so the next evaluation does not immediately undo it,
	// which would make the flip useless for testing a downstream handler.
	a.StateSetManually = true
	s.transition(a, state, reason, req.params.Str("StateReasonData"), s.now())
	return nil, nil
}

func (s *Server) enableAlarmActions(req *request) (any, *awshttp.APIError) {
	return s.setActionsEnabled(req, true)
}

func (s *Server) disableAlarmActions(req *request) (any, *awshttp.APIError) {
	return s.setActionsEnabled(req, false)
}

func (s *Server) setActionsEnabled(req *request, enabled bool) (any, *awshttp.APIError) {
	names := req.params.Strs("AlarmNames")
	if len(names) == 0 {
		return nil, errMissingParameter("The parameter AlarmNames is required.")
	}
	now := s.now()
	for _, n := range names {
		a, err := s.getAlarm(n)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		if a == nil {
			continue // AWS ignores names that do not exist here
		}
		if a.ActionsEnabled == enabled {
			continue
		}
		a.ActionsEnabled = enabled
		a.UpdatedMs = now.UnixMilli()
		verb := "disabled"
		if enabled {
			verb = "enabled"
		}
		if err := s.putAlarm(a, historyEntry{AlarmName: n, Type: historyConfigUpdate,
			AtMs: now.UnixMilli(), Summary: "Alarm actions " + verb}); err != nil {
			return nil, awshttp.AsAPIError(err)
		}
	}
	return nil, nil
}

func (s *Server) describeAlarmHistory(req *request) (any, *awshttp.APIError) {
	var from, to time.Time
	if t, ok := req.params.Time("StartDate"); ok {
		from = t
	}
	if t, ok := req.params.Time("EndDate"); ok {
		to = t
	}
	limit := req.params.Int("MaxRecords", 100)
	entries, err := s.readHistory(req.params.Str("AlarmName"), from, to,
		req.params.Str("HistoryItemType"), limit)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := describeAlarmHistoryResult{AlarmHistoryItems: []historyView{}}

	// Two filters name history doze-aws never writes. Contributor history
	// belongs to Contributor Insights, and the only alarm type here is
	// MetricAlarm — so each answers an empty list rather than every entry.
	if req.params.Str("AlarmContributorId") != "" {
		return out, nil
	}
	if types := req.params.Strs("AlarmTypes"); len(types) > 0 && !slices.Contains(types, "MetricAlarm") {
		return out, nil
	}
	// readHistory answers newest first, which is AWS's default. Ascending is
	// the same list read the other way.
	if req.params.Str("ScanBy") == scanAscending {
		slices.Reverse(entries)
	}

	for _, h := range entries {
		out.AlarmHistoryItems = append(out.AlarmHistoryItems, historyView{
			AlarmName:       h.AlarmName,
			AlarmType:       "MetricAlarm",
			Timestamp:       time.UnixMilli(h.AtMs).UTC(),
			HistoryItemType: h.Type,
			HistorySummary:  h.Summary,
			HistoryData:     h.Data,
		})
	}
	return out, nil
}
