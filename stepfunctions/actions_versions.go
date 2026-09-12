package stepfunctions

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// State machine versions: PublishStateMachineVersion, DeleteStateMachineVersion,
// ListStateMachineVersions, plus the qualified-ARN plumbing the older
// handlers lean on — DescribeStateMachine on a version ARN, the publish flag
// on Create/UpdateStateMachine, and StartExecution resolving a version or
// alias ARN to the snapshot it runs.
//
// A qualified ARN is the machine ARN with one more colon-separated field:
// arn:aws:states:<r>:<a>:stateMachine:<name>:<qualifier>. A qualifier that is
// a positive integer names a version; anything else names an alias, which is
// why CreateStateMachineAlias refuses an integer name. Machine names cannot
// contain ":" (checkName), so the split is unambiguous.

func versionARN(id awsident.Identity, machine string, n int) string {
	return machineARN(id, machine) + ":" + strconv.Itoa(n)
}
func aliasARN(id awsident.Identity, machine, alias string) string {
	return machineARN(id, machine) + ":" + alias
}

// splitMachineARN pulls the machine name and the qualifier ("" when
// unqualified) out of a stateMachine ARN. ok is false for anything that is
// not one, so the caller answers InvalidArn rather than looking up "".
func splitMachineARN(arn string) (name, qualifier string, ok bool) {
	full := nameFromARN(arn, "stateMachine")
	if full == "" {
		return "", "", false
	}
	name, qualifier, _ = strings.Cut(full, ":")
	return name, qualifier, true
}

// versionNumber reads a qualifier as a version number; 0 when it is not one
// (an alias name, or a "01" that AWS never mints).
func versionNumber(qualifier string) int {
	n, err := strconv.Atoi(qualifier)
	if err != nil || n <= 0 || strconv.Itoa(n) != qualifier {
		return 0
	}
	return n
}

// unqualifiedMachine resolves an ARN that must name the machine itself —
// publish, list versions, list aliases, update, delete. A version or alias
// ARN in that position is refused as ValidationException, since the ARN is
// well-formed but not what the operation takes.
func unqualifiedMachine(arn string) (string, *awshttp.APIError) {
	name, qualifier, ok := splitMachineARN(arn)
	if !ok {
		return "", errInvalidARN(arn)
	}
	if qualifier != "" {
		return "", errValidation("Invalid State Machine Arn: '%s' is a version or alias ARN; this operation takes the unqualified state machine ARN", arn)
	}
	return name, nil
}

// errRequired is the spelling modelcheck uses for a missing @required member,
// so an operation whose constraint table has not been generated yet still
// refuses the way the others do.
func errRequired(member string) *awshttp.APIError {
	return errValidation("1 validation error detected: Value null at '%s' failed to satisfy constraint: Member must not be null", member)
}

func errConflict(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(409, "ConflictException", format, args...)
}

// errQualifiedNotFound is ResourceNotFound with the resourceName member the
// model gives it — what an alias or version lookup answers when there is no
// such thing under an existing machine.
func errQualifiedNotFound(arn string) *awshttp.APIError {
	e := errResourceNotFound(arn)
	name, _ := json.Marshal(arn)
	e.Extra = map[string]json.RawMessage{"resourceName": name}
	return e
}

// publishParams reads the publish flag and versionDescription that
// Create/UpdateStateMachine share. A description without publish is refused
// up front, before the machine is created or changed — AWS rejects the
// combination, and doing it late would leave a half-applied request.
func publishParams(p map[string]any) (publish bool, description string, aerr *awshttp.APIError) {
	publish = awsjson.Bool(p, "publish")
	description = awsjson.Str(p, "versionDescription")
	if description != "" && !publish {
		return false, "", errValidation("versionDescription can only be set when publish is true")
	}
	return publish, description, nil
}

// publishInto publishes m and adds stateMachineVersionArn to out.
func (s *Server) publishInto(out map[string]any, m *StateMachine, description string) *awshttp.APIError {
	v, aerr := s.store.PublishVersion(m, description)
	if aerr != nil {
		return aerr
	}
	out["stateMachineVersionArn"] = v.ARN
	return nil
}

func (s *Server) publishStateMachineVersion(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	if arn == "" {
		return nil, errRequired("stateMachineArn")
	}
	name, aerr := unqualifiedMachine(arn)
	if aerr != nil {
		return nil, aerr
	}
	if d := awsjson.Str(p, "description"); len(d) > 256 {
		return nil, errValidation("1 validation error detected: Value at 'description' failed to satisfy constraint: Member must have length less than or equal to 256")
	}
	m, aerr := s.store.GetMachine(name)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, errMachineNotFound(arn)
	}
	// revisionId is the optimistic check: a caller who read the machine,
	// then publishes, wants the version to hold what they read — not what
	// a concurrent update slipped in.
	if want := awsjson.Str(p, "revisionId"); want != "" && want != m.RevisionID {
		return nil, errConflict("Failed to publish the State Machine version for revision %s. The current State Machine revision is %s.", want, m.RevisionID)
	}
	v, aerr := s.store.PublishVersion(m, awsjson.Str(p, "description"))
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"stateMachineVersionArn": v.ARN,
		"creationDate":           epoch(v.CreatedAt),
	}, nil
}

func (s *Server) deleteStateMachineVersion(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineVersionArn")
	if arn == "" {
		return nil, errRequired("stateMachineVersionArn")
	}
	name, qualifier, ok := splitMachineARN(arn)
	n := versionNumber(qualifier)
	if !ok || n == 0 {
		return nil, errInvalidARN(arn)
	}
	if aerr := s.store.DeleteVersion(name, n); aerr != nil {
		return nil, aerr
	}
	return map[string]any{}, nil
}

