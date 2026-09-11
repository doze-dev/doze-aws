package sts

// STS's model-derived input validation: the constraint tables, walked by
// internal/modelcheck.
//
// STS speaks the Query protocol, so the form is un-flattened into the nested
// shape the paths describe (modelcheck.FromQuery) before the walk. Generated
// from AWS's own service model with `dzaudit cases sts`, and replayed case by
// case in sts/rejection_parity_test.go.

import (
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"AssumeRole": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 900, Max: 43200},
		{Path: "ExternalId", Kind: modelcheck.KindLength, Min: 2, Max: 1224},
		{Path: "ExternalId", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=,.@:\/-]*$`)},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: modelcheck.NoMax},
		{Path: "PolicyArns[].arn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "ProvidedContexts[].ContextAssertion", Kind: modelcheck.KindLength, Min: 4, Max: 2048},
		{Path: "ProvidedContexts[].ProviderArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "RoleArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "RoleArn", Kind: modelcheck.KindRequired},
		{Path: "RoleSessionName", Kind: modelcheck.KindLength, Min: 2, Max: 64},
		{Path: "RoleSessionName", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=,.@-]*$`)},
		{Path: "RoleSessionName", Kind: modelcheck.KindRequired},
		{Path: "SerialNumber", Kind: modelcheck.KindLength, Min: 9, Max: 256},
		{Path: "SerialNumber", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=/:,.@-]*$`)},
		{Path: "SourceIdentity", Kind: modelcheck.KindLength, Min: 2, Max: 64},
		{Path: "SourceIdentity", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=,.@-]*$`)},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]+$`)},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]*$`)},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "TokenCode", Kind: modelcheck.KindLength, Min: 6, Max: 6},
		{Path: "TokenCode", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\d]*$`)},
		{Path: "TransitiveTagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "TransitiveTagKeys[]", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]+$`)},
	},
	"AssumeRoleWithSAML": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 900, Max: 43200},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "PolicyArns[].arn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "PrincipalArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "PrincipalArn", Kind: modelcheck.KindRequired},
		{Path: "RoleArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "RoleArn", Kind: modelcheck.KindRequired},
		{Path: "SAMLAssertion", Kind: modelcheck.KindLength, Min: 4, Max: 100000},
		{Path: "SAMLAssertion", Kind: modelcheck.KindRequired},
	},
	"AssumeRoleWithWebIdentity": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 900, Max: 43200},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "PolicyArns[].arn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "ProviderId", Kind: modelcheck.KindLength, Min: 4, Max: 2048},
		{Path: "RoleArn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "RoleArn", Kind: modelcheck.KindRequired},
		{Path: "RoleSessionName", Kind: modelcheck.KindLength, Min: 2, Max: 64},
		{Path: "RoleSessionName", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=,.@-]*$`)},
		{Path: "RoleSessionName", Kind: modelcheck.KindRequired},
		{Path: "WebIdentityToken", Kind: modelcheck.KindLength, Min: 4, Max: 20000},
		{Path: "WebIdentityToken", Kind: modelcheck.KindRequired},
	},
	"AssumeRoot": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 0, Max: 900},
		{Path: "TargetPrincipal", Kind: modelcheck.KindLength, Min: 12, Max: 2048},
		{Path: "TargetPrincipal", Kind: modelcheck.KindRequired},
		{Path: "TaskPolicyArn", Kind: modelcheck.KindRequired},
		{Path: "TaskPolicyArn.arn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
	},
	"DecodeAuthorizationMessage": {
		{Path: "EncodedMessage", Kind: modelcheck.KindLength, Min: 1, Max: 10240},
		{Path: "EncodedMessage", Kind: modelcheck.KindRequired},
	},
	"GetAccessKeyInfo": {
		{Path: "AccessKeyId", Kind: modelcheck.KindLength, Min: 16, Max: 128},
		{Path: "AccessKeyId", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w]*$`)},
		{Path: "AccessKeyId", Kind: modelcheck.KindRequired},
	},
	"GetFederationToken": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 900, Max: 129600},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 2, Max: 32},
		{Path: "Name", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=,.@-]*$`)},
		{Path: "Name", Kind: modelcheck.KindRequired},
		{Path: "Policy", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "PolicyArns[].arn", Kind: modelcheck.KindLength, Min: 20, Max: 2048},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]+$`)},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\p{L}\p{Z}\p{N}_.:/=+\-@]*$`)},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
	},
	"GetSessionToken": {
		{Path: "DurationSeconds", Kind: modelcheck.KindRange, Min: 900, Max: 129600},
		{Path: "SerialNumber", Kind: modelcheck.KindLength, Min: 9, Max: 256},
		{Path: "SerialNumber", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\w+=/:,.@-]*$`)},
		{Path: "TokenCode", Kind: modelcheck.KindLength, Min: 6, Max: 6},
		{Path: "TokenCode", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^[\d]*$`)},
	},
}
