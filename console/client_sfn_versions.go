package console

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---- Step Functions: versions and aliases ----
//
// A version is a frozen copy of the machine — definition, role, revision —
// addressed by the machine ARN with ":<n>" on the end. An alias is a name
// that routes StartExecution to one or two versions by weight, addressed by
// ":<name>". Both are what "deploy the workflow without editing the caller"
// looks like, and the console shows them the way Lambda's console shows
// function versions: a table each, under the machine.

// Version is one published snapshot.
type Version struct {
	Number      int
	ARN         string
	Description string
	Created     string
}

// Route is one entry of an alias's routing configuration.
type Route struct {
	VersionARN string
	Version    int
	Weight     int
}

// Alias is a named pointer at one or two versions.
type Alias struct {
	Name        string
	ARN         string
	Description string
	Created     string
	Updated     string
	Routing     []Route
}

// RoutingText renders the routing the way the table shows it: "v3 100%" for
// one version, "v2 70% · v3 30%" for two.
func (a Alias) RoutingText() string {
	parts := make([]string, 0, len(a.Routing))
	for _, r := range a.Routing {
		parts = append(parts, fmt.Sprintf("v%d %d%%", r.Version, r.Weight))
	}
	return strings.Join(parts, " · ")
}

// qualifierOf is the ":<n>" or ":<name>" on the end of a qualified machine
// ARN — "" for the machine itself.
func qualifierOf(arn string) string {
	parts := strings.SplitN(arn, ":", 8)
	if len(parts) != 8 {
		return ""
	}
	return parts[7]
}

// versionNumberOf reads the version number a version ARN carries; 0 when the
// qualifier is not one.
func versionNumberOf(arn string) int {
	n, _ := strconv.Atoi(qualifierOf(arn))
	return n
}

func (b *backend) PublishVersion(ctx context.Context, machineARN, description string) (string, error) {
	in := map[string]any{"stateMachineArn": machineARN}
	if description != "" {
		in["description"] = description
	}
	body, err := b.sfnCall(ctx, "PublishStateMachineVersion", in)
	if err != nil {
		return "", err
	}
	var out struct {
		ARN string `json:"stateMachineVersionArn"`
	}
	json.Unmarshal(body, &out)
	return out.ARN, nil
}

// ListVersions lists a machine's versions, newest first, each described so
// the table has its description: the list call carries only ARN and date,
// and DescribeStateMachine on a version ARN is how AWS answers the rest.
func (b *backend) ListVersions(ctx context.Context, machineARN string) ([]Version, error) {
	body, err := b.sfnCall(ctx, "ListStateMachineVersions", map[string]any{"stateMachineArn": machineARN, "maxResults": 1000})
	if err != nil {
		return nil, err
	}
	var out struct {
		Versions []struct {
			ARN          string  `json:"stateMachineVersionArn"`
			CreationDate float64 `json:"creationDate"`
		} `json:"stateMachineVersions"`
	}
	json.Unmarshal(body, &out)
	vs := make([]Version, 0, len(out.Versions))
	for _, v := range out.Versions {
		ver := Version{Number: versionNumberOf(v.ARN), ARN: v.ARN, Created: epochToTime(v.CreationDate)}
		if sm, err := b.DescribeStateMachine(ctx, v.ARN); err == nil {
			ver.Description = sm.Description
		}
		vs = append(vs, ver)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Number > vs[j].Number })
	return vs, nil
}

func (b *backend) DeleteVersion(ctx context.Context, versionARN string) error {
	_, err := b.sfnCall(ctx, "DeleteStateMachineVersion", map[string]any{"stateMachineVersionArn": versionARN})
	return err
}

// routingJSON is the wire shape of a routing configuration.
func routingJSON(routes []Route) []map[string]any {
	out := make([]map[string]any, 0, len(routes))
	for _, r := range routes {
		out = append(out, map[string]any{"stateMachineVersionArn": r.VersionARN, "weight": r.Weight})
	}
	return out
}