// listStateMachineVersions answers newest first, as AWS sorts by descending
// creation time. An unknown machine is an empty list, not
// StateMachineDoesNotExist — the model lists no such error for this
// operation.
func (s *Server) listStateMachineVersions(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	if arn == "" {
		return nil, errRequired("stateMachineArn")
	}
	name, aerr := unqualifiedMachine(arn)
	if aerr != nil {
		return nil, aerr
	}
	versions, aerr := s.store.ListVersions(name)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]any, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		items = append(items, map[string]any{
			"stateMachineVersionArn": versions[i].ARN,
			"creationDate":           epoch(versions[i].CreatedAt),
		})
	}
	return page(p, "stateMachineVersions", items)
}

// describeQualified is DescribeStateMachine for a qualified ARN: a version
// ARN answers the snapshot — that version's definition, role, revision and
// description, with the version ARN as stateMachineArn, the way AWS does.
// An alias ARN is not a machine and not a version; AWS documents only the
// version form, so an alias answers StateMachineDoesNotExist.
func (s *Server) describeQualified(arn, name, qualifier string) (any, *awshttp.APIError) {
	n := versionNumber(qualifier)
	if n == 0 {
		return nil, errMachineNotFound(arn)
	}
	v, aerr := s.store.GetVersion(name, n)
	if aerr != nil {
		return nil, aerr
	}
	if v == nil {
		return nil, errMachineNotFound(arn)
	}
	out := map[string]any{
		"stateMachineArn": v.ARN,
		"name":            v.MachineName,
		"definition":      v.Definition,
		"roleArn":         v.RoleARN,
		"type":            v.Type,
		"status":          "ACTIVE",
		"creationDate":    epoch(v.CreatedAt),
		"revisionId":      v.RevisionID,
	}
	if v.Description != "" {
		out["description"] = v.Description
	}
	putConfigs(out, &StateMachine{
		LoggingConfiguration:    v.LoggingConfiguration,
		TracingConfiguration:    v.TracingConfiguration,
		EncryptionConfiguration: v.EncryptionConfiguration,
	})
	return out, nil
}

// startTarget resolves StartExecution's stateMachineArn. Unqualified, it is
// the machine as it stands today. A version ARN runs that version's frozen
// snapshot; an alias ARN picks one of its versions by weight and runs that.
// The StateMachine handed back carries the definition, role and revision
// the execution will run, while ARN and Name stay the machine's own — the
// execution's stateMachineArn is unqualified on AWS too, and the version
// and alias travel as their own fields.
func (s *Server) startTarget(arn string) (m *StateMachine, versionARN, aliasARN string, aerr *awshttp.APIError) {
	name, qualifier, ok := splitMachineARN(arn)
	if !ok {
		return nil, "", "", errInvalidARN(arn)
	}
	m, aerr = s.store.GetMachine(name)
	if aerr != nil {
		return nil, "", "", aerr
	}
	if m == nil {
		return nil, "", "", errMachineNotFound(arn)
	}
	if qualifier == "" {
		return m, "", "", nil
	}
	n := versionNumber(qualifier)
	if n == 0 {
		a, aerr := s.store.GetAlias(name, qualifier)
		if aerr != nil {
			return nil, "", "", aerr
		}
		if a == nil {
			return nil, "", "", errMachineNotFound(arn)
		}
		aliasARN = a.ARN
		n = versionNumber(strings.TrimPrefix(pickRoute(a.Routing), machineARN(s.id, name)+":"))
	}
	v, aerr := s.store.GetVersion(name, n)
	if aerr != nil {
		return nil, "", "", aerr
	}
	if v == nil {
		// A version an alias still routes to cannot be deleted, so this is
		// only reachable for a direct version ARN.
		return nil, "", "", errMachineNotFound(arn)
	}
	m.Definition, m.RoleARN, m.RevisionID = v.Definition, v.RoleARN, v.RevisionID
	return m, v.ARN, aliasARN, nil
}

// pickRoute chooses a version by weight. One entry is deterministic (its
// weight is 100 by construction); two entries split as AWS does, "randomly
// chooses which version runs a given execution based on the percentage".
func pickRoute(routes []Route) string {
	if len(routes) == 1 {
		return routes[0].VersionARN
	}
	if rand.IntN(100) < routes[0].Weight {
		return routes[0].VersionARN
	}
	return routes[1].VersionARN
}

// executionQualifier is ListExecutions' side of qualified ARNs: an alias or
// version ARN lists only the executions started through it, and an unknown
// one is ResourceNotFound. Answers the (versionARN, aliasARN) pair an
// execution must match, "" meaning no constraint.
func (s *Server) executionQualifier(arn, name, qualifier string) (versionARN, aliasARN string, aerr *awshttp.APIError) {
	if qualifier == "" {
		return "", "", nil
	}
	if n := versionNumber(qualifier); n > 0 {
		v, aerr := s.store.GetVersion(name, n)
		if aerr != nil {
			return "", "", aerr
		}
		if v == nil {
			return "", "", errQualifiedNotFound(arn)
		}
		return v.ARN, "", nil
	}
	a, aerr := s.store.GetAlias(name, qualifier)
	if aerr != nil {
		return "", "", aerr
	}
	if a == nil {
		return "", "", errQualifiedNotFound(arn)
	}
	return "", a.ARN, nil
}

// putQualifiers adds stateMachineVersionArn and stateMachineAliasArn to a
// DescribeExecution or ListExecutions item when the execution was started
// through one; an execution of the bare machine carries neither.
func putQualifiers(out map[string]any, e *Execution) {
	if e.VersionARN != "" {
		out["stateMachineVersionArn"] = e.VersionARN
	}
	if e.AliasARN != "" {
		out["stateMachineAliasArn"] = e.AliasARN
	}
}
