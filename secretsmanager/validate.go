package secretsmanager

// Secrets Manager's model-derived input validation: the constraint tables,
// walked by internal/modelcheck.
//
// Generated from AWS's own service model with `dzaudit cases secrets-manager`
// rather than transcribed, and replayed case by case in
// secretsmanager/rejection_parity_test.go.

import (
	"regexp"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"BatchGetSecretValue": {
		{Path: "Filters[].Key", Kind: modelcheck.KindEnum, Enum: []string{"description", "name", "tag-key", "tag-value", "primary-region", "owning-service", "all"}},
		{Path: "Filters[].Values[]", Kind: modelcheck.KindLength, Min: 0, Max: 512},
		{Path: "Filters[].Values[]", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^\!?[a-zA-Z0-9 :_@\/\+\=\.\-\!]*$`)},
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 20},
		{Path: "NextToken", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "SecretIdList[]", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"CancelRotateSecret": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"CreateSecret": {
		{Path: "AddReplicaRegions[].KmsKeyId", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "AddReplicaRegions[].Region", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "AddReplicaRegions[].Region", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^([a-z]+-)+\d+$`)},
		{Path: "ClientRequestToken", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "Description", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "KmsKeyId", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 512},
		{Path: "Name", Kind: modelcheck.KindRequired},
		{Path: "SecretBinary", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "SecretString", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Type", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"DeleteResourcePolicy": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"DeleteSecret": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"DescribeSecret": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"GetRandomPassword": {
		{Path: "ExcludeCharacters", Kind: modelcheck.KindLength, Min: 0, Max: 4096},
		{Path: "PasswordLength", Kind: modelcheck.KindRange, Min: 1, Max: 4096},
	},
	"GetResourcePolicy": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"GetSecretValue": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "VersionId", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "VersionStage", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"ListSecretVersionIds": {
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 100},
		{Path: "NextToken", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"ListSecrets": {
		{Path: "Filters[].Key", Kind: modelcheck.KindEnum, Enum: []string{"description", "name", "tag-key", "tag-value", "primary-region", "owning-service", "all"}},
		{Path: "Filters[].Values[]", Kind: modelcheck.KindLength, Min: 0, Max: 512},
		{Path: "Filters[].Values[]", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^\!?[a-zA-Z0-9 :_@\/\+\=\.\-\!]*$`)},
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 100},
		{Path: "NextToken", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "SortBy", Kind: modelcheck.KindEnum, Enum: []string{"last-changed-date", "name", "created-date", "last-accessed-date"}},
		{Path: "SortOrder", Kind: modelcheck.KindEnum, Enum: []string{"asc", "desc"}},
	},
	"PutResourcePolicy": {
		{Path: "ResourcePolicy", Kind: modelcheck.KindLength, Min: 1, Max: 20480},
		{Path: "ResourcePolicy", Kind: modelcheck.KindRequired},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"PutSecretValue": {
		{Path: "ClientRequestToken", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "RotationToken", Kind: modelcheck.KindLength, Min: 36, Max: 256},
		{Path: "RotationToken", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9\-]+$`)},
		{Path: "SecretBinary", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "SecretString", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "VersionStages[]", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"RestoreSecret": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"RotateSecret": {
		{Path: "ClientRequestToken", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "ExternalSecretRotationMetadata[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "ExternalSecretRotationMetadata[].Value", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "ExternalSecretRotationRoleArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "RotationLambdaARN", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "RotationRules.AutomaticallyAfterDays", Kind: modelcheck.KindRange, Min: 1, Max: 1000},
		{Path: "RotationRules.Duration", Kind: modelcheck.KindLength, Min: 2, Max: 3},
		{Path: "RotationRules.Duration", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[0-9]+h$`)},
		{Path: "RotationRules.ScheduleExpression", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "RotationRules.ScheduleExpression", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[0-9A-Za-z\(\)#\?\*\-\/, ]+$`)},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
	},
	"TagResource": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "Tags", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"UntagResource": {
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
		{Path: "TagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
	},
	"UpdateSecret": {
		{Path: "ClientRequestToken", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "Description", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "KmsKeyId", Kind: modelcheck.KindLength, Min: 0, Max: 2048},
		{Path: "SecretBinary", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "SecretString", Kind: modelcheck.KindLength, Min: 1, Max: 65536},
		{Path: "Type", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"UpdateSecretVersionStage": {
		{Path: "MoveToVersionId", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "RemoveFromVersionId", Kind: modelcheck.KindLength, Min: 32, Max: 64},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "SecretId", Kind: modelcheck.KindRequired},
		{Path: "VersionStage", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "VersionStage", Kind: modelcheck.KindRequired},
	},
	"ValidateResourcePolicy": {
		{Path: "ResourcePolicy", Kind: modelcheck.KindLength, Min: 1, Max: 20480},
		{Path: "ResourcePolicy", Kind: modelcheck.KindRequired},
		{Path: "SecretId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
}
