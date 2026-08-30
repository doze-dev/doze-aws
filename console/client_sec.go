package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/doze-dev/doze-aws/awsident"
	"sort"
	"strings"
	"time"
)

// ---- KMS (JSON 1.1, TrentService) ----

type Key struct {
	ID          string
	ARN         string
	Alias       string
	Description string
	Spec        string
	Usage       string
	State       string
	Enabled     bool
	RotationOn  bool
	Created     string
	SigAlgos    []string // SIGN_VERIFY keys
	MacAlgos    []string // GENERATE_VERIFY_MAC keys
	Aliases     []string
}

func (b *backend) ListKeys(ctx context.Context) ([]Key, error) {
	body, err := b.json11(ctx, "TrentService", "ListKeys", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []struct {
			KeyId string `json:"KeyId"`
		} `json:"Keys"`
	}
	json.Unmarshal(body, &out)

	aliases := map[string]string{}
	if ab, err := b.json11(ctx, "TrentService", "ListAliases", map[string]any{}); err == nil {
		var al struct {
			Aliases []struct {
				AliasName   string `json:"AliasName"`
				TargetKeyId string `json:"TargetKeyId"`
			} `json:"Aliases"`
		}
		json.Unmarshal(ab, &al)
		for _, a := range al.Aliases {
			aliases[a.TargetKeyId] = strings.TrimPrefix(a.AliasName, "alias/")
		}
	}

	keys := make([]Key, 0, len(out.Keys))
	for _, k := range out.Keys {
		key, err := b.DescribeKey(ctx, k.KeyId)
		if err != nil {
			continue
		}
		key.Alias = aliases[key.ID]
		keys = append(keys, *key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Alias != keys[j].Alias {
			return keys[i].Alias < keys[j].Alias
		}
		return keys[i].ID < keys[j].ID
	})
	return keys, nil
}

// CountKeys is the cheap cardinality probe: one ListKeys call, no per-key
// describes or alias lookups.
func (b *backend) CountKeys(ctx context.Context) (int, error) {
	body, err := b.json11(ctx, "TrentService", "ListKeys", map[string]any{})
	if err != nil {
		return 0, err
	}
	var out struct {
		Keys []struct{} `json:"Keys"`
	}
	json.Unmarshal(body, &out)
	return len(out.Keys), nil
}

