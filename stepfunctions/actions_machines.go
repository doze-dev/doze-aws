package stepfunctions

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/asl"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// The dispatch table and the state-machine and activity handlers.
//
// Note the lowercase JSON member names — `stateMachineArn`, not
// `StateMachineArn`. Step Functions is the only service in this repo that
// spells them that way, and getting it wrong produces a request that decodes to
// an empty struct and fails as "required member missing" while looking correct.
var handlers = map[string]handler{
	"CreateStateMachine":             (*Server).createStateMachine,
	"DescribeStateMachine":           (*Server).describeStateMachine,
	"UpdateStateMachine":             (*Server).updateStateMachine,
	"DeleteStateMachine":             (*Server).deleteStateMachine,
	"ListStateMachines":              (*Server).listStateMachines,
	"ValidateStateMachineDefinition": (*Server).validateDefinition,

	"CreateActivity":   (*Server).createActivity,
	"DescribeActivity": (*Server).describeActivity,
	"DeleteActivity":   (*Server).deleteActivity,
	"ListActivities":   (*Server).listActivities,

	"TagResource":         (*Server).tagResource,
	"UntagResource":       (*Server).untagResource,
	"ListTagsForResource": (*Server).listTagsForResource,

	"StartExecution":                   (*Server).startExecution,
	"DescribeExecution":                (*Server).describeExecution,
	"StopExecution":                    (*Server).stopExecution,
	"ListExecutions":                   (*Server).listExecutions,
	"DescribeStateMachineForExecution": (*Server).describeStateMachineForExecution,
	"GetExecutionHistory":              (*Server).getExecutionHistory,

	"SendTaskSuccess":   (*Server).sendTaskSuccess,
	"SendTaskFailure":   (*Server).sendTaskFailure,
	"SendTaskHeartbeat": (*Server).sendTaskHeartbeat,
	"GetActivityTask":   (*Server).getActivityTask,

	"StartSyncExecution": (*Server).startSyncExecution,
	"TestState":          (*Server).testState,
	"RedriveExecution":   (*Server).redriveExecution,

	"DescribeMapRun": (*Server).describeMapRun,
	"ListMapRuns":    (*Server).listMapRuns,
	"UpdateMapRun":   (*Server).updateMapRun,

	"PublishStateMachineVersion": (*Server).publishStateMachineVersion,
	"DeleteStateMachineVersion":  (*Server).deleteStateMachineVersion,
	"ListStateMachineVersions":   (*Server).listStateMachineVersions,
	"CreateStateMachineAlias":    (*Server).createStateMachineAlias,
	"DescribeStateMachineAlias":  (*Server).describeStateMachineAlias,
	"UpdateStateMachineAlias":    (*Server).updateStateMachineAlias,
	"DeleteStateMachineAlias":    (*Server).deleteStateMachineAlias,
	"ListStateMachineAliases":    (*Server).listStateMachineAliases,
}

// nameRule is the character set AWS allows in a state machine or activity name.
// It excludes whitespace, control characters, the wildcard characters, and the
// bracket and quote family — the same rule the console shows.
var nameRule = regexp.MustCompile(`^[^\s<>{}\[\]?*"#%\\^|~` + "`" + `$&,;:/\x00-\x1f\x7f-\x9f]+$`)

func checkName(name string) *awshttp.APIError {
	switch {
	case name == "":
		return errInvalidName("must not be empty")
	case len(name) > 80:
		return errInvalidName("must be 80 characters or fewer")
	case !nameRule.MatchString(name):
		return errInvalidName("'" + name + "' contains a character that is not allowed")
	}
	return nil
}

func machineARN(name string) string  { return awsident.ARN("states", "stateMachine:"+name) }
func activityARN(name string) string { return awsident.ARN("states", "activity:"+name) }

// nameFromARN pulls the resource name out of an ARN of the shape
// arn:aws:states:<region>:<account>:<kind>:<name>. Returns "" for anything that
// is not one, so the caller can answer InvalidArn rather than looking up "".
func nameFromARN(arn, kind string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 7 || parts[2] != "states" || parts[5] != kind {
		return ""
	}
	return strings.Join(parts[6:], ":")
}

