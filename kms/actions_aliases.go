package kms

// Alias and tag actions: create/update/delete/list aliases plus resource tags.

import (
	"sort"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// ---- aliases ----

func (s *Server) createAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(awsjson.Str(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAlias(name, awsjson.Str(p, "TargetKeyId"), false, true))
}

func (s *Server) updateAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(awsjson.Str(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.SetAlias(name, awsjson.Str(p, "TargetKeyId"), true, false))
}

func (s *Server) deleteAlias(p map[string]any) (any, *awshttp.APIError) {
	name, aerr := aliasName(awsjson.Str(p, "AliasName"))
	if aerr != nil {
		return nil, aerr
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteAlias(name))
}

func aliasName(full string) (string, *awshttp.APIError) {
	if len(full) < 7 || full[:6] != "alias/" {
		return "", awshttp.Errf(400, "ValidationException", "AliasName must start with alias/")
	}
	return full[6:], nil
}

func (s *Server) listAliases(p map[string]any) (any, *awshttp.APIError) {
	aliases, err := s.store.Aliases()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	filterKey := ""
	if ident := awsjson.Str(p, "KeyId"); ident != "" {
		k, err := s.store.Resolve(ident)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		filterKey = k.ID
	}
	type entry struct {
		AliasName   string `json:"AliasName"`
		AliasArn    string `json:"AliasArn"`
		TargetKeyId string `json:"TargetKeyId"`
	}
	out := []entry{}
	for _, a := range aliases {
		if filterKey != "" && a[1] != filterKey {
			continue
		}
		out = append(out, entry{
			AliasName:   "alias/" + a[0],
			AliasArn:    aliasARN(a[0]),
			TargetKeyId: a[1],
		})
	}
	return map[string]any{"Aliases": out, "Truncated": false}, nil
}

func aliasARN(name string) string {
	return awsident.ARN("kms", "alias/"+name)
}

// ---- tags ----

func (s *Server) tagResource(p map[string]any) (any, *awshttp.APIError) {
	tags := ptags(p, "Tags")
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.Tags == nil {
			k.Tags = map[string]string{}
		}
		for key, v := range tags {
			k.Tags[key] = v
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) untagResource(p map[string]any) (any, *awshttp.APIError) {
	keys, _ := p["TagKeys"].([]any)
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		for _, tk := range keys {
			if name, ok := tk.(string); ok {
				delete(k.Tags, name)
			}
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) listResourceTags(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type tag struct {
		TagKey   string `json:"TagKey"`
		TagValue string `json:"TagValue"`
	}
	out := []tag{}
	for _, key := range sortedKeys(k.Tags) {
		out = append(out, tag{TagKey: key, TagValue: k.Tags[key]})
	}
	return map[string]any{"Tags": out, "Truncated": false}, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
