package secretsmanager

import (
	"crypto/rand"
	"encoding/base64"
	"maps"
	"sort"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var handlers = map[string]handler{
	"CreateSecret":             (*Server).createSecret,
	"GetSecretValue":           (*Server).getSecretValue,
	"BatchGetSecretValue":      (*Server).batchGetSecretValue,
	"PutSecretValue":           (*Server).putSecretValue,
	"UpdateSecret":             (*Server).updateSecret,
	"DeleteSecret":             (*Server).deleteSecret,
	"RestoreSecret":            (*Server).restoreSecret,
	"ListSecrets":              (*Server).listSecrets,
	"ListSecretVersionIds":     (*Server).listSecretVersionIds,
	"DescribeSecret":           (*Server).describeSecret,
	"UpdateSecretVersionStage": (*Server).updateSecretVersionStage,
	"RotateSecret":             (*Server).rotateSecret,
	"CancelRotateSecret":       (*Server).cancelRotateSecret,
	"TagResource":              (*Server).tagResource,
	"UntagResource":            (*Server).untagResource,
	"GetRandomPassword":        (*Server).getRandomPassword,
	"PutResourcePolicy":        (*Server).putResourcePolicy,
	"GetResourcePolicy":        (*Server).getResourcePolicy,
	"DeleteResourcePolicy":     (*Server).deleteResourcePolicy,
	"ValidateResourcePolicy":   (*Server).validateResourcePolicy,
}

func init() {
	// RotateSecret needs the lambda service (Phase 8); replication needs
	// other regions. Both answer honestly.
	for name, why := range map[string]string{
		"ReplicateSecretToRegions":     "there is exactly one region locally",
		"RemoveRegionsFromReplication": "there is exactly one region locally",
		"StopReplicationToReplica":     "there is exactly one region locally",
	} {
		reason := why
		handlers[name] = func(*Server, map[string]any) (any, *awshttp.APIError) {
			return nil, awshttp.Errf(400, "InvalidRequestException", "not supported by doze-aws: %s", reason)
		}
	}
}

// ---- param helpers ----

func pstr(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}

func pbool(p map[string]any, key string) bool {
	b, _ := p[key].(bool)
	return b
}

func pint(p map[string]any, key string) int {
	if f, ok := p[key].(float64); ok {
		return int(f)
	}
	return 0
}

func pblob(p map[string]any, key string) ([]byte, *awshttp.APIError) {
	s, ok := p[key].(string)
	if !ok || s == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, awshttp.Errf(400, "ValidationException", "%s is not valid base64", key)
	}
	return b, nil
}

func ptaglist(p map[string]any, key string) map[string]string {
	list, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			k, _ := m["Key"].(string)
			v, _ := m["Value"].(string)
			if k != "" {
				out[k] = v
			}
		}
	}
	return out
}

// values extracts SecretString/SecretBinary from a request.
func values(p map[string]any) (str, bin []byte, aerr *awshttp.APIError) {
	if s := pstr(p, "SecretString"); s != "" {
		str = []byte(s)
	}
	bin, aerr = pblob(p, "SecretBinary")
	return str, bin, aerr
}

// ---- handlers ----

func (s *Server) createSecret(p map[string]any) (any, *awshttp.APIError) {
	str, bin, aerr := values(p)
	if aerr != nil {
		return nil, aerr
	}
	sec, versionID, err := s.store.Create(
		pstr(p, "Name"), pstr(p, "Description"), pstr(p, "KmsKeyId"),
		pstr(p, "ClientRequestToken"), str, bin, ptaglist(p, "Tags"),
	)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name, "VersionId": versionID}, nil
}

