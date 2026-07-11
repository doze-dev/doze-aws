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