func (b *backend) CreateMachineAlias(ctx context.Context, name, description string, routes []Route) (string, error) {
	in := map[string]any{"name": name, "routingConfiguration": routingJSON(routes)}
	if description != "" {
		in["description"] = description
	}
	body, err := b.sfnCall(ctx, "CreateStateMachineAlias", in)
	if err != nil {
		return "", err
	}
	var out struct {
		ARN string `json:"stateMachineAliasArn"`
	}
	json.Unmarshal(body, &out)
	return out.ARN, nil
}

// UpdateMachineAlias changes an alias's routing and, when non-nil, its description.
// The two are separate because the model treats an absent description as
// "leave it", and the inline routing editor has no business clearing it.
func (b *backend) UpdateMachineAlias(ctx context.Context, aliasARN string, description *string, routes []Route) error {
	in := map[string]any{"stateMachineAliasArn": aliasARN}
	if len(routes) > 0 {
		in["routingConfiguration"] = routingJSON(routes)
	}
	if description != nil {
		in["description"] = *description
	}
	_, err := b.sfnCall(ctx, "UpdateStateMachineAlias", in)
	return err
}

func (b *backend) DeleteMachineAlias(ctx context.Context, aliasARN string) error {
	_, err := b.sfnCall(ctx, "DeleteStateMachineAlias", map[string]any{"stateMachineAliasArn": aliasARN})
	return err
}

func (b *backend) DescribeMachineAlias(ctx context.Context, aliasARN string) (Alias, error) {
	body, err := b.sfnCall(ctx, "DescribeStateMachineAlias", map[string]any{"stateMachineAliasArn": aliasARN})
	if err != nil {
		return Alias{}, err
	}
	var out struct {
		ARN         string `json:"stateMachineAliasArn"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Routing     []struct {
			VersionARN string `json:"stateMachineVersionArn"`
			Weight     int    `json:"weight"`
		} `json:"routingConfiguration"`
		CreationDate float64 `json:"creationDate"`
		UpdateDate   float64 `json:"updateDate"`
	}
	json.Unmarshal(body, &out)
	a := Alias{
		Name: out.Name, ARN: out.ARN, Description: out.Description,
		Created: epochToTime(out.CreationDate), Updated: epochToTime(out.UpdateDate),
	}
	for _, r := range out.Routing {
		a.Routing = append(a.Routing, Route{VersionARN: r.VersionARN, Version: versionNumberOf(r.VersionARN), Weight: r.Weight})
	}
	return a, nil
}

// ListAliases lists a machine's aliases in name order, each described — the
// list call is ARNs and dates, and the routing is the column that matters.
func (b *backend) ListMachineAliases(ctx context.Context, machineARN string) ([]Alias, error) {
	body, err := b.sfnCall(ctx, "ListStateMachineAliases", map[string]any{"stateMachineArn": machineARN, "maxResults": 1000})
	if err != nil {
		return nil, err
	}
	var out struct {
		Aliases []struct {
			ARN string `json:"stateMachineAliasArn"`
		} `json:"stateMachineAliases"`
	}
	json.Unmarshal(body, &out)
	as := make([]Alias, 0, len(out.Aliases))
	for _, item := range out.Aliases {
		a, err := b.DescribeMachineAlias(ctx, item.ARN)
		if err != nil {
			a = Alias{Name: qualifierOf(item.ARN), ARN: item.ARN}
		}
		as = append(as, a)
	}
	sort.Slice(as, func(i, j int) bool { return as[i].Name < as[j].Name })
	return as, nil
}

// versionARNOf builds a version ARN from the machine's console path segment.
func versionARNOf(machine string, n int) string {
	return stateMachineARNOf(machine) + ":" + strconv.Itoa(n)
}

// aliasARNOf builds an alias ARN from the two path segments.
func aliasARNOf(machine, alias string) string { return stateMachineARNOf(machine) + ":" + alias }
