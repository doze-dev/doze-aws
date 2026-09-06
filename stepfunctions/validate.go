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

	// The fifteen operations that arrived with the completion pass.
	"StartSyncExecution": {
		{Path: "includedData", Kind: modelcheck.KindEnum, Enum: []string{"METADATA_ONLY", "ALL_DATA"}},
		{Path: "input", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "name", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "traceHeader", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "traceHeader", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[[:ascii:]]*$`)},
	},
	"TestState": {
		{Path: "context", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "definition", Kind: modelcheck.KindRequired},
		{Path: "definition", Kind: modelcheck.KindLength, Min: 1, Max: 1048576},
		{Path: "input", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "inspectionLevel", Kind: modelcheck.KindEnum, Enum: []string{"INFO", "DEBUG", "TRACE"}},
		{Path: "mock.errorOutput.cause", Kind: modelcheck.KindLength, Min: 0, Max: 32768},
		{Path: "mock.errorOutput.error", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "mock.fieldValidationMode", Kind: modelcheck.KindEnum, Enum: []string{"STRICT", "PRESENT", "NONE"}},
		{Path: "mock.result", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "roleArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "stateConfiguration.errorCausedByState", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "stateConfiguration.mapItemReaderData", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
		{Path: "stateConfiguration.mapIterationFailureCount", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "stateConfiguration.retrierRetryCount", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "stateName", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "variables", Kind: modelcheck.KindLength, Min: 0, Max: 262144},
	},
	"GetActivityTask": {
		{Path: "activityArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "activityArn", Kind: modelcheck.KindRequired},
		{Path: "workerName", Kind: modelcheck.KindLength, Min: 1, Max: 80},
	},
	"RedriveExecution": {
		{Path: "clientToken", Kind: modelcheck.KindLength, Min: 1, Max: 64},
		{Path: "clientToken", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[!-~]+$`)},
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"PublishStateMachineVersion": {
		{Path: "description", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DeleteStateMachineVersion": {
		{Path: "stateMachineVersionArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineVersionArn", Kind: modelcheck.KindLength, Min: 1, Max: 2000},
	},
	"ListStateMachineVersions": {
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"CreateStateMachineAlias": {
		{Path: "description", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "name", Kind: modelcheck.KindRequired},
		{Path: "name", Kind: modelcheck.KindLength, Min: 1, Max: 80},
		{Path: "name", Kind: modelcheck.KindPattern, Pat: regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)},
		{Path: "routingConfiguration", Kind: modelcheck.KindRequired},
		{Path: "routingConfiguration[].stateMachineVersionArn", Kind: modelcheck.KindRequired},
		{Path: "routingConfiguration[].stateMachineVersionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "routingConfiguration[].weight", Kind: modelcheck.KindRequired},
		{Path: "routingConfiguration[].weight", Kind: modelcheck.KindRange, Min: 0, Max: 100},
	},
	"DescribeStateMachineAlias": {
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"UpdateStateMachineAlias": {
		{Path: "description", Kind: modelcheck.KindLength, Min: 0, Max: 256},
		{Path: "routingConfiguration[].stateMachineVersionArn", Kind: modelcheck.KindRequired},
		{Path: "routingConfiguration[].stateMachineVersionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "routingConfiguration[].weight", Kind: modelcheck.KindRange, Min: 0, Max: 100},
		{Path: "routingConfiguration[].weight", Kind: modelcheck.KindRequired},
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DeleteStateMachineAlias": {
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineAliasArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"ListStateMachineAliases": {
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
		{Path: "stateMachineArn", Kind: modelcheck.KindRequired},
		{Path: "stateMachineArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
	},
	"DescribeMapRun": {
		{Path: "mapRunArn", Kind: modelcheck.KindRequired},
		{Path: "mapRunArn", Kind: modelcheck.KindLength, Min: 1, Max: 2000},
	},
	"ListMapRuns": {
		{Path: "executionArn", Kind: modelcheck.KindLength, Min: 1, Max: 256},
		{Path: "executionArn", Kind: modelcheck.KindRequired},
		{Path: "maxResults", Kind: modelcheck.KindRange, Min: 0, Max: 1000},
		{Path: "nextToken", Kind: modelcheck.KindLength, Min: 1, Max: 1024},
	},
	"UpdateMapRun": {
		{Path: "mapRunArn", Kind: modelcheck.KindRequired},
		{Path: "mapRunArn", Kind: modelcheck.KindLength, Min: 1, Max: 2000},
		{Path: "maxConcurrency", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "toleratedFailureCount", Kind: modelcheck.KindRange, Min: 0, Max: modelcheck.NoMax},
		{Path: "toleratedFailurePercentage", Kind: modelcheck.KindRange, Min: 0, Max: 100},
	},
}
