package eventbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/eventpattern"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

var handlers = map[string]handler{
	"PutEvents":             (*Server).putEvents,
	"PutRule":               (*Server).putRule,
	"DeleteRule":            (*Server).deleteRule,
	"DescribeRule":          (*Server).describeRule,
	"ListRules":             (*Server).listRules,
	"EnableRule":            (*Server).enableRule,
	"DisableRule":           (*Server).disableRule,
	"PutTargets":            (*Server).putTargets,
	"RemoveTargets":         (*Server).removeTargets,
	"ListTargetsByRule":     (*Server).listTargetsByRule,
	"ListRuleNamesByTarget": (*Server).listRuleNamesByTarget,
	"CreateEventBus":        (*Server).createEventBus,
	"DeleteEventBus":        (*Server).deleteEventBus,
	"DescribeEventBus":      (*Server).describeEventBus,
	"ListEventBuses":        (*Server).listEventBuses,
	"TestEventPattern":      (*Server).testEventPattern,
	"TagResource":           (*Server).tagResource,
	"UntagResource":         (*Server).untagResource,
	"ListTagsForResource":   (*Server).listTagsForResource,
	"CreateArchive":         (*Server).createArchive,
	"DescribeArchive":       (*Server).describeArchive,
	"ListArchives":          (*Server).listArchives,
	"UpdateArchive":         (*Server).updateArchive,
	"DeleteArchive":         (*Server).deleteArchive,
	"StartReplay":           (*Server).startReplay,
	"DescribeReplay":        (*Server).describeReplay,
	"ListReplays":           (*Server).listReplays,
	"CancelReplay":          (*Server).cancelReplay,
}

// ---- param helpers ----

func pstr(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}

func busOrDefault(p map[string]any) string {
	if b := pstr(p, "EventBusName"); b != "" {
		// Accept both names and ARNs.
		if i := strings.LastIndex(b, "/"); i >= 0 && strings.HasPrefix(b, "arn:") {
			return b[i+1:]
		}
		return b
	}
	return DefaultBus
}

// ---- events ----

func (s *Server) putEvents(p map[string]any) (any, *awshttp.APIError) {
	entriesRaw, ok := p["Entries"].([]any)
	if !ok || len(entriesRaw) == 0 || len(entriesRaw) > 10 {
		return nil, awshttp.Errf(400, "ValidationException", "PutEvents accepts 1-10 entries")
	}
	results := make([]map[string]any, 0, len(entriesRaw))
	failed := 0
	for _, e := range entriesRaw {
		entry, _ := e.(map[string]any)
		res := s.putOneEvent(entry)
		if _, isErr := res["ErrorCode"]; isErr {
			failed++
		}
		results = append(results, res)
	}
	return map[string]any{"Entries": results, "FailedEntryCount": failed}, nil
}

// putOneEvent validates an entry, matches enabled rules, dispatches targets.
func (s *Server) putOneEvent(entry map[string]any) map[string]any {
	source := pstr(entry, "Source")
	detailType := pstr(entry, "DetailType")
	detail := pstr(entry, "Detail")
	if source == "" || detailType == "" {
		return map[string]any{"ErrorCode": "ValidationException", "ErrorMessage": "Source and DetailType are required"}
	}
	if detail == "" {
		detail = "{}"
	}
	if !json.Valid([]byte(detail)) {
		return map[string]any{"ErrorCode": "MalformedDetail", "ErrorMessage": "Detail must be valid JSON"}
	}
	bus := DefaultBus
	if b := pstr(entry, "EventBusName"); b != "" {
		bus = b
	}

	eventID := awshttp.RequestID()
	// The full event document rules match against.
	doc := map[string]any{
		"version":     "0",
		"id":          eventID,
		"detail-type": detailType,
		"source":      source,
		"account":     awsident.AccountID,
		"time":        awshttp.ISO8601(s.now()),
		"region":      awsident.Region,
		"resources":   entry["Resources"],
		"detail":      json.RawMessage(detail),
	}
	if doc["resources"] == nil {
		doc["resources"] = []string{}
	}
	eventJSON, err := json.Marshal(doc)
	if err != nil {
		return map[string]any{"ErrorCode": "InternalFailure", "ErrorMessage": err.Error()}
	}

	s.matchAndDispatch(bus, eventJSON, nil)
	s.captureToArchives(bus, eventJSON)
	return map[string]any{"EventId": eventID}
}

