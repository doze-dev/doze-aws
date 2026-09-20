package console

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
)

// ---- EventBridge (JSON 1.1, AWSEvents) ----

type Bus struct {
	Name     string
	ARN      string
	Rules    int
	RuleList []Rule // the rules behind Rules — BuildGraph reuses them
}

type Rule struct {
	Name     string
	ARN      string
	Bus      string
	Pattern  string
	Schedule string
	State    string
	Targets  []Target
}

type Target struct {
	ID  string
	ARN string
}

func (b *backend) ListBuses(ctx context.Context) ([]Bus, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListEventBuses", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		EventBuses []struct {
			Name string `json:"Name"`
			Arn  string `json:"Arn"`
		} `json:"EventBuses"`
	}
	json.Unmarshal(body, &out)
	buses := make([]Bus, 0, len(out.EventBuses))
	for _, eb := range out.EventBuses {
		bus := Bus{Name: eb.Name, ARN: eb.Arn}
		if rules, err := b.ListRules(ctx, eb.Name); err == nil {
			bus.Rules = len(rules)
			bus.RuleList = rules
		}
		buses = append(buses, bus)
	}
	sort.Slice(buses, func(i, j int) bool {
		// default bus first, then alphabetical
		if buses[i].Name == "default" {
			return true
		}
		if buses[j].Name == "default" {
			return false
		}
		return buses[i].Name < buses[j].Name
	})
	return buses, nil
}

// CountBuses is the cheap cardinality probe: one ListEventBuses call, no
// per-bus rule fetches.
func (b *backend) CountBuses(ctx context.Context) (int, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListEventBuses", map[string]any{})
	if err != nil {
		return 0, err
	}
	var out struct {
		EventBuses []struct{} `json:"EventBuses"`
	}
	json.Unmarshal(body, &out)
	return len(out.EventBuses), nil
}

func (b *backend) CreateBus(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "CreateEventBus", map[string]any{"Name": name})
	return err
}

func (b *backend) DeleteBus(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "DeleteEventBus", map[string]any{"Name": name})
	return err
}