func (s *Server) createStateMachine(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "name")
	if aerr := checkName(name); aerr != nil {
		return nil, aerr
	}
	definition := awsjson.Str(p, "definition")

	// The whole reason to validate locally: a definition AWS would refuse is
	// refused here, with the same diagnostics, before it reaches a deploy.
	d, rep := asl.ValidateDefinition([]byte(definition))
	if !rep.OK() {
		return nil, errInvalidDefinition(rep.Error())
	}
	if aerr := refuseUnrunnable(d); aerr != nil {
		return nil, aerr
	}

	publish, versionDescription, aerr := publishParams(p)
	if aerr != nil {
		return nil, aerr
	}
	typ := awsjson.Str(p, "type")
	if typ == "" {
		typ = "STANDARD"
	}
	if typ != "STANDARD" && typ != "EXPRESS" {
		return nil, errValidation("type must be STANDARD or EXPRESS, got %q", typ)
	}

	m := &StateMachine{
		Name: name, ARN: machineARN(name), Definition: definition,
		RoleARN: awsjson.Str(p, "roleArn"), Type: typ,
		LoggingConfiguration:    rawOf(p, "loggingConfiguration"),
		TracingConfiguration:    rawOf(p, "tracingConfiguration"),
		EncryptionConfiguration: rawOf(p, "encryptionConfiguration"),
	}
	stored, aerr := s.store.PutMachine(m)
	if aerr != nil {
		return nil, aerr
	}
	if tags := tagsOf(p); len(tags) > 0 {
		if err := s.store.setTags(stored.ARN, tags); err != nil {
			return nil, asAPIError(err)
		}
	}
	out := map[string]any{
		"stateMachineArn": stored.ARN,
		"creationDate":    epoch(stored.CreatedAt),
	}
	if publish {
		if aerr := s.publishInto(out, stored, versionDescription); aerr != nil {
			return nil, aerr
		}
	}
	return out, nil
}

func (s *Server) describeStateMachine(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name, qualifier, ok := splitMachineARN(arn)
	if !ok {
		return nil, errInvalidARN(arn)
	}
	if qualifier != "" {
		return s.describeQualified(arn, name, qualifier)
	}
	m, aerr := s.store.GetMachine(name)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, errMachineNotFound(arn)
	}
	out := map[string]any{
		"stateMachineArn": m.ARN,
		"name":            m.Name,
		"definition":      m.Definition,
		"roleArn":         m.RoleARN,
		"type":            m.Type,
		"status":          m.Status,
		"creationDate":    epoch(m.CreatedAt),
		"revisionId":      m.RevisionID,
	}
	putConfigs(out, m)
	return out, nil
}

// putConfigs answers the three configuration blocks the way AWS does: what
// the caller stored, or AWS's defaults when they passed none — logging off,
// tracing disabled, an AWS-owned key. A describe with the blocks missing
// reads as a different machine to a tool that diffs against them.
func putConfigs(out map[string]any, m *StateMachine) {
	putRawDefault(out, "loggingConfiguration", m.LoggingConfiguration, map[string]any{"level": "OFF", "includeExecutionData": false})
	putRawDefault(out, "tracingConfiguration", m.TracingConfiguration, map[string]any{"enabled": false})
	putRawDefault(out, "encryptionConfiguration", m.EncryptionConfiguration, map[string]any{"type": "AWS_OWNED_KEY"})
}

func (s *Server) updateStateMachine(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name, aerr := unqualifiedMachine(arn)
	if aerr != nil {
		return nil, aerr
	}
	publish, versionDescription, aerr := publishParams(p)
	if aerr != nil {
		return nil, aerr
	}
	var definition, roleARN *string
	if raw, ok := p["definition"].(string); ok {
		d, rep := asl.ValidateDefinition([]byte(raw))
		if !rep.OK() {
			return nil, errInvalidDefinition(rep.Error())
		}
		if aerr := refuseUnrunnable(d); aerr != nil {
			return nil, aerr
		}
		definition = &raw
	}
	if raw, ok := p["roleArn"].(string); ok {
		roleARN = &raw
	}
	m, aerr := s.store.UpdateMachine(name, definition, roleARN)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, errMachineNotFound(arn)
	}
	out := map[string]any{
		"updateDate": epoch(s.store.now()),
		"revisionId": m.RevisionID,
	}
	if publish {
		if aerr := s.publishInto(out, m, versionDescription); aerr != nil {
			return nil, aerr
		}
	}
	return out, nil
}

func (s *Server) deleteStateMachine(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name, aerr := unqualifiedMachine(arn)
	if aerr != nil {
		return nil, aerr
	}
	// AWS deletes idempotently: removing one that is already gone succeeds, so
	// a repeated `cdk destroy` does not fail on the second run.
	if aerr := s.store.DeleteMachine(name); aerr != nil {
		return nil, aerr
	}
	// AWS deletes a machine's versions and aliases with it.
	if aerr := s.store.DeleteVersionsAndAliases(name); aerr != nil {
		return nil, aerr
	}
	return map[string]any{}, nil
}

func (s *Server) listStateMachines(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	machines, aerr := s.store.ListMachines()
	if aerr != nil {
		return nil, aerr
	}
	items := make([]any, 0, len(machines))
	for _, m := range machines {
		items = append(items, map[string]any{
			"stateMachineArn": m.ARN,
			"name":            m.Name,
			"type":            m.Type,
			"creationDate":    epoch(m.CreatedAt),
		})
	}
	return page(p, "stateMachines", items)
}