// matchAndDispatch delivers an event to every enabled pattern rule on a bus that
// matches it. filter, when non-nil, restricts delivery to rules whose ARN is in
// the set (used by StartReplay); nil means all rules.
func (s *Server) matchAndDispatch(bus string, eventJSON []byte, filter map[string]bool) {
	rules, rerr := s.store.Rules(bus, "")
	if rerr != nil {
		s.logf("eventbridge: rules(%s): %v", bus, rerr)
		return
	}
	for _, rule := range rules {
		if rule.State != "ENABLED" || rule.Pattern == "" {
			continue
		}
		if filter != nil && !filter[rule.ARN()] {
			continue
		}
		pat, perr := eventpattern.Parse([]byte(rule.Pattern))
		if perr != nil {
			s.logf("eventbridge: rule %s has an unparseable pattern: %v", rule.Name, perr)
			continue
		}
		matched, merr := pat.Match(eventJSON)
		if merr != nil || !matched {
			continue
		}
		for _, target := range rule.Targets {
			s.dispatch(rule, target, eventJSON)
		}
	}
}

// dispatch delivers one matched event to one target, applying input shaping.
func (s *Server) dispatch(rule Rule, target Target, eventJSON []byte) {
	payload, err := shapeInput(target, eventJSON)
	if err != nil {
		s.logf("eventbridge: rule %s target %s input shaping: %v", rule.Name, target.ID, err)
		return
	}
	switch {
	case strings.Contains(target.ARN, ":sqs:"):
		queue := target.ARN[strings.LastIndex(target.ARN, ":")+1:]
		if err := peercall.SQSSend(s.peers, queue, string(payload), nil); err != nil {
			s.logf("eventbridge: rule %s -> sqs %s: %v", rule.Name, queue, err)
		}
	case strings.Contains(target.ARN, ":lambda:"):
		fn := target.ARN[strings.LastIndex(target.ARN, ":")+1:]
		if err := peercall.LambdaInvokeAsync(s.peers, fn, payload); err != nil {
			s.logf("eventbridge: rule %s -> lambda %s: %v", rule.Name, fn, err)
		}
	case strings.Contains(target.ARN, ":sns:"):
		if err := peercall.SNSPublish(s.peers, target.ARN, string(payload)); err != nil {
			s.logf("eventbridge: rule %s -> sns: %v", rule.Name, err)
		}
	default:
		s.logf("eventbridge: rule %s target %s: unsupported target service in %s (sqs, lambda, sns supported)",
			rule.Name, target.ID, target.ARN)
	}
}

// shapeInput applies Input / InputPath / InputTransformer.
func shapeInput(target Target, eventJSON []byte) ([]byte, error) {
	switch {
	case target.Input != "":
		return []byte(target.Input), nil
	case target.InputPath != "":
		v, ok := extractPath(eventJSON, target.InputPath)
		if !ok {
			return nil, fmt.Errorf("InputPath %s matched nothing", target.InputPath)
		}
		return json.Marshal(v)
	case target.InputTransformer != nil:
		out := target.InputTransformer.Template
		for name, path := range target.InputTransformer.PathsMap {
			v, ok := extractPath(eventJSON, path)
			if !ok {
				v = ""
			}
			out = strings.ReplaceAll(out, "<"+name+">", renderScalar(v))
		}
		return []byte(out), nil
	}
	return eventJSON, nil
}

// extractPath resolves a $.a.b JSONPath-lite expression.
func extractPath(eventJSON []byte, path string) (any, bool) {
	var doc any
	if json.Unmarshal(eventJSON, &doc) != nil {
		return nil, false
	}
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return doc, true
	}
	cur := doc
	for part := range strings.SplitSeq(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// renderScalar renders a value into an InputTemplate placeholder the way
// EventBridge does: strings unquoted, everything else as JSON.
func renderScalar(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// ---- rules ----

func (s *Server) putRule(p map[string]any) (any, *awshttp.APIError) {
	name := pstr(p, "Name")
	if name == "" {
		return nil, awshttp.Errf(400, "ValidationException", "Name is required")
	}
	pattern := pstr(p, "EventPattern")
	schedule := pstr(p, "ScheduleExpression")
	if pattern == "" && schedule == "" {
		return nil, awshttp.Errf(400, "ValidationException", "either EventPattern or ScheduleExpression is required")
	}
	if pattern != "" {
		if _, err := eventpattern.Parse([]byte(pattern)); err != nil {
			return nil, awshttp.Errf(400, "InvalidEventPatternException", "%v", err)
		}
	}
	if schedule != "" {
		// rate(...) is driven by the local ticker; cron(...) is accepted and
		// stored but not driven (a wall-clock cron isn't useful in an ephemeral
		// local stack). Anything else is malformed.
		if _, ok := parseRate(schedule); !ok && !strings.HasPrefix(strings.TrimSpace(schedule), "cron(") {
			return nil, awshttp.Errf(400, "ValidationException",
				"ScheduleExpression %q is not a valid rate(...) or cron(...) expression", schedule)
		}
	}
	state := pstr(p, "State")
	if state == "" {
		state = "ENABLED"
	}
	r := Rule{
		Bus: busOrDefault(p), Name: name, Pattern: pattern, Schedule: schedule,
		State: state, Desc: pstr(p, "Description"),
	}
	if err := s.store.PutRule(r); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"RuleArn": r.ARN()}, nil
}

func (s *Server) deleteRule(p map[string]any) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteRule(busOrDefault(p), pstr(p, "Name")))
}

