package ssm

// SSM Parameter Store's model-derived input validation: the constraint tables, walked by
// internal/modelcheck.
//
// Generated with `dzaudit cases ssm` rather than transcribed, and replayed
// case by case in ssm/rejection_parity_test.go.

import (
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"AddTagsToResource": {
		{Path: "ResourceId", Kind: modelcheck.KindRequired},
		{Path: "ResourceType", Kind: modelcheck.KindEnum, Enum: []string{"Automation", "Association", "CloudConnector", "Document", "ManagedInstance", "MaintenanceWindow", "Parameter", "PatchBaseline", "OpsMetadata", "OpsItem"}},
		{Path: "ResourceType", Kind: modelcheck.KindRequired},
		{Path: "Tags", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
	},
	"DeleteParameter": {
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
	},
	"DeleteParameters": {
		{Path: "Names", Kind: modelcheck.KindRequired},
		{Path: "Names[]", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"DescribeParameters": {
		{Path: "Filters[].Key", Kind: modelcheck.KindEnum, Enum: []string{"Name", "Type", "KeyId"}},
		{Path: "Filters[].Key", Kind: modelcheck.KindRequired},
		{Path: "Filters[].Values", Kind: modelcheck.KindRequired},
		{Path: "Filters[].Values[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 132},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^tag:.+|Name|Type|KeyId|Path|Label|Tier|DataType$`)},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindRequired},
		{Path: "ParameterFilters[].Option", Kind: modelcheck.KindLength, Min: 1, Max: 10},
		{Path: "ParameterFilters[].Values[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"GetParameter": {
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
	},
	"GetParameterHistory": {
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 50},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
	},
	"GetParameters": {
		{Path: "Names", Kind: modelcheck.KindRequired},
		{Path: "Names[]", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"GetParametersByPath": {
		{Path: "MaxResults", Kind: modelcheck.KindRange, Min: 1, Max: 10},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 132},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^tag:.+|Name|Type|KeyId|Path|Label|Tier|DataType$`)},
		{Path: "ParameterFilters[].Key", Kind: modelcheck.KindRequired},
		{Path: "ParameterFilters[].Option", Kind: modelcheck.KindLength, Min: 1, Max: 10},
		{Path: "ParameterFilters[].Values[]", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "Path", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Path", Kind: modelcheck.KindRequired},
	},
	"LabelParameterVersion": {
		{Path: "Labels", Kind: modelcheck.KindRequired},
		{Path: "Labels[]", Kind: modelcheck.KindLength, Min: 1, Max: 100},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
	},
	"ListTagsForResource": {
		{Path: "ResourceId", Kind: modelcheck.KindRequired},
		{Path: "ResourceType", Kind: modelcheck.KindEnum, Enum: []string{"Parameter", "PatchBaseline", "OpsMetadata", "OpsItem", "Automation", "Association", "CloudConnector", "Document", "ManagedInstance", "MaintenanceWindow"}},
		{Path: "ResourceType", Kind: modelcheck.KindRequired},
	},
	"PutParameter": {
		{Path: "AllowedPattern", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "DataType", Kind: modelcheck.KindLength, Min: 0, Max: 128},
		{Path: "Description", Kind: modelcheck.KindLength, Min: 0, Max: 1024},
		{Path: "KeyId", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "KeyId", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([a-zA-Z0-9:/_-]+)$`)},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
		{Path: "Policies", Kind: modelcheck.KindLength, Min: 1, Max: 4096},
		{Path: "Tags[].Key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "Tags[].Key", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)},
		{Path: "Tags[].Key", Kind: modelcheck.KindRequired},
		{Path: "Tags[].Value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "Tags[].Value", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)},
		{Path: "Tags[].Value", Kind: modelcheck.KindRequired},
		{Path: "Tier", Kind: modelcheck.KindEnum, Enum: []string{"Advanced", "Intelligent-Tiering", "Standard"}},
		{Path: "Type", Kind: modelcheck.KindEnum, Enum: []string{"String", "StringList", "SecureString"}},
		{Path: "Value", Kind: modelcheck.KindRequired},
	},
	"RemoveTagsFromResource": {
		{Path: "ResourceId", Kind: modelcheck.KindRequired},
		{Path: "ResourceType", Kind: modelcheck.KindEnum, Enum: []string{"Document", "ManagedInstance", "MaintenanceWindow", "Parameter", "PatchBaseline", "OpsMetadata", "OpsItem", "Automation", "Association", "CloudConnector"}},
		{Path: "ResourceType", Kind: modelcheck.KindRequired},
		{Path: "TagKeys", Kind: modelcheck.KindRequired},
		{Path: "TagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "TagKeys[]", Kind: modelcheck.KindPattern, Pat: modelcheck.Pattern(`^([\p{L}\p{Z}\p{N}_.:/=+\-@]*)$`)},
	},
	"UnlabelParameterVersion": {
		{Path: "Labels", Kind: modelcheck.KindRequired},
		{Path: "Labels[]", Kind: modelcheck.KindLength, Min: 1, Max: 100},
		{Path: "Name", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "Name", Kind: modelcheck.KindRequired},
		{Path: "ParameterVersion", Kind: modelcheck.KindRequired},
	},
}
