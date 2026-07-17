package ssm

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

var handlers = map[string]handler{
	"PutParameter":            (*Server).putParameter,
	"GetParameter":            (*Server).getParameter,
	"GetParameters":           (*Server).getParameters,
	"GetParametersByPath":     (*Server).getParametersByPath,
	"GetParameterHistory":     (*Server).getParameterHistory,
	"DeleteParameter":         (*Server).deleteParameter,
	"DeleteParameters":        (*Server).deleteParameters,
	"DescribeParameters":      (*Server).describeParameters,
	"LabelParameterVersion":   (*Server).labelParameterVersion,
	"UnlabelParameterVersion": (*Server).unlabelParameterVersion,
	"AddTagsToResource":       (*Server).addTags,
	"RemoveTagsFromResource":  (*Server).removeTags,
	"ListTagsForResource":     (*Server).listTags,
}

// ---- param helpers ----

func base64of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// paramView is the GetParameter(s) result shape.
type paramView struct {
	Name             string  `json:"Name"`
	Type             string  `json:"Type"`
	Value            string  `json:"Value"`
	Version          int64   `json:"Version"`
	Selector         string  `json:"Selector,omitempty"`
	ARN              string  `json:"ARN"`
	DataType         string  `json:"DataType"`
	LastModifiedDate float64 `json:"LastModifiedDate"`
}

func view(p *Parameter, v *Version, value, selector string) paramView {
	return paramView{
		Name:             p.Name,
		Type:             p.Type,
		Value:            value,
		Version:          v.Version,
		Selector:         selector,
		ARN:              paramARN(p.Name),
		DataType:         p.DataType,
		LastModifiedDate: float64(v.Created),
	}
}

// ---- handlers ----

func (s *Server) putParameter(p map[string]any) (any, *awshttp.APIError) {
	if _, ok := p["Value"]; !ok {
		return nil, awshttp.Errf(400, "ValidationException", "Value is required")
	}
	policies := awsjson.Str(p, "Policies")
	expiresAt, aerr := parseExpiration(policies)
	if aerr != nil {
		return nil, aerr
	}
	tags := map[string]string{}
	if list, ok := p["Tags"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				k, _ := m["Key"].(string)
				v, _ := m["Value"].(string)
				if k != "" {
					tags[k] = v
				}
			}
		}
	}
	version, aerr := s.store.Put(
		awsjson.Str(p, "Name"), awsjson.Str(p, "Type"), awsjson.Str(p, "Value"),
		awsjson.Str(p, "KeyId"), awsjson.Str(p, "Description"), awsjson.Str(p, "DataType"),
		awsjson.Str(p, "Tier"), policies, expiresAt, tags, awsjson.Bool(p, "Overwrite"),
	)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{"Version": version, "Tier": orDefault(awsjson.Str(p, "Tier"), "Standard")}, nil
}

// parseExpiration extracts the Expiration policy timestamp from a parameter
// policies document, if present.
func parseExpiration(policies string) (int64, *awshttp.APIError) {
	if strings.TrimSpace(policies) == "" {
		return 0, nil
	}
	var list []struct {
		Type       string `json:"Type"`
		Attributes struct {
			Timestamp string `json:"Timestamp"`
		} `json:"Attributes"`
	}
	if err := json.Unmarshal([]byte(policies), &list); err != nil {
		return 0, awshttp.Errf(400, "InvalidPolicyTypeException", "Policies is not a valid policy list: %v", err)
	}
	for _, pol := range list {
		if !strings.EqualFold(pol.Type, "Expiration") {
			continue
		}
		t, err := time.Parse(time.RFC3339, pol.Attributes.Timestamp)
		if err != nil {
			return 0, awshttp.Errf(400, "InvalidPolicyAttributeException", "Expiration Timestamp %q is not RFC3339", pol.Attributes.Timestamp)
		}
		return t.Unix(), nil
	}
	return 0, nil
}

