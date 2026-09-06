package stepfunctions

// Step Functions' model-derived input validation: the constraint tables,
// walked by internal/modelcheck before every handler.
//
// Generated from AWS's own service model (`dzaudit list sfn`, the sfn.json
// Smithy model) and replayed case by case in rejection_parity_test.go. An
// operation missing from this map either states no constraints on its inputs
// or is one doze-aws does not dispatch — the latter refuses every request
// including a valid one, so replaying a mutation against it proves nothing.
//
// Note the lowercase-initial paths: Step Functions' members are spelled
// stateMachineArn, not StateMachineArn, and the walker matches keys exactly.

import (
	"regexp"

	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

var constraintTables = map[string][]modelcheck.Constraint{
	"CreateActivity": {
		{Path: "encryptionConfiguration.kmsDataKeyReusePeriodSeconds", Kind: modelcheck.KindRange, Min: 60, Max: 900},
		{Path: "encryptionConfiguration.kmsKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindRequired},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindEnum, Enum: []string{"AWS_OWNED_KEY", "CUSTOMER_MANAGED_KMS_KEY"}},
		{Path: "name", Kind: modelcheck.KindRequired},
		{Path: "name", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "tags[].key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "tags[].value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"CreateStateMachine": {
		{Path: "definition", Kind: modelcheck.KindRequired},
		{Path: "definition", Kind: modelcheck.KindLength, Min: 1, Max: 1048576},
		{Path: "encryptionConfiguration.kmsDataKeyReusePeriodSeconds", Kind: modelcheck.KindRange, Min: 60, Max: 900},
		{Path: "encryptionConfiguration.kmsKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindRequired},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindEnum, Enum: []string{"AWS_OWNED_KEY", "CUSTOMER_MANAGED_KMS_KEY"}},
		{Path: "loggingConfiguration.level", Kind: modelcheck.KindEnum, Enum: []string{"ALL", "ERROR", "FATAL", "OFF"}},
		{Path: "name", Kind: modelcheck.KindRequired},
		{Path: "name", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "roleArn", Kind: modelcheck.KindRequired},
		{Path: "roleArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "tags[].key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "tags[].value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "type", Kind: modelcheck.KindEnum, Enum: []string{"STANDARD", "EXPRESS"}},
		{Path: "versionDescription", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"DeleteActivity": {
		{Path: "activityArn", Kind: modelcheck.KindRequired},
		{Path: "activityArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DeleteStateMachine": {
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DescribeActivity": {
		{Path: "activityArn", Kind: modelcheck.KindRequired},
		{Path: "activityArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DescribeExecution": {
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "includedData", Kind: modelcheck.KindEnum, Enum: []string{"ALL_DATA", "METADATA_ONLY"}},
	},
	"DescribeStateMachine": {
		{Path: "includedData", Kind: modelcheck.KindEnum, Enum: []string{"ALL_DATA", "METADATA_ONLY"}},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DescribeStateMachineForExecution": {
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "includedData", Kind: modelcheck.KindEnum, Enum: []string{"ALL_DATA", "METADATA_ONLY"}},
	},
	"GetExecutionHistory": {
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListActivities": {
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListExecutions": {
		{Path: "mapRunArn", Kind: modelcheck.KindLength, Min: 1, Max: 2000},
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 3096},
		{Path: "redriveFilter", Kind: modelcheck.KindEnum, Enum: []string{"REDRIVEN", "NOT_REDRIVEN"}},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "statusFilter", Kind: modelcheck.KindEnum, Enum: []string{"TIMED_OUT", "ABORTED", "PENDING_REDRIVE", "RUNNING", "SUCCEEDED", "FAILED"}},
	},
	"ListStateMachines": {
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"ListTagsForResource": {
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
		{Path: "resourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"SendTaskFailure": {
		{Path: "cause", Kind: modelcheck.KindLength, Min: 0, Max: 32768},
		{Path: "error", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "taskToken", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "taskToken", Kind: modelcheck.KindRequired},
	},
	"SendTaskHeartbeat": {
		{Path: "taskToken", Kind: modelcheck.KindRequired},
		{Path: "taskToken", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"SendTaskSuccess": {
		{Path: "output", Kind: modelcheck.KindRequired},
		{Path: "output", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "taskToken", Kind: modelcheck.KindRequired},
		{Path: "taskToken", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
	},
	"StartExecution": {
		{Path: "input", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "name", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "traceHeader", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "traceHeader", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[[:ascii:]]*$`)},
	},
	"StopExecution": {
		{Path: "cause", Kind: modelcheck.KindLength, Min: 0, Max: 32768},
		{Path: "error", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"TagResource": {
		{Path: "resourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
		{Path: "tags", Kind: modelcheck.KindRequired},
		{Path: "tags[].key", Kind: modelcheck.KindLength, Min: 1, Max: 128},
		{Path: "tags[].value", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"UntagResource": {
		{Path: "resourceArn", Kind: modelcheck.KindRequired},
		{Path: "resourceArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "tagKeys", Kind: modelcheck.KindRequired},
		{Path: "tagKeys[]", Kind: modelcheck.KindLength, Min: 1, Max: 128},
	},
	"UpdateStateMachine": {
		{Path: "definition", Kind: modelcheck.KindLength, Min: 1, Max: 1048576},
		{Path: "encryptionConfiguration.kmsDataKeyReusePeriodSeconds", Kind: modelcheck.KindRange, Min: 60, Max: 900},
		{Path: "encryptionConfiguration.kmsKeyId", Kind: modelcheck.KindLength, Min: 1, Max: 2048},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindRequired},
		{Path: "encryptionConfiguration.type", Kind: modelcheck.KindEnum, Enum: []string{"AWS_OWNED_KEY", "CUSTOMER_MANAGED_KMS_KEY"}},
		{Path: "loggingConfiguration.level", Kind: modelcheck.KindEnum, Enum: []string{"ALL", "ERROR", "FATAL", "OFF"}},
		{Path: "roleArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "versionDescription", Kind: modelcheck.KindLength, Min: 0, Max: 256},
	},
	"ValidateStateMachineDefinition": {
		{Path: "definition", Kind: modelcheck.KindRequired},
		{Path: "definition", Kind: modelcheck.KindLength, Min: 1, Max: 1048576},
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 100},
		{Path: "severity", Kind: modelcheck.KindEnum, Enum: []string{"ERROR", "WARNING"}},
		{Path: "type", Kind: modelcheck.KindEnum, Enum: []string{"STANDARD", "EXPRESS"}},
	},
}
