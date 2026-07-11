package console

import (
	"context"
	"encoding/json"
	"sort"
)

// ---- EventBridge (JSON 1.1, AWSEvents) ----

type Bus struct {
	Name  string
	ARN   string
	Rules int
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