func (s *Server) getSecretValue(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Get(pstr(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if sec.DeletedAt > 0 {
		return nil, errDeleted(sec.Name)
	}
	vid, v, aerr := sec.Resolve(pstr(p, "VersionId"), pstr(p, "VersionStage"))
	if aerr != nil {
		return nil, aerr
	}
	return s.renderValue(sec, vid, v)
}

func (s *Server) renderValue(sec *Secret, vid string, v *Version) (map[string]any, *awshttp.APIError) {
	out := map[string]any{
		"ARN":           sec.ARN,
		"Name":          sec.Name,
		"VersionId":     vid,
		"VersionStages": v.Stages,
		"CreatedDate":   float64(v.Created),
	}
	if v.String != nil {
		str, err := s.store.open(v.String)
		if err != nil {
			return nil, awshttp.Errf(500, "InternalServiceError", "stored secret value is corrupt")
		}
		out["SecretString"] = string(str)
	}
	if v.Binary != nil {
		bin, err := s.store.open(v.Binary)
		if err != nil {
			return nil, awshttp.Errf(500, "InternalServiceError", "stored secret value is corrupt")
		}
		out["SecretBinary"] = base64.StdEncoding.EncodeToString(bin)
	}
	return out, nil
}

func (s *Server) batchGetSecretValue(p map[string]any) (any, *awshttp.APIError) {
	ids, _ := p["SecretIdList"].([]any)
	var secrets []map[string]any
	var errs []map[string]any
	for _, idAny := range ids {
		id, _ := idAny.(string)
		sec, err := s.store.Get(id)
		if err != nil || sec.DeletedAt > 0 {
			errs = append(errs, map[string]any{
				"SecretId": id, "ErrorCode": "ResourceNotFoundException",
				"Message": "secret not found or scheduled for deletion",
			})
			continue
		}
		vid, v, aerr := sec.Resolve("", "")
		if aerr != nil {
			errs = append(errs, map[string]any{"SecretId": id, "ErrorCode": aerr.Code, "Message": aerr.Message})
			continue
		}
		val, aerr := s.renderValue(sec, vid, v)
		if aerr != nil {
			return nil, aerr
		}
		secrets = append(secrets, val)
	}
	return map[string]any{"SecretValues": secrets, "Errors": errs}, nil
}

func (s *Server) putSecretValue(p map[string]any) (any, *awshttp.APIError) {
	str, bin, aerr := values(p)
	if aerr != nil {
		return nil, aerr
	}
	if str == nil && bin == nil {
		return nil, awshttp.Errf(400, "InvalidRequestException", "SecretString or SecretBinary is required")
	}
	var stages []string
	if list, ok := p["VersionStages"].([]any); ok {
		for _, v := range list {
			if st, ok := v.(string); ok {
				stages = append(stages, st)
			}
		}
	}
	sec, vid, err := s.store.AddVersion(pstr(p, "SecretId"), pstr(p, "ClientRequestToken"), str, bin, stages)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ARN": sec.ARN, "Name": sec.Name, "VersionId": vid,
		"VersionStages": sec.Versions[vid].Stages,
	}, nil
}

func (s *Server) updateSecret(p map[string]any) (any, *awshttp.APIError) {
	str, bin, aerr := values(p)
	if aerr != nil {
		return nil, aerr
	}
	id := pstr(p, "SecretId")
	sec, err := s.store.Mutate(id, func(sec *Secret) error {
		if sec.DeletedAt > 0 {
			return errDeleted(sec.Name)
		}
		if d := pstr(p, "Description"); d != "" {
			sec.Description = d
		}
		if k := pstr(p, "KmsKeyId"); k != "" {
			sec.KMSKeyID = k
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{"ARN": sec.ARN, "Name": sec.Name}
	if str != nil || bin != nil {
		_, vid, err := s.store.AddVersion(id, pstr(p, "ClientRequestToken"), str, bin, nil)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		out["VersionId"] = vid
	}
	return out, nil
}

func (s *Server) deleteSecret(p map[string]any) (any, *awshttp.APIError) {
	force := pbool(p, "ForceDeleteWithoutRecovery")
	days := pint(p, "RecoveryWindowInDays")
	if force && days > 0 {
		return nil, awshttp.Errf(400, "InvalidParameterException",
			"specify either ForceDeleteWithoutRecovery or RecoveryWindowInDays, not both")
	}
	sec, err := s.store.Delete(pstr(p, "SecretId"), days, force)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	deletionDate := float64(sec.PurgeAt)
	if force {
		deletionDate = float64(sec.DeletedAt)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name, "DeletionDate": deletionDate}, nil
}

func (s *Server) restoreSecret(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Restore(pstr(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name}, nil
}

// describe renders the DescribeSecret/ListSecrets entry shape.
func describe(sec *Secret) map[string]any {
	stages := map[string][]string{}
	for vid, v := range sec.Versions {
		if len(v.Stages) > 0 {
			stages[vid] = v.Stages
		}
	}
	out := map[string]any{
		"ARN":             sec.ARN,
		"Name":            sec.Name,
		"Description":     sec.Description,
		"KmsKeyId":        sec.KMSKeyID,
		"CreatedDate":     float64(sec.Created),
		"LastChangedDate": float64(sec.LastChanged),
		// DescribeSecret names this VersionIdsToStages; the ListSecrets entry
		// shape names it SecretVersionsToStages. Emit both.
		"VersionIdsToStages":     stages,
		"SecretVersionsToStages": stages,
	}
	if len(sec.Tags) > 0 {
		type tag struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		}
		keys := make([]string, 0, len(sec.Tags))
		for k := range sec.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		tags := []tag{}
		for _, k := range keys {
			tags = append(tags, tag{Key: k, Value: sec.Tags[k]})
		}
		out["Tags"] = tags
	}
	if sec.DeletedAt > 0 {
		out["DeletedDate"] = float64(sec.DeletedAt)
	}
	out["RotationEnabled"] = sec.RotationEnabled
	if sec.RotationLambdaARN != "" {
		out["RotationLambdaARN"] = sec.RotationLambdaARN
	}
	if sec.LastRotatedDate > 0 {
		out["LastRotatedDate"] = float64(sec.LastRotatedDate)
	}
	return out
}

func (s *Server) describeSecret(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Get(pstr(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return describe(sec), nil
}

func (s *Server) listSecrets(p map[string]any) (any, *awshttp.APIError) {
	all, err := s.store.List()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	includeDeleted := pbool(p, "IncludePlannedDeletion")
	out := []map[string]any{}
	for i := range all {
		if all[i].DeletedAt > 0 && !includeDeleted {
			continue
		}
		out = append(out, describe(&all[i]))
	}
	return map[string]any{"SecretList": out}, nil
}

func (s *Server) listSecretVersionIds(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Get(pstr(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type entry struct {
		VersionId     string   `json:"VersionId"`
		VersionStages []string `json:"VersionStages,omitempty"`
		CreatedDate   float64  `json:"CreatedDate"`
	}
	out := []entry{}
	for vid, v := range sec.Versions {
		if len(v.Stages) == 0 && !pbool(p, "IncludeDeprecated") {
			continue
		}
		out = append(out, entry{VersionId: vid, VersionStages: v.Stages, CreatedDate: float64(v.Created)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedDate < out[j].CreatedDate })
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name, "Versions": out}, nil
}

func (s *Server) updateSecretVersionStage(p map[string]any) (any, *awshttp.APIError) {
	stage := pstr(p, "VersionStage")
	if stage == "" {
		return nil, awshttp.Errf(400, "ValidationException", "VersionStage is required")
	}
	moveTo := pstr(p, "MoveToVersionId")
	removeFrom := pstr(p, "RemoveFromVersionId")
	sec, err := s.store.Mutate(pstr(p, "SecretId"), func(sec *Secret) error {
		if removeFrom != "" {
			v, ok := sec.Versions[removeFrom]
			if !ok || !contains(v.Stages, stage) {
				return awshttp.Errf(400, "InvalidParameterException",
					"version %s does not carry stage %s", removeFrom, stage)
			}
			v.Stages = remove(v.Stages, stage)
			sec.Versions[removeFrom] = v
		}
		if moveTo != "" {
			v, ok := sec.Versions[moveTo]
			if !ok {
				return errSecretNotFound(sec.Name + " version " + moveTo)
			}
			// A stage names at most one version: strip it elsewhere.
			for vid, other := range sec.Versions {
				if vid != moveTo && contains(other.Stages, stage) {
					other.Stages = remove(other.Stages, stage)
					sec.Versions[vid] = other
				}
			}
			if !contains(v.Stages, stage) {
				v.Stages = append(v.Stages, stage)
			}
			sec.Versions[moveTo] = v
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name}, nil
}

func (s *Server) tagResource(p map[string]any) (any, *awshttp.APIError) {
	tags := ptaglist(p, "Tags")
	_, err := s.store.Mutate(pstr(p, "SecretId"), func(sec *Secret) error {
		if sec.Tags == nil {
			sec.Tags = map[string]string{}
		}
		maps.Copy(sec.Tags, tags)
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) untagResource(p map[string]any) (any, *awshttp.APIError) {
	keys, _ := p["TagKeys"].([]any)
	_, err := s.store.Mutate(pstr(p, "SecretId"), func(sec *Secret) error {
		for _, k := range keys {
			if name, ok := k.(string); ok {
				delete(sec.Tags, name)
			}
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) getRandomPassword(p map[string]any) (any, *awshttp.APIError) {
	length := pint(p, "PasswordLength")
	if length == 0 {
		length = 32
	}
	if length < 1 || length > 4096 {
		return nil, awshttp.Errf(400, "InvalidParameterException", "PasswordLength must be 1-4096")
	}
	alphabet := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	if !pbool(p, "ExcludePunctuation") && !pbool(p, "ExcludeCharacters") {
		alphabet += "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
	}
	if exclude := pstr(p, "ExcludeCharacters"); exclude != "" {
		var b strings.Builder
		for _, r := range alphabet {
			if !strings.ContainsRune(exclude, r) {
				b.WriteRune(r)
			}
		}
		alphabet = b.String()
	}
	raw := make([]byte, length)
	rand.Read(raw)
	pw := make([]byte, length)
	for i, b := range raw {
		pw[i] = alphabet[int(b)%len(alphabet)]
	}
	return map[string]any{"RandomPassword": string(pw)}, nil
}

func (s *Server) putResourcePolicy(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Mutate(pstr(p, "SecretId"), func(sec *Secret) error {
		sec.Policy = pstr(p, "ResourcePolicy")
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name}, nil
}

func (s *Server) getResourcePolicy(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Get(pstr(p, "SecretId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{"ARN": sec.ARN, "Name": sec.Name}
	if sec.Policy != "" {
		out["ResourcePolicy"] = sec.Policy
	}
	return out, nil
}

func (s *Server) deleteResourcePolicy(p map[string]any) (any, *awshttp.APIError) {
	sec, err := s.store.Mutate(pstr(p, "SecretId"), func(sec *Secret) error {
		sec.Policy = ""
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ARN": sec.ARN, "Name": sec.Name}, nil
}

func (s *Server) validateResourcePolicy(p map[string]any) (any, *awshttp.APIError) {
	// No IAM locally: any syntactically-plausible JSON policy validates.
	return map[string]any{"PolicyValidationPassed": true, "ValidationErrors": []any{}}, nil
}
