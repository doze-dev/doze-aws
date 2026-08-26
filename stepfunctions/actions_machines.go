package stepfunctions

import (
	"encoding/json"
	"regexp"
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

func (s *Server) createStateMachine(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "name")
	if aerr := checkName(name); aerr != nil {
		return nil, aerr
	}
	definition := awsjson.Str(p, "definition")

	// The whole reason to validate locally: a definition AWS would refuse is
	// refused here, with the same diagnostics, before it reaches a deploy.
	if _, rep := asl.ValidateDefinition([]byte(definition)); !rep.OK() {
		return nil, errInvalidDefinition(rep.Error())
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
	return map[string]any{
		"stateMachineArn": stored.ARN,
		"creationDate":    epoch(stored.CreatedAt),
	}, nil
}

func (s *Server) describeStateMachine(p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name := nameFromARN(arn, "stateMachine")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	m, aerr := s.store.GetMachine(name)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, errMachineNotFound(arn)
	}
	out := map[string]any{
		"stateMachineArn":        m.ARN,
		"name":                   m.Name,
		"definition":             m.Definition,
		"roleArn":                m.RoleARN,
		"type":                   m.Type,
		"status":                 m.Status,
		"creationDate":           epoch(m.CreatedAt),
		"revisionId":             m.RevisionID,
		"stateMachineRevisionId": m.RevisionID,
	}
	putRaw(out, "loggingConfiguration", m.LoggingConfiguration)
	putRaw(out, "tracingConfiguration", m.TracingConfiguration)
	putRaw(out, "encryptionConfiguration", m.EncryptionConfiguration)
	return out, nil
}

func (s *Server) updateStateMachine(p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name := nameFromARN(arn, "stateMachine")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	var definition, roleARN *string
	if raw, ok := p["definition"].(string); ok {
		if _, rep := asl.ValidateDefinition([]byte(raw)); !rep.OK() {
			return nil, errInvalidDefinition(rep.Error())
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
	return map[string]any{
		"updateDate":             epoch(s.store.now()),
		"revisionId":             m.RevisionID,
		"stateMachineRevisionId": m.RevisionID,
	}, nil
}

func (s *Server) deleteStateMachine(p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	name := nameFromARN(arn, "stateMachine")
	if name == "" {
		return nil, errInvalidARN(arn)
	}
	// AWS deletes idempotently: removing one that is already gone succeeds, so
	// a repeated `cdk destroy` does not fail on the second run.
	if aerr := s.store.DeleteMachine(name); aerr != nil {
		return nil, aerr
	}
	return map[string]any{}, nil
}

func (s *Server) listStateMachines(p map[string]any) (any, *awshttp.APIError) {
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
	return map[string]any{"stateMachines": items}, nil
}

// validateDefinition backs ValidateStateMachineDefinition, which is the
// analyser exposed directly. It is free once the analyser exists, and it is the
// operation the CDK and the CLI both use to check a definition without creating
// anything.
func (s *Server) validateDefinition(p map[string]any) (any, *awshttp.APIError) {
	_, rep := asl.ValidateDefinition([]byte(awsjson.Str(p, "definition")))
	if rep.OK() {
		return map[string]any{"result": "OK", "diagnostics": []any{}}, nil
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
	return map[string]any{"result": "FAIL", "diagnostics": diags}, nil
}

func (s *Server) createActivity(p map[string]any) (any, *awshttp.APIError) {
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

func (s *Server) describeActivity(p map[string]any) (any, *awshttp.APIError) {
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

func (s *Server) deleteActivity(p map[string]any) (any, *awshttp.APIError) {
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

func (s *Server) listActivities(p map[string]any) (any, *awshttp.APIError) {
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
	return map[string]any{"activities": items}, nil
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