func (b *backend) DescribeKey(ctx context.Context, id string) (*Key, error) {
	body, err := b.json11(ctx, "TrentService", "DescribeKey", map[string]any{"KeyId": id})
	if err != nil {
		return nil, err
	}
	var out struct {
		KeyMetadata struct {
			KeyId             string   `json:"KeyId"`
			Arn               string   `json:"Arn"`
			Description       string   `json:"Description"`
			KeySpec           string   `json:"KeySpec"`
			KeyUsage          string   `json:"KeyUsage"`
			KeyState          string   `json:"KeyState"`
			Enabled           bool     `json:"Enabled"`
			CreationDate      float64  `json:"CreationDate"`
			SigningAlgorithms []string `json:"SigningAlgorithms"`
			MacAlgorithms     []string `json:"MacAlgorithms"`
		} `json:"KeyMetadata"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	m := out.KeyMetadata
	k := &Key{
		ID: m.KeyId, ARN: m.Arn, Description: m.Description,
		Spec: m.KeySpec, Usage: m.KeyUsage, State: m.KeyState, Enabled: m.Enabled,
		SigAlgos: m.SigningAlgorithms, MacAlgos: m.MacAlgorithms,
	}
	// Aliases pointing at this key (the list pane and header prefer an alias).
	if ab, err := b.json11(ctx, "TrentService", "ListAliases", map[string]any{"KeyId": m.KeyId}); err == nil {
		var al struct {
			Aliases []struct {
				AliasName string `json:"AliasName"`
			} `json:"Aliases"`
		}
		json.Unmarshal(ab, &al)
		for _, a := range al.Aliases {
			k.Aliases = append(k.Aliases, a.AliasName)
		}
	}
	if m.CreationDate > 0 {
		k.Created = time.Unix(int64(m.CreationDate), 0).Local().Format("2006-01-02 15:04")
	}
	if rb, err := b.json11(ctx, "TrentService", "GetKeyRotationStatus", map[string]any{"KeyId": m.KeyId}); err == nil {
		var rs struct {
			KeyRotationEnabled bool `json:"KeyRotationEnabled"`
		}
		json.Unmarshal(rb, &rs)
		k.RotationOn = rs.KeyRotationEnabled
	}
	return k, nil
}

// CreateKey creates a key (plus optional alias) and returns the new key id.
func (b *backend) CreateKey(ctx context.Context, spec, usage, alias, description string) (string, error) {
	in := map[string]any{}
	if spec != "" {
		in["KeySpec"] = spec
	}
	if usage != "" {
		in["KeyUsage"] = usage
	}
	if description != "" {
		in["Description"] = description
	}
	body, err := b.json11(ctx, "TrentService", "CreateKey", in)
	if err != nil {
		return "", err
	}
	var out struct {
		KeyMetadata struct {
			KeyId string `json:"KeyId"`
		} `json:"KeyMetadata"`
	}
	json.Unmarshal(body, &out)
	if alias != "" {
		if _, err := b.json11(ctx, "TrentService", "CreateAlias", map[string]any{
			"AliasName": "alias/" + alias, "TargetKeyId": out.KeyMetadata.KeyId,
		}); err != nil {
			return "", err
		}
	}
	return out.KeyMetadata.KeyId, nil
}

func (b *backend) SetKeyEnabled(ctx context.Context, id string, enable bool) error {
	action := "DisableKey"
	if enable {
		action = "EnableKey"
	}
	_, err := b.json11(ctx, "TrentService", action, map[string]any{"KeyId": id})
	return err
}

func (b *backend) SetKeyRotation(ctx context.Context, id string, on bool) error {
	action := "DisableKeyRotation"
	if on {
		action = "EnableKeyRotation"
	}
	_, err := b.json11(ctx, "TrentService", action, map[string]any{"KeyId": id})
	return err
}

func (b *backend) RotateKeyNow(ctx context.Context, id string) error {
	_, err := b.json11(ctx, "TrentService", "RotateKeyOnDemand", map[string]any{"KeyId": id})
	return err
}

func (b *backend) ScheduleKeyDeletion(ctx context.Context, id string) error {
	_, err := b.json11(ctx, "TrentService", "ScheduleKeyDeletion", map[string]any{"KeyId": id, "PendingWindowInDays": 7})
	return err
}

// KMSEncrypt encrypts plaintext, returning base64 ciphertext.
func (b *backend) KMSEncrypt(ctx context.Context, id, plaintext string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "Encrypt", map[string]any{
		"KeyId": id, "Plaintext": base64.StdEncoding.EncodeToString([]byte(plaintext)),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
	json.Unmarshal(body, &out)
	return out.CiphertextBlob, nil
}

// KMSDecrypt decrypts base64 ciphertext, returning plaintext.
// KMSSign signs a message and returns the base64 signature.
func (b *backend) KMSSign(ctx context.Context, id, algo, message string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "Sign", map[string]any{
		"KeyId": id, "SigningAlgorithm": algo, "MessageType": "RAW",
		"Message": base64.StdEncoding.EncodeToString([]byte(message)),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Signature string `json:"Signature"`
	}
	json.Unmarshal(body, &out)
	return out.Signature, nil
}

// KMSVerify checks a signature — a non-error return means valid.
func (b *backend) KMSVerify(ctx context.Context, id, algo, message, signature string) error {
	_, err := b.json11(ctx, "TrentService", "Verify", map[string]any{
		"KeyId": id, "SigningAlgorithm": algo, "MessageType": "RAW",
		"Message":   base64.StdEncoding.EncodeToString([]byte(message)),
		"Signature": strings.TrimSpace(signature),
	})
	return err
}

// KMSGenerateMac returns the base64 HMAC of a message.
func (b *backend) KMSGenerateMac(ctx context.Context, id, algo, message string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "GenerateMac", map[string]any{
		"KeyId": id, "MacAlgorithm": algo,
		"Message": base64.StdEncoding.EncodeToString([]byte(message)),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Mac string `json:"Mac"`
	}
	json.Unmarshal(body, &out)
	return out.Mac, nil
}

// KMSVerifyMac checks an HMAC — a non-error return means valid.
func (b *backend) KMSVerifyMac(ctx context.Context, id, algo, message, mac string) error {
	_, err := b.json11(ctx, "TrentService", "VerifyMac", map[string]any{
		"KeyId": id, "MacAlgorithm": algo,
		"Message": base64.StdEncoding.EncodeToString([]byte(message)),
		"Mac":     strings.TrimSpace(mac),
	})
	return err
}

// KMSAddAlias / KMSDeleteAlias / KMSCancelDeletion round out key management.
func (b *backend) KMSAddAlias(ctx context.Context, id, alias string) error {
	if !strings.HasPrefix(alias, "alias/") {
		alias = "alias/" + alias
	}
	_, err := b.json11(ctx, "TrentService", "CreateAlias", map[string]any{
		"AliasName": alias, "TargetKeyId": id,
	})
	return err
}

func (b *backend) KMSDeleteAlias(ctx context.Context, alias string) error {
	_, err := b.json11(ctx, "TrentService", "DeleteAlias", map[string]any{"AliasName": alias})
	return err
}

func (b *backend) KMSCancelDeletion(ctx context.Context, id string) error {
	_, err := b.json11(ctx, "TrentService", "CancelKeyDeletion", map[string]any{"KeyId": id})
	return err
}

func (b *backend) KMSDecrypt(ctx context.Context, ciphertext string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "Decrypt", map[string]any{
		"CiphertextBlob": strings.TrimSpace(ciphertext),
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Plaintext string `json:"Plaintext"`
	}
	json.Unmarshal(body, &out)
	pt, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ---- SSM Parameter Store (JSON 1.1, AmazonSSM) ----

type Parameter struct {
	Name     string
	Type     string
	Value    string
	Version  int64
	ARN      string
	Modified string
	Labels   []string
}

func (b *backend) ListParameters(ctx context.Context) ([]Parameter, error) {
	body, err := b.json11(ctx, "AmazonSSM", "DescribeParameters", map[string]any{"MaxResults": 50})
	if err != nil {
		return nil, err
	}
	var out struct {
		Parameters []struct {
			Name             string  `json:"Name"`
			Type             string  `json:"Type"`
			Version          int64   `json:"Version"`
			LastModifiedDate float64 `json:"LastModifiedDate"`
		} `json:"Parameters"`
	}
	json.Unmarshal(body, &out)
	params := make([]Parameter, 0, len(out.Parameters))
	for _, p := range out.Parameters {
		params = append(params, Parameter{
			Name: p.Name, Type: p.Type, Version: p.Version,
			Modified: epochToTime(p.LastModifiedDate),
		})
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	return params, nil
}

func (b *backend) GetParameter(ctx context.Context, name string) (*Parameter, error) {
	body, err := b.json11(ctx, "AmazonSSM", "GetParameter", map[string]any{
		"Name": name, "WithDecryption": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Parameter struct {
			Name             string  `json:"Name"`
			Type             string  `json:"Type"`
			Value            string  `json:"Value"`
			Version          int64   `json:"Version"`
			ARN              string  `json:"ARN"`
			LastModifiedDate float64 `json:"LastModifiedDate"`
		} `json:"Parameter"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	p := out.Parameter
	return &Parameter{
		Name: p.Name, Type: p.Type, Value: p.Value, Version: p.Version, ARN: p.ARN,
		Modified: epochToTime(p.LastModifiedDate),
	}, nil
}

func (b *backend) ParameterHistory(ctx context.Context, name string) ([]Parameter, error) {
	body, err := b.json11(ctx, "AmazonSSM", "GetParameterHistory", map[string]any{
		"Name": name, "WithDecryption": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Parameters []struct {
			Type             string   `json:"Type"`
			Value            string   `json:"Value"`
			Version          int64    `json:"Version"`
			LastModifiedDate float64  `json:"LastModifiedDate"`
			Labels           []string `json:"Labels"`
		} `json:"Parameters"`
	}
	json.Unmarshal(body, &out)
	hist := make([]Parameter, 0, len(out.Parameters))
	for _, p := range out.Parameters {
		hist = append(hist, Parameter{
			Type: p.Type, Value: p.Value, Version: p.Version, Modified: epochToTime(p.LastModifiedDate), Labels: p.Labels,
		})
	}
	sort.Slice(hist, func(i, j int) bool { return hist[i].Version > hist[j].Version })
	return hist, nil
}

func (b *backend) PutParameter(ctx context.Context, name, value, typ string, overwrite bool) error {
	_, err := b.json11(ctx, "AmazonSSM", "PutParameter", map[string]any{
		"Name": name, "Value": value, "Type": typ, "Overwrite": overwrite,
	})
	return err
}

// LabelParameter attaches a label to a parameter version (or its latest).
// UnlabelParameter detaches a label from a version. Labels were add-only from
// the console — a mistyped "prod" could only be buried under a second label,
// never removed, though the API has always taken both directions.
func (b *backend) UnlabelParameter(ctx context.Context, name, label string, version int) error {
	_, err := b.json11(ctx, "AmazonSSM", "UnlabelParameterVersion", map[string]any{
		"Name": name, "Labels": []string{label}, "ParameterVersion": version,
	})
	return err
}

func (b *backend) LabelParameter(ctx context.Context, name, label string, version int) error {
	in := map[string]any{"Name": name, "Labels": []string{label}}
	if version > 0 {
		in["ParameterVersion"] = version
	}
	_, err := b.json11(ctx, "AmazonSSM", "LabelParameterVersion", in)
	return err
}

func (b *backend) DeleteParameter(ctx context.Context, name string) error {
	_, err := b.json11(ctx, "AmazonSSM", "DeleteParameter", map[string]any{"Name": name})
	return err
}

// ---- Secrets Manager (JSON 1.1, secretsmanager) ----

type Secret struct {
	Name           string
	ARN            string
	Description    string
	Changed        string
	Value          string
	VersionID      string
	Stages         map[string][]string // version id -> stages
	Deleted        bool                // pending deletion (restorable)
	DeletedAt      string
	RotationOn     bool
	RotationLambda string
	RotationDays   int
	LastRotated    string
}

func (b *backend) ListSecrets(ctx context.Context) ([]Secret, error) {
	// IncludePlannedDeletion keeps soft-deleted secrets visible — restoring
	// them is the recovery window's whole purpose.
	body, err := b.json11(ctx, "secretsmanager", "ListSecrets", map[string]any{"IncludePlannedDeletion": true})
	if err != nil {
		return nil, err
	}
	var out struct {
		SecretList []struct {
			Name            string  `json:"Name"`
			ARN             string  `json:"ARN"`
			Description     string  `json:"Description"`
			LastChangedDate float64 `json:"LastChangedDate"`
			DeletedDate     float64 `json:"DeletedDate"`
		} `json:"SecretList"`
	}
	json.Unmarshal(body, &out)
	secrets := make([]Secret, 0, len(out.SecretList))
	for _, s := range out.SecretList {
		secrets = append(secrets, Secret{
			Name: s.Name, ARN: s.ARN, Description: s.Description,
			Changed: epochToTime(s.LastChangedDate),
			Deleted: s.DeletedDate > 0, DeletedAt: epochToTime(s.DeletedDate),
		})
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Name < secrets[j].Name })
	return secrets, nil
}

// GetRandomPassword generates a password via the server (used to fill create /
// new-version forms).
func (b *backend) GetRandomPassword(ctx context.Context, length int) (string, error) {
	if length <= 0 {
		length = 24
	}
	body, err := b.json11(ctx, "secretsmanager", "GetRandomPassword", map[string]any{
		"PasswordLength": length, "ExcludePunctuation": false,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		RandomPassword string `json:"RandomPassword"`
	}
	json.Unmarshal(body, &out)
	return out.RandomPassword, nil
}

// ConfigureRotation sets or clears the rotation lambda + schedule.
func (b *backend) ConfigureRotation(ctx context.Context, id, lambdaName string, days int) error {
	in := map[string]any{"SecretId": id}
	if lambdaName == "" { // clear rotation
		_, err := b.json11(ctx, "secretsmanager", "CancelRotateSecret", in)
		return err
	}
	in["RotationLambdaARN"] = "arn:aws:lambda:" + awsident.Region + ":" + awsident.AccountID + ":function:" + lambdaName
	if days <= 0 {
		days = 30
	}
	in["RotationRules"] = map[string]any{"AutomaticallyAfterDays": days}
	_, err := b.json11(ctx, "secretsmanager", "RotateSecret", in)
	return err
}

// RotateNow triggers an immediate rotation (runs the configured lambda).
func (b *backend) RotateNow(ctx context.Context, id string) error {
	_, err := b.json11(ctx, "secretsmanager", "RotateSecret", map[string]any{"SecretId": id})
	return err
}

// RestoreSecret cancels a pending deletion within the recovery window.
func (b *backend) RestoreSecret(ctx context.Context, id string) error {
	_, err := b.json11(ctx, "secretsmanager", "RestoreSecret", map[string]any{"SecretId": id})
	return err
}

func (b *backend) GetSecret(ctx context.Context, id string) (*Secret, error) {
	body, err := b.json11(ctx, "secretsmanager", "DescribeSecret", map[string]any{"SecretId": id})
	if err != nil {
		return nil, err
	}
	var desc struct {
		Name              string  `json:"Name"`
		ARN               string  `json:"ARN"`
		Description       string  `json:"Description"`
		LastChangedDate   float64 `json:"LastChangedDate"`
		DeletedDate       float64 `json:"DeletedDate"`
		LastRotatedDate   float64 `json:"LastRotatedDate"`
		RotationEnabled   bool    `json:"RotationEnabled"`
		RotationLambdaARN string  `json:"RotationLambdaARN"`
		RotationRules     struct {
			AutomaticallyAfterDays int `json:"AutomaticallyAfterDays"`
		} `json:"RotationRules"`
		VersionIdsToStages map[string][]string `json:"VersionIdsToStages"`
	}
	if err := json.Unmarshal(body, &desc); err != nil {
		return nil, err
	}
	s := &Secret{
		Name: desc.Name, ARN: desc.ARN, Description: desc.Description,
		Changed: epochToTime(desc.LastChangedDate), Stages: desc.VersionIdsToStages,
		Deleted: desc.DeletedDate > 0, DeletedAt: epochToTime(desc.DeletedDate),
		RotationOn: desc.RotationEnabled, RotationLambda: arnLeaf(desc.RotationLambdaARN),
		RotationDays: desc.RotationRules.AutomaticallyAfterDays, LastRotated: epochToTime(desc.LastRotatedDate),
	}
	if vb, err := b.json11(ctx, "secretsmanager", "GetSecretValue", map[string]any{"SecretId": id}); err == nil {
		var val struct {
			SecretString string `json:"SecretString"`
			VersionId    string `json:"VersionId"`
		}
		json.Unmarshal(vb, &val)
		s.Value, s.VersionID = val.SecretString, val.VersionId
	}
	return s, nil
}

// GetSecretVersion fetches a specific version's value (for the Versions diff).
func (b *backend) GetSecretVersion(ctx context.Context, id, versionID string) (string, error) {
	in := map[string]any{"SecretId": id}
	if versionID != "" {
		in["VersionId"] = versionID
	}
	body, err := b.json11(ctx, "secretsmanager", "GetSecretValue", in)
	if err != nil {
		return "", err
	}
	var out struct {
		SecretString string `json:"SecretString"`
	}
	json.Unmarshal(body, &out)
	return out.SecretString, nil
}

func (b *backend) CreateSecret(ctx context.Context, name, value, description string) error {
	in := map[string]any{"Name": name, "SecretString": value}
	if description != "" {
		in["Description"] = description
	}
	_, err := b.json11(ctx, "secretsmanager", "CreateSecret", in)
	return err
}

func (b *backend) PutSecretValue(ctx context.Context, id, value string) error {
	_, err := b.json11(ctx, "secretsmanager", "PutSecretValue", map[string]any{
		"SecretId": id, "SecretString": value,
	})
	return err
}

func (b *backend) DeleteSecret(ctx context.Context, id string, force bool) error {
	in := map[string]any{"SecretId": id}
	if force {
		in["ForceDeleteWithoutRecovery"] = true
	} else {
		in["RecoveryWindowInDays"] = 7
	}
	_, err := b.json11(ctx, "secretsmanager", "DeleteSecret", in)
	return err
}

func epochToTime(f float64) string {
	if f <= 0 {
		return ""
	}
	return time.Unix(int64(f), 0).Local().Format("2006-01-02 15:04")
}

// PromoteSecretVersion moves AWSCURRENT to an older version — the rollback.
//
// This is the operation you want at 3am and the console could not do at all.
// Secrets Manager has no "undo": PutSecretValue makes a new version current and
// demotes the old one to AWSPREVIOUS, and going back means moving the stage
// label yourself. Doing that from the CLI needs both version ids in hand, which
// is why it never got done under pressure.
//
// RemoveFromVersionId is required by AWS when the stage is already attached
// somewhere, and omitting it is the usual way this call fails.
func (b *backend) PromoteSecretVersion(ctx context.Context, id, toVersion, fromVersion string) error {
	in := map[string]any{
		"SecretId": id, "VersionStage": "AWSCURRENT", "MoveToVersionId": toVersion,
	}
	if fromVersion != "" {
		in["RemoveFromVersionId"] = fromVersion
	}
	_, err := b.json11(ctx, "secretsmanager", "UpdateSecretVersionStage", in)
	return err
}

// UpdateSecretMeta changes a secret's description and KMS key without writing a
// new version. PutSecretValue creates a version every time; changing the
// description through it would leave a version history full of entries where
// nothing about the secret changed.
func (b *backend) UpdateSecretMeta(ctx context.Context, id, description, kmsKeyID string) error {
	in := map[string]any{"SecretId": id, "Description": description}
	if kmsKeyID != "" {
		in["KmsKeyId"] = kmsKeyID
	}
	_, err := b.json11(ctx, "secretsmanager", "UpdateSecret", in)
	return err
}

// SecretVersionIDs lists every version, INCLUDING the deprecated ones that
// carry no stage label any more.
//
// DescribeSecret's VersionIdsToStages — what the versions tab used — only shows
// versions that still hold a stage, so the history appeared to be two entries
// deep however many times a secret had been rotated. The deprecated versions
// are the history.
func (b *backend) SecretVersionIDs(ctx context.Context, id string) (map[string][]string, error) {
	body, err := b.json11(ctx, "secretsmanager", "ListSecretVersionIds", map[string]any{
		"SecretId": id, "IncludeDeprecated": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Versions []struct {
			VersionID     string   `json:"VersionId"`
			VersionStages []string `json:"VersionStages"`
		} `json:"Versions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	stages := make(map[string][]string, len(out.Versions))
	for _, v := range out.Versions {
		stages[v.VersionID] = v.VersionStages
	}
	return stages, nil
}

// UpdateKeyDescription renames a key's description after creation.
//
// The description was set-once: the create form offered it and nothing could
// change it afterwards, though UpdateKeyDescription has always been there. A
// key's description is the only human-readable thing about it — the id is a
// UUID and the alias is optional — so being unable to correct one is worse
// than it sounds.
func (b *backend) UpdateKeyDescription(ctx context.Context, keyID, description string) error {
	_, err := b.json11(ctx, "TrentService", "UpdateKeyDescription", map[string]any{
		"KeyId": keyID, "Description": description,
	})
	return err
}

// GenerateRandom returns bytes from the service rather than from the console.
//
// It looks like a toy and is not: it is the one KMS call that needs no key, so
// it is what you reach for to check the endpoint is answering and your
// credentials resolve, before blaming a key policy for a failure that is really
// a misconfigured endpoint.
func (b *backend) GenerateRandom(ctx context.Context, bytes int) (string, error) {
	body, err := b.json11(ctx, "TrentService", "GenerateRandom", map[string]any{
		"NumberOfBytes": bytes,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Plaintext string `json:"Plaintext"`
	}
	json.Unmarshal(body, &out)
	return out.Plaintext, nil
}

// PublicKey exports the public half of an asymmetric key.
//
// The console could sign and verify and could not hand you the key a peer needs
// to verify independently — which is the entire point of signing with an
// asymmetric key rather than a MAC.
func (b *backend) PublicKey(ctx context.Context, keyID string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "GetPublicKey", map[string]any{"KeyId": keyID})
	if err != nil {
		return "", err
	}
	var out struct {
		PublicKey string `json:"PublicKey"`
	}
	json.Unmarshal(body, &out)
	return out.PublicKey, nil
}

// ReEncrypt moves ciphertext from one key to another without the plaintext ever
// coming back to the caller. Doing it as Decrypt-then-Encrypt in the console
// would work and would be the wrong demonstration: the reason ReEncrypt exists
// is that the plaintext does not travel.
func (b *backend) ReEncrypt(ctx context.Context, ciphertext, destKeyID string) (string, error) {
	body, err := b.json11(ctx, "TrentService", "ReEncrypt", map[string]any{
		"CiphertextBlob": ciphertext, "DestinationKeyId": destKeyID,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
	json.Unmarshal(body, &out)
	return out.CiphertextBlob, nil
}

// UpdateAlias repoints an existing alias at another key. Without it the console
// could only delete and re-create, which is a different thing: an alias is
// briefly absent in between, and anything resolving it at that moment fails.
func (b *backend) UpdateAlias(ctx context.Context, alias, targetKeyID string) error {
	if !strings.HasPrefix(alias, "alias/") {
		alias = "alias/" + alias
	}
	_, err := b.json11(ctx, "TrentService", "UpdateAlias", map[string]any{
		"AliasName": alias, "TargetKeyId": targetKeyID,
	})
	return err
}

// ParametersByPath lists everything under a path prefix, recursively, using
// the service's own tree query. The console's list-pane tree is built
// client-side from DescribeParameters; this is the API your code would use,
// and the enumeration step behind "delete this whole path".
func (b *backend) ParametersByPath(ctx context.Context, path string) ([]Parameter, error) {
	body, err := b.json11(ctx, "AmazonSSM", "GetParametersByPath", map[string]any{
		"Path": path, "Recursive": true, "WithDecryption": false,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Parameters []struct {
			Name    string `json:"Name"`
			Type    string `json:"Type"`
			Value   string `json:"Value"`
			Version int64  `json:"Version"`
		} `json:"Parameters"`
	}
	json.Unmarshal(body, &out)
	ps := make([]Parameter, 0, len(out.Parameters))
	for _, p := range out.Parameters {
		ps = append(ps, Parameter{Name: p.Name, Type: p.Type, Value: p.Value, Version: p.Version})
	}
	return ps, nil
}

// DeleteParameters removes up to fifty parameters per call and reports which
// of them did not exist — a batch API with partial results, like the SQS ones,
// and worth the same honesty about what actually happened.
func (b *backend) DeleteParameters(ctx context.Context, names []string) (deleted, invalid []string, err error) {
	for start := 0; start < len(names); start += 50 {
		end := min(start+50, len(names))
		body, e := b.json11(ctx, "AmazonSSM", "DeleteParameters", map[string]any{
			"Names": names[start:end],
		})
		if e != nil {
			return deleted, invalid, e
		}
		var out struct {
			DeletedParameters []string `json:"DeletedParameters"`
			InvalidParameters []string `json:"InvalidParameters"`
		}
		json.Unmarshal(body, &out)
		deleted = append(deleted, out.DeletedParameters...)
		invalid = append(invalid, out.InvalidParameters...)
	}
	return deleted, invalid, nil
}