func ruleView(r *Rule) map[string]any {
	out := map[string]any{
		"Name":         r.Name,
		"Arn":          r.ARN(),
		"State":        r.State,
		"EventBusName": r.Bus,
	}
	if r.Pattern != "" {
		out["EventPattern"] = r.Pattern
	}
	if r.Schedule != "" {
		out["ScheduleExpression"] = r.Schedule
	}
	if r.Desc != "" {
		out["Description"] = r.Desc
	}
	return out
}

func (s *Server) describeRule(p map[string]any) (any, *awshttp.APIError) {
	r, err := s.store.GetRule(busOrDefault(p), pstr(p, "Name"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return ruleView(r), nil
}

func (s *Server) listRules(p map[string]any) (any, *awshttp.APIError) {
	rules, err := s.store.Rules(busOrDefault(p), pstr(p, "NamePrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := []map[string]any{}
	for i := range rules {
		views = append(views, ruleView(&rules[i]))
	}
	return map[string]any{"Rules": views}, nil
}

func (s *Server) enableRule(p map[string]any) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.UpdateRule(busOrDefault(p), pstr(p, "Name"), func(r *Rule) error {
		r.State = "ENABLED"
		return nil
	}))
}

func (s *Server) disableRule(p map[string]any) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.UpdateRule(busOrDefault(p), pstr(p, "Name"), func(r *Rule) error {
		r.State = "DISABLED"
		return nil
	}))
}

// ---- targets ----