// page applies maxResults and nextToken to a fully built list, answering the
// items under key plus a nextToken when more remain. The token is the offset
// of the next item, which is opaque enough for a local list and lets a bad
// one be refused as InvalidToken the way AWS refuses a stale cursor. Before
// this the three list operations ignored both parameters, and a caller
// paging with maxResults=1 got everything in one answer — which an SDK
// paginator tolerates and a test written against AWS does not.
func page(p map[string]any, key string, items []any) (any, *awshttp.APIError) {
	limit := awsjson.Int(p, "maxResults", 0)
	if limit < 0 || limit > 1000 {
		limit = 0
	}
	start := 0
	if tok := awsjson.Str(p, "nextToken"); tok != "" {
		n, err := strconv.Atoi(tok)
		if err != nil || n < 0 || n > len(items) {
			return nil, awshttp.Errf(400, "InvalidToken", "Invalid Token: '%s'", tok)
		}
		start = n
	}
	items = items[start:]
	out := map[string]any{key: items}
	if limit > 0 && len(items) > limit {
		out[key] = items[:limit]
		out["nextToken"] = strconv.Itoa(start + limit)
	}
	return out, nil
}

// validateDefinition backs ValidateStateMachineDefinition, which is the
// analyser exposed directly. It is free once the analyser exists, and it is the
// operation the CDK and the CLI both use to check a definition without creating
// anything.
func (s *Server) validateDefinition(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	_, rep := asl.ValidateDefinition([]byte(awsjson.Str(p, "definition")))
	if rep.OK() {
		return map[string]any{"result": "OK", "diagnostics": []any{}, "truncated": false}, nil
	}
	diags := make([]any, 0, len(rep.Diagnostics))
	for _, d := range rep.Diagnostics {
		diags = append(diags, map[string]any{
			"severity": "ERROR",
			"code":     "SCHEMA_VALIDATION_FAILED",
			"message":  d.Message,
			"location": d.Where,
		})
	}
	return map[string]any{"result": "FAIL", "diagnostics": diags, "truncated": false}, nil
}

func (s *Server) createActivity(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "name")
	if aerr := checkName(name); aerr != nil {
		return nil, aerr
	}
	a, aerr := s.store.PutActivity(&Activity{Name: name, ARN: activityARN(name)})
	if aerr != nil {
		return nil, aerr
	}
	if tags := tagsOf(p); len(tags) > 0 {
		if err := s.store.setTags(a.ARN, tags); err != nil {
			return nil, asAPIError(err)
		}
	}
	return map[string]any{"activityArn": a.ARN, "creationDate": epoch(a.CreatedAt)}, nil
}

func (s *Server) describeActivity(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "activityArn")
	name := nameFromARN(arn, "activity")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	a, aerr := s.store.GetActivity(name)
	if aerr != nil {
		return nil, aerr
	}
	if a == nil {
		return nil, errActivityNotFound(arn)
	}
	return map[string]any{"activityArn": a.ARN, "name": a.Name, "creationDate": epoch(a.CreatedAt)}, nil
}

func (s *Server) deleteActivity(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "activityArn")
	name := nameFromARN(arn, "activity")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	if aerr := s.store.DeleteActivity(name); aerr != nil {
		return nil, aerr
	}
	return map[string]any{}, nil
}

func (s *Server) listActivities(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	acts, aerr := s.store.ListActivities()
	if aerr != nil {
		return nil, aerr
	}
	items := make([]any, 0, len(acts))
	for _, a := range acts {
		items = append(items, map[string]any{
			"activityArn": a.ARN, "name": a.Name, "creationDate": epoch(a.CreatedAt),
		})
	}
	return page(p, "activities", items)
}

// epoch renders a stored millisecond timestamp the way awsJson expects a
// Smithy timestamp: epoch seconds with a fractional part.
func epoch(ms int64) float64 { return float64(ms) / 1000 }

func rawOf(p map[string]any, key string) json.RawMessage {
	v, ok := p[key]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func putRaw(out map[string]any, key string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		out[key] = v
	}
}

// putRawDefault is putRaw with a value for when nothing was stored.
func putRawDefault(out map[string]any, key string, raw json.RawMessage, def any) {
	putRaw(out, key, raw)
	if _, ok := out[key]; !ok {
		out[key] = def
	}
}

// tagsOf reads the [{key,value}] list shape Step Functions uses, which is not
// the {k:v} map DynamoDB and Lambda use.
func tagsOf(p map[string]any) map[string]string {
	list, ok := p["tags"].([]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k, _ := entry["key"].(string)
		v, _ := entry["value"].(string)
		if k != "" {
			out[k] = v
		}
	}
	return out
}
