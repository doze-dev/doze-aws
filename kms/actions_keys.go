package kms

// Key lifecycle actions: create/describe/list, enable/disable, deletion
// scheduling, description updates, and rotation.

import (
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// keyMetadata is the DescribeKey/CreateKey metadata shape.
type keyMetadata struct {
	AWSAccountId          string   `json:"AWSAccountId"`
	KeyId                 string   `json:"KeyId"`
	Arn                   string   `json:"Arn"`
	CreationDate          float64  `json:"CreationDate"`
	Enabled               bool     `json:"Enabled"`
	Description           string   `json:"Description"`
	KeyUsage              string   `json:"KeyUsage"`
	KeyState              string   `json:"KeyState"`
	DeletionDate          *float64 `json:"DeletionDate,omitempty"`
	Origin                string   `json:"Origin"`
	KeyManager            string   `json:"KeyManager"`
	KeySpec               string   `json:"KeySpec"`
	CustomerMasterKeySpec string   `json:"CustomerMasterKeySpec"` // legacy field, same value
	EncryptionAlgorithms  []string `json:"EncryptionAlgorithms,omitempty"`
	SigningAlgorithms     []string `json:"SigningAlgorithms,omitempty"`
	MacAlgorithms         []string `json:"MacAlgorithms,omitempty"`
	MultiRegion           bool     `json:"MultiRegion"`
}

func metadataFor(k *Key) keyMetadata {
	md := keyMetadata{
		AWSAccountId:          "000000000000",
		KeyId:                 k.ID,
		Arn:                   k.ARN(),
		CreationDate:          float64(k.Created),
		Enabled:               k.State == stateEnabled,
		Description:           k.Description,
		KeyUsage:              k.KeyUsage,
		KeyState:              k.State,
		Origin:                "AWS_KMS",
		KeyManager:            "CUSTOMER",
		KeySpec:               k.KeySpec,
		CustomerMasterKeySpec: k.KeySpec,
		EncryptionAlgorithms:  k.encryptionAlgorithms(),
		MultiRegion:           k.MultiRegion,
	}
	if k.KeyUsage == usageSignVerify {
		md.SigningAlgorithms = k.signingAlgorithms()
		md.EncryptionAlgorithms = nil
	}
	if alg, _ := k.macAlgorithm(); alg != "" {
		md.MacAlgorithms = []string{alg}
		md.EncryptionAlgorithms = nil
	}
	if k.DeletionAt > 0 {
		d := float64(k.DeletionAt)
		md.DeletionDate = &d
	}
	return md
}

// ---- key lifecycle ----

func (s *Server) createKey(p map[string]any) (any, *awshttp.APIError) {
	spec := awsjson.Str(p, "KeySpec")
	if spec == "" {
		spec = awsjson.Str(p, "CustomerMasterKeySpec") // legacy parameter name
	}
	k, err := s.store.CreateKey(spec, awsjson.Str(p, "KeyUsage"), awsjson.Str(p, "Description"), awsjson.Str(p, "Policy"), ptags(p, "Tags"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyMetadata": metadataFor(k)}, nil
}

func (s *Server) describeKey(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyMetadata": metadataFor(k)}, nil
}

func (s *Server) listKeys(map[string]any) (any, *awshttp.APIError) {
	keys, err := s.store.List()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type entry struct {
		KeyId  string `json:"KeyId"`
		KeyArn string `json:"KeyArn"`
	}
	out := []entry{}
	for _, k := range keys {
		out = append(out, entry{KeyId: k.ID, KeyArn: k.ARN()})
	}
	return map[string]any{"Keys": out, "Truncated": false}, nil
}

func (s *Server) enableKey(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State == statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is pending deletion", k.ID)
		}
		k.State = stateEnabled
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) disableKey(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State == statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is pending deletion", k.ID)
		}
		k.State = stateDisabled
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) scheduleKeyDeletion(p map[string]any) (any, *awshttp.APIError) {
	days := awsjson.Int(p, "PendingWindowInDays", 30)
	if days < 7 || days > 30 {
		return nil, awshttp.Errf(400, "ValidationException", "PendingWindowInDays must be between 7 and 30, got %d", days)
	}
	var when time.Time
	k, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		when = s.store.now().Add(time.Duration(days) * 24 * time.Hour)
		k.State = statePendingDeletion
		k.DeletionAt = when.Unix()
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"KeyId":               k.ARN(),
		"DeletionDate":        float64(when.Unix()),
		"KeyState":            statePendingDeletion,
		"PendingWindowInDays": days,
	}, nil
}

func (s *Server) cancelKeyDeletion(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.State != statePendingDeletion {
			return awshttp.Errf(400, "KMSInvalidStateException", "key %s is not pending deletion", k.ID)
		}
		k.State = stateDisabled // AWS leaves a cancelled key disabled
		k.DeletionAt = 0
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyId": k.ARN()}, nil
}

func (s *Server) updateKeyDescription(p map[string]any) (any, *awshttp.APIError) {
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		k.Description = awsjson.Str(p, "Description")
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

// ---- rotation flags ----

func (s *Server) getKeyRotationStatus(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"KeyRotationEnabled": k.RotationOn}, nil
}

func (s *Server) enableKeyRotation(p map[string]any) (any, *awshttp.APIError) {
	return s.setRotation(p, true)
}

func (s *Server) disableKeyRotation(p map[string]any) (any, *awshttp.APIError) {
	return s.setRotation(p, false)
}

func (s *Server) setRotation(p map[string]any, on bool) (any, *awshttp.APIError) {
	_, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.KeySpec != "SYMMETRIC_DEFAULT" {
			return awshttp.Errf(400, "UnsupportedOperationException", "rotation applies to symmetric keys only")
		}
		k.RotationOn = on
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

// rotateKeyOnDemand generates fresh backing material for a symmetric key,
// retiring the old material (kept so pre-rotation ciphertexts still decrypt) and
// recording the rotation time. New encryptions use the new material.
func (s *Server) rotateKeyOnDemand(p map[string]any) (any, *awshttp.APIError) {
	key, err := s.store.Update(awsjson.Str(p, "KeyId"), func(k *Key) *awshttp.APIError {
		if k.KeySpec != "SYMMETRIC_DEFAULT" {
			return awshttp.Errf(400, "UnsupportedOperationException", "on-demand rotation applies to symmetric keys only")
		}
		if k.State != "Enabled" {
			return awshttp.Errf(400, "KMSInvalidStateException", "key is not enabled")
		}
		mat, gerr := generateMaterial(k.KeySpec)
		if gerr != nil {
			return awshttp.Errf(500, "KMSInternalException", "generating key material: %v", gerr)
		}
		k.OldMaterials = append([][]byte{k.Material}, k.OldMaterials...)
		k.Material = mat
		k.Rotations = append([]int64{s.store.now().Unix()}, k.Rotations...)
		return nil
	})
	if e := awshttp.AsAPIErrorOrNil(err); e != nil {
		return nil, e
	}
	return map[string]any{"KeyId": key.ID}, nil
}

// listKeyRotations returns the key's on-demand rotation history, newest first.
func (s *Server) listKeyRotations(p map[string]any) (any, *awshttp.APIError) {
	k, err := s.store.Resolve(awsjson.Str(p, "KeyId"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	rotations := make([]map[string]any, 0, len(k.Rotations))
	for _, ts := range k.Rotations {
		rotations = append(rotations, map[string]any{
			"KeyId":        k.ID,
			"RotationDate": ts,
			"RotationType": "ON_DEMAND",
		})
	}
	return map[string]any{"Rotations": rotations}, nil
}