func (s *Server) putTargets(p map[string]any) (any, *awshttp.APIError) {
	targetsRaw, _ := p["Targets"].([]any)
	if len(targetsRaw) == 0 {
		return nil, awshttp.Errf(400, "ValidationException", "Targets is required")
	}
	var targets []Target
	for _, tr := range targetsRaw {
		tm, _ := tr.(map[string]any)
		t := Target{
			ID:        pstr(tm, "Id"),
			ARN:       pstr(tm, "Arn"),
			Input:     pstr(tm, "Input"),
			InputPath: pstr(tm, "InputPath"),
		}
		if it, ok := tm["InputTransformer"].(map[string]any); ok {
			trans := &InputTransformer{Template: pstr(it, "InputTemplate"), PathsMap: map[string]string{}}
			if pm, ok := it["InputPathsMap"].(map[string]any); ok {
				for k, v := range pm {
					if sv, ok := v.(string); ok {
						trans.PathsMap[k] = sv
					}
				}
			}
			t.InputTransformer = trans
		}
		if t.ID == "" || t.ARN == "" {
			return nil, awshttp.Errf(400, "ValidationException", "each target needs Id and Arn")
		}
		targets = append(targets, t)
	}
	err := s.store.UpdateRule(busOrDefault(p), pstr(p, "Rule"), func(r *Rule) error {
		for _, nt := range targets {
			replaced := false
			for i := range r.Targets {
				if r.Targets[i].ID == nt.ID {
					r.Targets[i] = nt
					replaced = true
					break
				}
			}
			if !replaced {
				r.Targets = append(r.Targets, nt)
			}
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"FailedEntryCount": 0, "FailedEntries": []any{}}, nil
}

func (s *Server) removeTargets(p map[string]any) (any, *awshttp.APIError) {
	idsRaw, _ := p["Ids"].([]any)
	err := s.store.UpdateRule(busOrDefault(p), pstr(p, "Rule"), func(r *Rule) error {
		for _, idAny := range idsRaw {
			id, _ := idAny.(string)
			for i := range r.Targets {
				if r.Targets[i].ID == id {
					r.Targets = append(r.Targets[:i], r.Targets[i+1:]...)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"FailedEntryCount": 0, "FailedEntries": []any{}}, nil
}

func (s *Server) listTargetsByRule(p map[string]any) (any, *awshttp.APIError) {
	r, err := s.store.GetRule(busOrDefault(p), pstr(p, "Rule"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	targets := []map[string]any{}
	for _, t := range r.Targets {
		tv := map[string]any{"Id": t.ID, "Arn": t.ARN}
		if t.Input != "" {
			tv["Input"] = t.Input
		}
		if t.InputPath != "" {
			tv["InputPath"] = t.InputPath
		}
		if t.InputTransformer != nil {
			tv["InputTransformer"] = map[string]any{
				"InputTemplate": t.InputTransformer.Template,
				"InputPathsMap": t.InputTransformer.PathsMap,
			}
		}
		targets = append(targets, tv)
	}
	return map[string]any{"Targets": targets}, nil
}

func (s *Server) listRuleNamesByTarget(p map[string]any) (any, *awshttp.APIError) {
	arn := pstr(p, "TargetArn")
	rules, err := s.store.Rules(busOrDefault(p), "")
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	names := []string{}
	for _, r := range rules {
		for _, t := range r.Targets {
			if t.ARN == arn {
				names = append(names, r.Name)
				break
			}
		}
	}
	return map[string]any{"RuleNames": names}, nil
}

// ---- buses ----

func (s *Server) createEventBus(p map[string]any) (any, *awshttp.APIError) {
	name := pstr(p, "Name")
	if err := s.store.CreateBus(name, nil); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"EventBusArn": busARN(name)}, nil
}

func (s *Server) deleteEventBus(p map[string]any) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteBus(pstr(p, "Name")))
}

func (s *Server) describeEventBus(p map[string]any) (any, *awshttp.APIError) {
	name := pstr(p, "Name")
	if name == "" {
		name = DefaultBus
	}
	buses, err := s.store.ListBuses()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	for _, b := range buses {
		if b.Name == name {
			return map[string]any{"Name": b.Name, "Arn": busARN(b.Name)}, nil
		}
	}
	return nil, awshttp.Errf(400, "ResourceNotFoundException", "event bus %s does not exist", name)
}

func (s *Server) listEventBuses(p map[string]any) (any, *awshttp.APIError) {
	buses, err := s.store.ListBuses()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := []map[string]any{}
	for _, b := range buses {
		views = append(views, map[string]any{"Name": b.Name, "Arn": busARN(b.Name)})
	}
	return map[string]any{"EventBuses": views}, nil
}

// ---- misc ----

func (s *Server) testEventPattern(p map[string]any) (any, *awshttp.APIError) {
	pattern := pstr(p, "EventPattern")
	event := pstr(p, "Event")
	pat, err := eventpattern.Parse([]byte(pattern))
	if err != nil {
		return nil, awshttp.Errf(400, "InvalidEventPatternException", "%v", err)
	}
	matched, merr := pat.Match([]byte(event))
	if merr != nil {
		return nil, awshttp.Errf(400, "ValidationException", "%v", merr)
	}
	return map[string]any{"Result": matched}, nil
}

// Tags apply to rules (by ARN); buses share the mechanism.
func (s *Server) tagResource(p map[string]any) (any, *awshttp.APIError) {
	bus, name, aerr := ruleFromARN(pstr(p, "ResourceARN"))
	if aerr != nil {
		return nil, aerr
	}
	tagsRaw, _ := p["Tags"].([]any)
	return nil, awshttp.AsAPIErrorOrNil(s.store.UpdateRule(bus, name, func(r *Rule) error {
		for _, tr := range tagsRaw {
			tm, _ := tr.(map[string]any)
			k, _ := tm["Key"].(string)
			v, _ := tm["Value"].(string)
			if k != "" {
				if r.Tags == nil {
					r.Tags = map[string]string{}
				}
				r.Tags[k] = v
			}
		}
		return nil
	}))
}

func (s *Server) untagResource(p map[string]any) (any, *awshttp.APIError) {
	bus, name, aerr := ruleFromARN(pstr(p, "ResourceARN"))
	if aerr != nil {
		return nil, aerr
	}
	keysRaw, _ := p["TagKeys"].([]any)
	return nil, awshttp.AsAPIErrorOrNil(s.store.UpdateRule(bus, name, func(r *Rule) error {
		for _, kAny := range keysRaw {
			if k, ok := kAny.(string); ok {
				delete(r.Tags, k)
			}
		}
		return nil
	}))
}

func (s *Server) listTagsForResource(p map[string]any) (any, *awshttp.APIError) {
	bus, name, aerr := ruleFromARN(pstr(p, "ResourceARN"))
	if aerr != nil {
		return nil, aerr
	}
	r, err := s.store.GetRule(bus, name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	tags := []map[string]string{}
	for k, v := range r.Tags {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	return map[string]any{"Tags": tags}, nil
}

// ruleFromARN parses arn:aws:events:...:rule/[bus/]name.
func ruleFromARN(arn string) (bus, name string, aerr *awshttp.APIError) {
	i := strings.Index(arn, ":rule/")
	if i < 0 {
		return "", "", awshttp.Errf(400, "ValidationException", "%q is not a rule ARN", arn)
	}
	rest := arn[i+len(":rule/"):]
	if b, n, ok := strings.Cut(rest, "/"); ok {
		return b, n, nil
	}
	return DefaultBus, rest, nil
}
