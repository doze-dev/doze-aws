package stepfunctions

import (
	"context"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// State machine aliases: a name that routes StartExecution to one or two
// published versions of one machine, so a caller can pin "PROD" and move it
// without changing client code. Create, Describe, Update, Delete, List.

// aliasOf resolves the stateMachineAliasArn parameter to (machine, alias
// name). An integer qualifier is a version ARN, which is InvalidArn here —
// the operation asked for an alias.
func aliasOf(p map[string]any) (machine, alias string, aerr *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineAliasArn")
	if arn == "" {
		return "", "", errRequired("stateMachineAliasArn")
	}
	machine, alias, ok := splitMachineARN(arn)
	if !ok || alias == "" || versionNumber(alias) != 0 {
		return "", "", errInvalidARN(arn)
	}
	return machine, alias, nil
}

// routingOf validates routingConfiguration and answers it as Routes, plus the
// machine the versions belong to. AWS's rules: one or two entries, each a
// version ARN with a weight in 0..100, weights summing to 100, all versions
// of the same machine and no version twice; every version must exist, which
// is ResourceNotFound rather than ValidationException because the request
// was well-formed. machine, when non-empty, is the alias's own machine
// (UpdateStateMachineAlias), which the versions must belong to.
func (s *Server) routingOf(p map[string]any, machine string) ([]Route, string, *awshttp.APIError) {
	raw, ok := p["routingConfiguration"].([]any)
	if !ok {
		return nil, "", errRequired("routingConfiguration")
	}
	if len(raw) < 1 || len(raw) > 2 {
		return nil, "", errValidation("1 validation error detected: Value at 'routingConfiguration' failed to satisfy constraint: Member must have length between 1 and 2")
	}
	routes := make([]Route, 0, len(raw))
	sum := 0
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		arn := awsjson.Str(entry, "stateMachineVersionArn")
		if arn == "" {
			return nil, "", errRequired("routingConfiguration.stateMachineVersionArn")
		}
		name, qualifier, ok := splitMachineARN(arn)
		n := versionNumber(qualifier)
		if !ok || n == 0 {
			return nil, "", errInvalidARN(arn)
		}
		if machine == "" {
			machine = name
		} else if name != machine {
			return nil, "", errValidation("Routing configuration must reference versions of the same state machine")
		}
		weight := awsjson.Int(entry, "weight", -1)
		if weight < 0 || weight > 100 {
			return nil, "", errValidation("1 validation error detected: Value at 'routingConfiguration.weight' failed to satisfy constraint: Member must have value between 0 and 100")
		}
		for _, r := range routes {
			if r.VersionARN == arn {
				return nil, "", errValidation("Routing configuration must not reference the same version twice: '%s'", arn)
			}
		}
		v, aerr := s.store.GetVersion(name, n)
		if aerr != nil {
			return nil, "", aerr
		}
		if v == nil {
			return nil, "", errQualifiedNotFound(arn)
		}
		routes = append(routes, Route{VersionARN: arn, Weight: weight})
		sum += weight
	}
	if sum != 100 {
		return nil, "", errValidation("Routing configuration weights must sum to 100, got %d", sum)
	}
	return routes, machine, nil
}

func aliasDescription(p map[string]any) (*string, *awshttp.APIError) {
	raw, ok := p["description"]
	if !ok {
		return nil, nil
	}
	d, _ := raw.(string)
	if len(d) > 256 {
		return nil, errValidation("1 validation error detected: Value at 'description' failed to satisfy constraint: Member must have length less than or equal to 256")
	}
	return &d, nil
}

func (s *Server) createStateMachineAlias(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "name")
	if name == "" {
		return nil, errRequired("name")
	}
	if aerr := checkName(name); aerr != nil {
		return nil, aerr
	}
	// An integer name would mint an ARN indistinguishable from a version's.
	if versionNumber(name) != 0 {
		return nil, errValidation("Alias name must not be an integer: '%s' would collide with a version ARN", name)
	}
	description, aerr := aliasDescription(p)
	if aerr != nil {
		return nil, aerr
	}
	routes, machine, aerr := s.routingOf(p, "")
	if aerr != nil {
		return nil, aerr
	}
	a := &Alias{MachineName: machine, Name: name, ARN: aliasARN(s.id, machine, name), Routing: routes}
	if description != nil {
		a.Description = *description
	}
	stored, aerr := s.store.PutAlias(a)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{
		"stateMachineAliasArn": stored.ARN,
		"creationDate":         epoch(stored.CreatedAt),
	}, nil
}