func (s *Server) getParameter(p map[string]any) (any, *awshttp.APIError) {
	selector := awsjson.Str(p, "Name")
	param, v, value, aerr := s.store.Get(selector, awsjson.Bool(p, "WithDecryption"))
	if aerr != nil {
		return nil, aerr
	}
	sel := ""
	if i := strings.LastIndex(selector, ":"); i > 0 {
		sel = selector[i:]
	}
	return map[string]any{"Parameter": view(param, v, value, sel)}, nil
}

func (s *Server) getParameters(p map[string]any) (any, *awshttp.APIError) {
	decrypt := awsjson.Bool(p, "WithDecryption")
	params := []paramView{}
	invalid := []string{}
	for _, name := range awsjson.Strs(p, "Names") {
		param, v, value, aerr := s.store.Get(name, decrypt)
		if aerr != nil {
			invalid = append(invalid, name)
			continue
		}
		params = append(params, view(param, v, value, ""))
	}
	return map[string]any{"Parameters": params, "InvalidParameters": invalid}, nil
}

func (s *Server) getParametersByPath(p map[string]any) (any, *awshttp.APIError) {
	decrypt := awsjson.Bool(p, "WithDecryption")
	list, err := s.store.ByPath(awsjson.Str(p, "Path"), awsjson.Bool(p, "Recursive"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	params := []paramView{}
	for i := range list {
		param := &list[i]
		v := param.Latest()
		value, aerr := s.store.render(param, v, decrypt)
		if aerr != nil {
			return nil, aerr
		}
		params = append(params, view(param, v, value, ""))
	}
	return map[string]any{"Parameters": params}, nil
}

func (s *Server) getParameterHistory(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "Name")
	param, _, _, aerr := s.store.Get(name, false)
	if aerr != nil {
		return nil, aerr
	}
	decrypt := awsjson.Bool(p, "WithDecryption")
	type histEntry struct {
		Name             string   `json:"Name"`
		Type             string   `json:"Type"`
		Value            string   `json:"Value"`
		Version          int64    `json:"Version"`
		Labels           []string `json:"Labels"`
		LastModifiedDate float64  `json:"LastModifiedDate"`
		Description      string   `json:"Description,omitempty"`
		DataType         string   `json:"DataType"`
	}
	out := []histEntry{}
	for i := range param.Versions {
		v := &param.Versions[i]
		value, aerr := s.store.render(param, v, decrypt)
		if aerr != nil {
			return nil, aerr
		}
		labels := v.Labels
		if labels == nil {
			labels = []string{}
		}
		out = append(out, histEntry{
			Name: param.Name, Type: param.Type, Value: value, Version: v.Version,
			Labels: labels, LastModifiedDate: float64(v.Created),
			Description: param.Description, DataType: param.DataType,
		})
	}
	return map[string]any{"Parameters": out}, nil
}

func (s *Server) deleteParameter(p map[string]any) (any, *awshttp.APIError) {
	return nil, s.store.Delete(awsjson.Str(p, "Name"))
}

func (s *Server) deleteParameters(p map[string]any) (any, *awshttp.APIError) {
	deleted := []string{}
	invalid := []string{}
	for _, name := range awsjson.Strs(p, "Names") {
		if aerr := s.store.Delete(name); aerr != nil {
			invalid = append(invalid, name)
			continue
		}
		deleted = append(deleted, name)
	}
	return map[string]any{"DeletedParameters": deleted, "InvalidParameters": invalid}, nil
}

func (s *Server) describeParameters(p map[string]any) (any, *awshttp.APIError) {
	all, err := s.store.List()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	filtered, aerr := applyFilters(all, p)
	if aerr != nil {
		return nil, aerr
	}
	type meta struct {
		Name             string  `json:"Name"`
		ARN              string  `json:"ARN"`
		Type             string  `json:"Type"`
		KeyId            string  `json:"KeyId,omitempty"`
		Description      string  `json:"Description,omitempty"`
		Version          int64   `json:"Version"`
		Tier             string  `json:"Tier"`
		DataType         string  `json:"DataType"`
		LastModifiedDate float64 `json:"LastModifiedDate"`
	}
	out := []meta{}
	for i := range filtered {
		param := &filtered[i]
		out = append(out, meta{
			Name: param.Name, ARN: paramARN(param.Name), Type: param.Type,
			KeyId: param.KeyID, Description: param.Description,
			Version: param.Latest().Version, Tier: orDefault(param.Tier, "Standard"),
			DataType: param.DataType, LastModifiedDate: float64(param.Latest().Created),
		})
	}
	return map[string]any{"Parameters": out}, nil
}

// applyFilters implements the common ParameterFilters: Name (Equals/BeginsWith)
// and Type (Equals). Other filter keys are ignored rather than erroring — SDKs
// send them speculatively.
func applyFilters(all []Parameter, p map[string]any) ([]Parameter, *awshttp.APIError) {
	filters, ok := p["ParameterFilters"].([]any)
	if !ok || len(filters) == 0 {
		return all, nil
	}
	out := all
	for _, f := range filters {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		key, _ := fm["Key"].(string)
		option, _ := fm["Option"].(string)
		values := awsjson.Strs(fm, "Values")
		match := func(param *Parameter) bool { return true }
		switch key {
		case "Name":
			match = func(param *Parameter) bool {
				for _, v := range values {
					if option == "BeginsWith" && strings.HasPrefix(param.Name, v) {
						return true
					}
					if (option == "" || option == "Equals") && param.Name == v {
						return true
					}
				}
				return false
			}
		case "Type":
			match = func(param *Parameter) bool {
				for _, v := range values {
					if param.Type == v {
						return true
					}
				}
				return false
			}
		}
		var next []Parameter
		for _, param := range out {
			if match(&param) {
				next = append(next, param)
			}
		}
		out = next
	}
	return out, nil
}

func (s *Server) labelParameterVersion(p map[string]any) (any, *awshttp.APIError) {
	if _, aerr := s.store.Label(awsjson.Str(p, "Name"), awsjson.Int64(p, "ParameterVersion", 0), awsjson.Strs(p, "Labels")); aerr != nil {
		return nil, aerr
	}
	return map[string]any{"InvalidLabels": []string{}, "ParameterVersion": awsjson.Int64(p, "ParameterVersion", 0)}, nil
}

func (s *Server) unlabelParameterVersion(p map[string]any) (any, *awshttp.APIError) {
	removed, aerr := s.store.Unlabel(awsjson.Str(p, "Name"), awsjson.Int64(p, "ParameterVersion", 0), awsjson.Strs(p, "Labels"))
	if aerr != nil {
		return nil, aerr
	}
	if removed == nil {
		removed = []string{}
	}
	return map[string]any{"RemovedLabels": removed, "InvalidLabels": []string{}}, nil
}

// resourceName extracts the parameter name from tagging calls, which address
// parameters by ResourceType=Parameter + ResourceId=<name>.
func resourceName(p map[string]any) (string, *awshttp.APIError) {
	if rt := awsjson.Str(p, "ResourceType"); rt != "" && rt != "Parameter" {
		return "", awshttp.Errf(400, "InvalidResourceType",
			"doze-aws supports tagging for ResourceType Parameter only, got %q", rt)
	}
	return awsjson.Str(p, "ResourceId"), nil
}

func (s *Server) addTags(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := resourceName(p)
	if aerr != nil {
		return nil, aerr
	}
	tags := map[string]string{}
	if list, ok := p["Tags"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				k, _ := m["Key"].(string)
				v, _ := m["Value"].(string)
				if k != "" {
					tags[k] = v
				}
			}
		}
	}
	return nil, s.store.UpdateTags(name, tags, nil)
}

func (s *Server) removeTags(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := resourceName(p)
	if aerr != nil {
		return nil, aerr
	}
	return nil, s.store.UpdateTags(name, nil, awsjson.Strs(p, "TagKeys"))
}

func (s *Server) listTags(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := resourceName(p)
	if aerr != nil {
		return nil, aerr
	}
	tags, aerr := s.store.Tags(name)
	if aerr != nil {
		return nil, aerr
	}
	type tag struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []tag{}
	for _, k := range keys {
		out = append(out, tag{Key: k, Value: tags[k]})
	}
	return map[string]any{"TagList": out}, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