func (b *backend) ListRules(ctx context.Context, bus string) ([]Rule, error) {
	in := map[string]any{}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	body, err := b.json11(ctx, "AWSEvents", "ListRules", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Rules []struct {
			Name               string `json:"Name"`
			Arn                string `json:"Arn"`
			EventPattern       string `json:"EventPattern"`
			ScheduleExpression string `json:"ScheduleExpression"`
			State              string `json:"State"`
		} `json:"Rules"`
	}
	json.Unmarshal(body, &out)
	rules := make([]Rule, 0, len(out.Rules))
	for _, r := range out.Rules {
		rules = append(rules, Rule{
			Name: r.Name, ARN: r.Arn, Bus: bus,
			Pattern: prettyJSON(r.EventPattern), Schedule: r.ScheduleExpression, State: r.State,
		})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	return rules, nil
}

// SetRuleState enables or disables a rule.
func (b *backend) SetRuleState(ctx context.Context, bus, name string, enable bool) error {
	action := "DisableRule"
	if enable {
		action = "EnableRule"
	}
	in := map[string]any{"Name": name}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	_, err := b.json11(ctx, "AWSEvents", action, in)
	return err
}

// TestEventPattern asks the service whether an event matches a rule pattern —
// the same evaluator that routes real PutEvents traffic.
func (b *backend) TestEventPattern(ctx context.Context, pattern, event string) (bool, error) {
	body, err := b.json11(ctx, "AWSEvents", "TestEventPattern", map[string]any{
		"EventPattern": pattern, "Event": event,
	})
	if err != nil {
		return false, err
	}
	var out struct {
		Result bool `json:"Result"`
	}
	json.Unmarshal(body, &out)
	return out.Result, nil
}

// ruleTargets fetches only a rule's targets — BuildGraph needs the edges but
// not the DescribeRule half of GetRule.
func (b *backend) ruleTargets(ctx context.Context, bus, name string) []Target {
	in := map[string]any{"Rule": name}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	body, err := b.json11(ctx, "AWSEvents", "ListTargetsByRule", in)
	if err != nil {
		return nil
	}
	var out struct {
		Targets []struct {
			Id  string `json:"Id"`
			Arn string `json:"Arn"`
		} `json:"Targets"`
	}
	json.Unmarshal(body, &out)
	targets := make([]Target, 0, len(out.Targets))
	for _, t := range out.Targets {
		targets = append(targets, Target{ID: t.Id, ARN: t.Arn})
	}
	return targets
}

func (b *backend) GetRule(ctx context.Context, bus, name string) (*Rule, error) {
	in := map[string]any{"Name": name}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	body, err := b.json11(ctx, "AWSEvents", "DescribeRule", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Name               string `json:"Name"`
		Arn                string `json:"Arn"`
		EventPattern       string `json:"EventPattern"`
		ScheduleExpression string `json:"ScheduleExpression"`
		State              string `json:"State"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	r := &Rule{
		Name: out.Name, ARN: out.Arn, Bus: bus,
		Pattern: prettyJSON(out.EventPattern), Schedule: out.ScheduleExpression, State: out.State,
	}
	tin := map[string]any{"Rule": name}
	if bus != "" && bus != "default" {
		tin["EventBusName"] = bus
	}
	if tb, err := b.json11(ctx, "AWSEvents", "ListTargetsByRule", tin); err == nil {
		var tout struct {
			Targets []struct {
				Id  string `json:"Id"`
				Arn string `json:"Arn"`
			} `json:"Targets"`
		}
		json.Unmarshal(tb, &tout)
		for _, t := range tout.Targets {
			r.Targets = append(r.Targets, Target{ID: t.Id, ARN: t.Arn})
		}
	}
	return r, nil
}

func (b *backend) PutRule(ctx context.Context, bus, name, pattern, schedule string) error {
	in := map[string]any{"Name": name}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	if pattern != "" {
		in["EventPattern"] = pattern
	}
	if schedule != "" {
		in["ScheduleExpression"] = schedule
	}
	_, err := b.json11(ctx, "AWSEvents", "PutRule", in)
	return err
}

func (b *backend) DeleteRule(ctx context.Context, bus, name string) error {
	// Targets must be removed first, like AWS.
	if r, err := b.GetRule(ctx, bus, name); err == nil && len(r.Targets) > 0 {
		ids := make([]string, 0, len(r.Targets))
		for _, t := range r.Targets {
			ids = append(ids, t.ID)
		}
		b.RemoveTarget(ctx, bus, name, ids...)
	}
	in := map[string]any{"Name": name}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	_, err := b.json11(ctx, "AWSEvents", "DeleteRule", in)
	return err
}

func (b *backend) AddTarget(ctx context.Context, bus, rule, id, arn string) error {
	in := map[string]any{"Rule": rule, "Targets": []map[string]string{{"Id": id, "Arn": arn}}}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	_, err := b.json11(ctx, "AWSEvents", "PutTargets", in)
	return err
}

func (b *backend) RemoveTarget(ctx context.Context, bus, rule string, ids ...string) error {
	in := map[string]any{"Rule": rule, "Ids": ids}
	if bus != "" && bus != "default" {
		in["EventBusName"] = bus
	}
	_, err := b.json11(ctx, "AWSEvents", "RemoveTargets", in)
	return err
}

// PutTestEvent publishes one event to a bus and reports whether it failed.
func (b *backend) PutTestEvent(ctx context.Context, bus, source, detailType, detail string) error {
	entry := map[string]any{"Source": source, "DetailType": detailType, "Detail": detail}
	if bus != "" && bus != "default" {
		entry["EventBusName"] = bus
	}
	body, err := b.json11(ctx, "AWSEvents", "PutEvents", map[string]any{"Entries": []any{entry}})
	if err != nil {
		return err
	}
	var out struct {
		FailedEntryCount int `json:"FailedEntryCount"`
		Entries          []struct {
			ErrorMessage string `json:"ErrorMessage"`
		} `json:"Entries"`
	}
	json.Unmarshal(body, &out)
	if out.FailedEntryCount > 0 && len(out.Entries) > 0 {
		return &apiErr{status: 400, body: out.Entries[0].ErrorMessage}
	}
	return nil
}

// ---- Archives + replay ----

type EBArchive struct {
	Name      string
	ARN       string
	Events    int64
	Retention int
	State     string
	Created   string
	Pattern   string
}

type EBReplay struct {
	Name    string
	State   string
	Started string
}

// ListArchives returns the archives over one bus (by its ARN).
func (b *backend) ListArchives(ctx context.Context, busARN string) ([]EBArchive, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListArchives", map[string]any{"EventSourceArn": busARN})
	if err != nil {
		return nil, err
	}
	var out struct {
		Archives []struct {
			ArchiveName   string  `json:"ArchiveName"`
			State         string  `json:"State"`
			EventCount    int64   `json:"EventCount"`
			RetentionDays int     `json:"RetentionDays"`
			CreationTime  float64 `json:"CreationTime"`
		} `json:"Archives"`
	}
	json.Unmarshal(body, &out)
	arcs := make([]EBArchive, 0, len(out.Archives))
	for _, a := range out.Archives {
		arcs = append(arcs, EBArchive{
			Name: a.ArchiveName, Events: a.EventCount, Retention: a.RetentionDays,
			State: a.State, Created: epochToTime(a.CreationTime),
		})
	}
	sort.Slice(arcs, func(i, j int) bool { return arcs[i].Name < arcs[j].Name })
	return arcs, nil
}

// CreateArchive registers an archive over a bus, optionally pattern-filtered.
func (b *backend) CreateArchive(ctx context.Context, name, busARN, pattern string) error {
	in := map[string]any{"ArchiveName": name, "EventSourceArn": busARN}
	if pattern != "" {
		in["EventPattern"] = pattern
	}
	_, err := b.json11(ctx, "AWSEvents", "CreateArchive", in)
	return err
}

func (b *backend) DeleteArchive(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AWSEvents", "DeleteArchive", map[string]any{"ArchiveName": name})
	return err
}

// StartReplay replays an archive's events back onto its bus over a time window.
func (b *backend) StartReplay(ctx context.Context, name, archiveARN, busARN string, start, end int64) error {
	_, err := b.json11(ctx, "AWSEvents", "StartReplay", map[string]any{
		"ReplayName":     name,
		"EventSourceArn": archiveARN,
		"EventStartTime": start,
		"EventEndTime":   end,
		"Destination":    map[string]any{"Arn": busARN},
	})
	return err
}

// ListReplays returns recent replays, newest first.
func (b *backend) ListReplays(ctx context.Context) ([]EBReplay, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListReplays", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Replays []struct {
			ReplayName      string  `json:"ReplayName"`
			State           string  `json:"State"`
			ReplayStartTime float64 `json:"ReplayStartTime"`
		} `json:"Replays"`
	}
	json.Unmarshal(body, &out)
	reps := make([]EBReplay, 0, len(out.Replays))
	for _, r := range out.Replays {
		reps = append(reps, EBReplay{Name: r.ReplayName, State: r.State, Started: epochToTime(r.ReplayStartTime)})
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i].Name > reps[j].Name })
	return reps, nil
}

// archiveARN builds an archive ARN from its name.
func archiveARN(name string) string {
	return "arn:aws:events:us-east-1:000000000000:archive/" + name
}

// prettyJSON re-indents a compact JSON string for display; non-JSON passes through.
func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}

// The describe operations, and archive updating.
//
// ListArchives and ListReplays return a summary; the describes carry the fields
// that say WHY something is the shape it is — an archive's event pattern, a
// replay's window and where it stopped, a bus's policy. Those are the fields
// you want when the answer is "nothing was archived" or "the replay delivered
// nothing", and neither list call carries them.

// DescribeArchive adds the pattern and the state reason to what the list knows.
// The pattern is the whole explanation for an archive with zero events.
func (b *backend) DescribeArchive(ctx context.Context, name string) (EBArchive, string, error) {
	body, err := b.json11(ctx, "AWSEvents", "DescribeArchive", map[string]any{"ArchiveName": name})
	if err != nil {
		return EBArchive{}, "", err
	}
	var out struct {
		ArchiveName   string  `json:"ArchiveName"`
		ArchiveArn    string  `json:"ArchiveArn"`
		EventPattern  string  `json:"EventPattern"`
		State         string  `json:"State"`
		StateReason   string  `json:"StateReason"`
		EventCount    int64   `json:"EventCount"`
		RetentionDays int     `json:"RetentionDays"`
		CreationTime  float64 `json:"CreationTime"`
	}
	json.Unmarshal(body, &out)
	return EBArchive{
		Name: out.ArchiveName, ARN: out.ArchiveArn, Events: out.EventCount,
		Retention: out.RetentionDays, State: out.State, Pattern: out.EventPattern,
		Created: epochSecString(strconv.FormatInt(int64(out.CreationTime), 10)),
	}, out.StateReason, nil
}

// UpdateArchive changes an existing archive's pattern or retention. Without it
// an archive's filter is fixed at creation, and getting it wrong means deleting
// the archive — which discards everything it has already captured.
func (b *backend) UpdateArchive(ctx context.Context, name, pattern string, retentionDays int) error {
	in := map[string]any{"ArchiveName": name}
	if pattern != "" {
		in["EventPattern"] = pattern
	}
	if retentionDays > 0 {
		in["RetentionDays"] = retentionDays
	}
	_, err := b.json11(ctx, "AWSEvents", "UpdateArchive", in)
	return err
}

// DescribeReplay carries the window and the state reason — the fields that
// explain a replay that finished having delivered nothing, which the list's
// "COMPLETED" does not.
func (b *backend) DescribeReplay(ctx context.Context, name string) (map[string]string, error) {
	body, err := b.json11(ctx, "AWSEvents", "DescribeReplay", map[string]any{"ReplayName": name})
	if err != nil {
		return nil, err
	}
	var out struct {
		ReplayName      string  `json:"ReplayName"`
		State           string  `json:"State"`
		StateReason     string  `json:"StateReason"`
		EventSourceArn  string  `json:"EventSourceArn"`
		EventStartTime  float64 `json:"EventStartTime"`
		EventEndTime    float64 `json:"EventEndTime"`
		ReplayStartTime float64 `json:"ReplayStartTime"`
		ReplayEndTime   float64 `json:"ReplayEndTime"`
	}
	json.Unmarshal(body, &out)
	sec := func(f float64) string { return epochSecString(strconv.FormatInt(int64(f), 10)) }
	return map[string]string{
		"State": out.State, "Reason": out.StateReason, "Source": arnLeaf(out.EventSourceArn),
		"Window from": sec(out.EventStartTime), "Window to": sec(out.EventEndTime),
		"Replayed from": sec(out.ReplayStartTime), "Replayed to": sec(out.ReplayEndTime),
	}, nil
}

// DescribeEventBus carries the bus's resource policy, which nothing else shows.
func (b *backend) DescribeEventBus(ctx context.Context, name string) (map[string]string, error) {
	body, err := b.json11(ctx, "AWSEvents", "DescribeEventBus", map[string]any{"Name": name})
	if err != nil {
		return nil, err
	}
	var out struct {
		Name   string `json:"Name"`
		Arn    string `json:"Arn"`
		Policy string `json:"Policy"`
	}
	json.Unmarshal(body, &out)
	return map[string]string{"Name": out.Name, "Arn": out.Arn, "Policy": out.Policy}, nil
}

// RuleNamesByTarget is the reverse lookup: which rules fire into this ARN.
// The forward direction is a rule's target list; this is the question you
// actually ask, standing on a queue that is receiving something unexpected.
func (b *backend) RuleNamesByTarget(ctx context.Context, targetARN, busName string) ([]string, error) {
	body, err := b.json11(ctx, "AWSEvents", "ListRuleNamesByTarget", map[string]any{
		"TargetArn": targetARN, "EventBusName": busName,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		RuleNames []string `json:"RuleNames"`
	}
	json.Unmarshal(body, &out)
	return out.RuleNames, nil
}