func (s *Server) describeStateMachineAlias(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	machine, name, aerr := aliasOf(p)
	if aerr != nil {
		return nil, aerr
	}
	a, aerr := s.store.GetAlias(machine, name)
	if aerr != nil {
		return nil, aerr
	}
	if a == nil {
		return nil, errQualifiedNotFound(awsjson.Str(p, "stateMachineAliasArn"))
	}
	routing := make([]any, 0, len(a.Routing))
	for _, r := range a.Routing {
		routing = append(routing, map[string]any{"stateMachineVersionArn": r.VersionARN, "weight": r.Weight})
	}
	out := map[string]any{
		"stateMachineAliasArn": a.ARN,
		"name":                 a.Name,
		"routingConfiguration": routing,
		"creationDate":         epoch(a.CreatedAt),
		"updateDate":           epoch(a.UpdatedAt),
	}
	if a.Description != "" {
		out["description"] = a.Description
	}
	return out, nil
}

func (s *Server) updateStateMachineAlias(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	machine, name, aerr := aliasOf(p)
	if aerr != nil {
		return nil, aerr
	}
	description, aerr := aliasDescription(p)
	if aerr != nil {
		return nil, aerr
	}
	var routes []Route
	if _, ok := p["routingConfiguration"]; ok {
		if routes, _, aerr = s.routingOf(p, machine); aerr != nil {
			return nil, aerr
		}
	}
	if description == nil && routes == nil {
		return nil, errValidation("At least one of description or routingConfiguration must be specified")
	}
	a, aerr := s.store.UpdateAlias(machine, name, description, routes)
	if aerr != nil {
		return nil, aerr
	}
	if a == nil {
		return nil, errQualifiedNotFound(awsjson.Str(p, "stateMachineAliasArn"))
	}
	return map[string]any{"updateDate": epoch(a.UpdatedAt)}, nil
}

// deleteStateMachineAlias answers ResourceNotFound for an alias that is not
// there — the model lists it, unlike DeleteStateMachineVersion, whose
// absent target succeeds. The versions the alias pointed at stay.
func (s *Server) deleteStateMachineAlias(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	machine, name, aerr := aliasOf(p)
	if aerr != nil {
		return nil, aerr
	}
	found, aerr := s.store.DeleteAlias(machine, name)
	if aerr != nil {
		return nil, aerr
	}
	if !found {
		return nil, errQualifiedNotFound(awsjson.Str(p, "stateMachineAliasArn"))
	}
	return map[string]any{}, nil
}

// listStateMachineAliases lists a machine's aliases in name order. Given a
// version ARN it lists only the aliases routing to that version, as AWS
// documents; an alias ARN is neither and is InvalidArn.
func (s *Server) listStateMachineAliases(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "stateMachineArn")
	if arn == "" {
		return nil, errRequired("stateMachineArn")
	}
	machine, qualifier, ok := splitMachineARN(arn)
	if !ok || (qualifier != "" && versionNumber(qualifier) == 0) {
		return nil, errInvalidARN(arn)
	}
	m, aerr := s.store.GetMachine(machine)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, errMachineNotFound(arn)
	}
	if qualifier != "" {
		v, aerr := s.store.GetVersion(machine, versionNumber(qualifier))
		if aerr != nil {
			return nil, aerr
		}
		if v == nil {
			return nil, errQualifiedNotFound(arn)
		}
	}
	aliases, aerr := s.store.ListAliases(machine)
	if aerr != nil {
		return nil, aerr
	}
	items := make([]any, 0, len(aliases))
	for _, a := range aliases {
		if qualifier != "" && !routesTo(a, arn) {
			continue
		}
		items = append(items, map[string]any{
			"stateMachineAliasArn": a.ARN,
			"creationDate":         epoch(a.CreatedAt),
		})
	}
	return page(p, "stateMachineAliases", items)
}

func routesTo(a Alias, versionARN string) bool {
	for _, r := range a.Routing {
		if r.VersionARN == versionARN {
			return true
		}
	}
	return false
}
