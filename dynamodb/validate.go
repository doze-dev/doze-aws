package dynamodb

// Model-derived input validation.
//
// Every constraint here is stated by AWS's own service model — `dzaudit list
// --op <op> dynamodb` prints the same set — and `dynamodb/rejection_parity_test.go`
// replays a violating value for each one. The tables and the test are two
// halves of the same fact: the table refuses, the test proves the refusal
// happens for the right reason and not by accident.
//
// # Why the raw body
//
// The checks run against the body decoded to `any` rather than the typed
// request, because most of these members have no local effect and so were
// never decoded — which is precisely why they were accepted. A member
// doze-aws ignores still has to be refused when it is invalid, or code that
// would fail on deploy passes here, which is the whole failure this audit
// exists to catch.
//
// # Why paths
//
// Two thirds of the model's constraints live inside structures rather than on
// top-level members. A flat member->rule map cannot express
// GlobalSecondaryIndexes[].Projection.ProjectionType, so a table is keyed by
// path and a walker resolves it. A segment is a member name followed by
// container markers, applied left to right:
//
//	Tags[]                    every element of a list
//	RequestItems{}            every value of a map
//	RequestItems{}[].PutRequest   a map of lists, which is BatchWriteItem's shape
//
// A path whose enclosing structure was not sent yields nothing to check, which
// is what makes a constraint on an optional structure's member apply only when
// that structure is actually present.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// noMax is an unbounded upper bound — the model's "range 1..-".
var noMax = math.Inf(1)

type ckind int

const (
	ckRequired ckind = iota
	ckEnum
	ckLength
	ckRange
	ckPattern
)

// constraint is one rule the model states about one input path.
type constraint struct {
	path string
	kind ckind
	enum []string
	min  float64
	max  float64
	pat  *regexp.Regexp
}

// site is one concrete location a path resolved to: the value found there,
// whether it was present at all, and how AWS would spell that location.
type site struct {
	val     any
	present bool
	disp    string
}

var markerRE = regexp.MustCompile(`^([A-Za-z0-9]+)((?:\[\]|\{\})*)$`)

// splitSegment separates a segment into its member name and its markers.
func splitSegment(seg string) (name string, markers []string) {
	m := markerRE.FindStringSubmatch(seg)
	if m == nil {
		return seg, nil
	}
	for i := 0; i+1 < len(m[2]); i += 2 {
		markers = append(markers, m[2][i:i+2])
	}
	return m[1], markers
}

// element is one value reached while expanding a segment's markers, with the
// display path naming its position.
type element struct {
	v    any
	disp []string
}

// expand applies a segment's markers in order, fanning one value out to every
// element (or map value) it contains.
func expand(start element, markers []string) []element {
	level := []element{start}
	for _, mk := range markers {
		var next []element
		for _, el := range level {
			switch mk {
			case "[]":
				lst, ok := el.v.([]any)
				if !ok {
					continue
				}
				for i, item := range lst {
					// AWS indexes list positions from 1 in validation messages.
					next = append(next, element{v: item,
						disp: append(append([]string{}, el.disp...), strconv.Itoa(i+1))})
				}
			case "{}":
				m, ok := el.v.(map[string]any)
				if !ok {
					continue
				}
				// Sorted, so a body with several keys produces the same message
				// every run rather than map-iteration roulette.
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, k := range keys {
					next = append(next, element{v: m[k],
						disp: append(append([]string{}, el.disp...), k)})
				}
			}
		}
		level = next
	}
	return level
}

// sites resolves a path over the body.
func sites(root map[string]any, path string) []site {
	return descend(root, strings.Split(path, "."), nil)
}

func descend(cur map[string]any, segs []string, prefix []string) []site {
	name, markers := splitSegment(segs[0])
	here := append(append([]string{}, prefix...), lowerFirst(name))
	v, present := cur[name]

	if len(segs) == 1 {
		// The leaf. Without markers it is the member itself; with them, the
		// constraint is on each element or map value — AttributesToGet[] bounds
		// the strings in the list, not the list.
		if len(markers) == 0 || !present {
			return []site{{val: v, present: present, disp: strings.Join(here, ".")}}
		}
		var out []site
		for _, el := range expand(element{v: v, disp: here}, markers) {
			out = append(out, site{val: el.v, present: true, disp: strings.Join(el.disp, ".")})
		}
		return out
	}

	if !present {
		return nil // the structure was not sent: nothing inside it to check
	}
	var out []site
	for _, el := range expand(element{v: v, disp: here}, markers) {
		m, ok := el.v.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, descend(m, segs[1:], el.disp)...)
	}
	return out
}

// check applies one constraint at one site, returning the error AWS would.
func (c constraint) check(s site) *awshttp.APIError {
	if c.kind == ckRequired {
		if !s.present || s.val == nil {
			return validationErr("Value null at '%s' failed to satisfy constraint: "+
				"Member must not be null", s.disp)
		}
		return nil
	}
	if !s.present || s.val == nil {
		return nil // absent is the @required check's business, not this one's
	}

	switch c.kind {
	case ckEnum:
		str, ok := s.val.(string)
		if !ok {
			return nil
		}
		if !slices.Contains(c.enum, str) {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [%s]",
				str, s.disp, strings.Join(c.enum, ", "))
		}
	case ckLength:
		n, ok := lengthOf(s.val)
		if !ok {
			return nil
		}
		if float64(n) < c.min {
			return validationErr("Value at '%s' failed to satisfy constraint: "+
				"Member must have length greater than or equal to %d", s.disp, int(c.min))
		}
		if float64(n) > c.max {
			return validationErr("Value at '%s' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", s.disp, int(c.max))
		}
	case ckRange:
		f, ok := toNumber(s.val)
		if !ok {
			return nil
		}
		if f < c.min {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value greater than or equal to %d",
				trimNum(f), s.disp, int(c.min))
		}
		if f > c.max {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must have value less than or equal to %d",
				trimNum(f), s.disp, int(c.max))
		}
	case ckPattern:
		str, ok := s.val.(string)
		if !ok {
			return nil
		}
		if !c.pat.MatchString(str) {
			return validationErr("Value '%s' at '%s' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: %s", str, s.disp, c.pat.String())
		}
	}
	return nil
}

// lengthOf measures whatever the model bounds the length of: a string, a list
// or a map. @length on a map member bounds the collection, not its values.
func lengthOf(v any) (int, bool) {
	switch t := v.(type) {
	case string:
		return len(t), true
	case []any:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

// validate runs a constraint table over a raw request body.
//
// Order is the table's order, so a request breaking several rules reports the
// same one every run rather than whichever the map iterated to first.
func validate(body []byte, table []constraint) *awshttp.APIError {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil // the caller's own decode reports this
	}
	for _, c := range table {
		for _, s := range sites(raw, c.path) {
			if err := c.check(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func validationErr(format string, args ...any) *awshttp.APIError {
	return awshttp.Errf(400, "ValidationException", "%s",
		"1 validation error detected: "+fmt.Sprintf(format, args...))
}

// toNumber accepts the shapes a JSON number arrives in.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// trimNum renders a number the way it was written, so an integer bound does
// not come back as "0.000000" in the message.
func trimNum(f float64) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// lowerFirst renders a member the way AWS names it in a validation message.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// Constraint tables, one per operation, generated from AWS's own service
// model with `dzaudit cases dynamodb` rather than transcribed by hand.
//
// An operation missing from this map either states no constraints on its
// inputs or is one doze-aws does not dispatch — the latter refuses every
// request including a valid one, so it cannot be audited by replaying
// cases at all, and docs/api-support/dynamodb.md records which.
var constraintTables = map[string][]constraint{
	"BatchExecuteStatement": {
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "Statements", kind: ckRequired},
		{path: "Statements[].ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "Statements[].Statement", kind: ckLength, min: 1, max: 8192},
		{path: "Statements[].Statement", kind: ckRequired},
	},
	"BatchGetItem": {
		{path: "RequestItems", kind: ckRequired},
		{path: "RequestItems{}.AttributesToGet[]", kind: ckLength, min: 0, max: 65535},
		{path: "RequestItems{}.ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "RequestItems{}.Keys", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"TOTAL", "NONE", "INDEXES"}},
	},
	"BatchWriteItem": {
		{path: "RequestItems", kind: ckRequired},
		{path: "RequestItems{}[].DeleteRequest.Key", kind: ckRequired},
		{path: "RequestItems{}[].PutRequest.Item", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"NONE", "INDEXES", "TOTAL"}},
		{path: "ReturnItemCollectionMetrics", kind: ckEnum, enum: []string{"SIZE", "NONE"}},
	},
	"CreateTable": {
		{path: "AttributeDefinitions[].AttributeName", kind: ckLength, min: 1, max: 255},
		{path: "AttributeDefinitions[].AttributeName", kind: ckRequired},
		{path: "AttributeDefinitions[].AttributeType", kind: ckEnum, enum: []string{"B", "S", "N"}},
		{path: "AttributeDefinitions[].AttributeType", kind: ckRequired},
		{path: "BillingMode", kind: ckEnum, enum: []string{"PROVISIONED", "PAY_PER_REQUEST"}},
		{path: "GlobalSecondaryIndexes[].IndexName", kind: ckLength, min: 3, max: 255},
		{path: "GlobalSecondaryIndexes[].IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "GlobalSecondaryIndexes[].IndexName", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].KeySchema", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].KeySchema[].AttributeName", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].KeySchema[].KeyType", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].Projection", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].Projection.ProjectionType", kind: ckEnum, enum: []string{"ALL", "KEYS_ONLY", "INCLUDE"}},
		{path: "GlobalSecondaryIndexes[].ProvisionedThroughput.ReadCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "GlobalSecondaryIndexes[].ProvisionedThroughput.ReadCapacityUnits", kind: ckRequired},
		{path: "GlobalSecondaryIndexes[].ProvisionedThroughput.WriteCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "GlobalSecondaryIndexes[].ProvisionedThroughput.WriteCapacityUnits", kind: ckRequired},
		{path: "GlobalTableSettingsReplicationMode", kind: ckEnum, enum: []string{"ENABLED", "DISABLED", "ENABLED_WITH_OVERRIDES"}},
		{path: "GlobalTableSourceArn", kind: ckLength, min: 1, max: 1024},
		{path: "KeySchema[].AttributeName", kind: ckLength, min: 1, max: 255},
		{path: "KeySchema[].AttributeName", kind: ckRequired},
		{path: "KeySchema[].KeyType", kind: ckEnum, enum: []string{"RANGE", "HASH"}},
		{path: "KeySchema[].KeyType", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].IndexName", kind: ckLength, min: 3, max: 255},
		{path: "LocalSecondaryIndexes[].IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "LocalSecondaryIndexes[].IndexName", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].KeySchema", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].KeySchema[].AttributeName", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].KeySchema[].KeyType", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].Projection", kind: ckRequired},
		{path: "LocalSecondaryIndexes[].Projection.ProjectionType", kind: ckEnum, enum: []string{"KEYS_ONLY", "INCLUDE", "ALL"}},
		{path: "ProvisionedThroughput.ReadCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "ProvisionedThroughput.ReadCapacityUnits", kind: ckRequired},
		{path: "ProvisionedThroughput.WriteCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "ProvisionedThroughput.WriteCapacityUnits", kind: ckRequired},
		{path: "SSESpecification.SSEType", kind: ckEnum, enum: []string{"AES256", "KMS"}},
		{path: "StreamSpecification.StreamEnabled", kind: ckRequired},
		{path: "StreamSpecification.StreamViewType", kind: ckEnum, enum: []string{"NEW_IMAGE", "OLD_IMAGE", "NEW_AND_OLD_IMAGES", "KEYS_ONLY"}},
		{path: "TableClass", kind: ckEnum, enum: []string{"STANDARD_INFREQUENT_ACCESS", "STANDARD"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
		{path: "Tags[].Key", kind: ckLength, min: 1, max: 128},
		{path: "Tags[].Key", kind: ckRequired},
		{path: "Tags[].Value", kind: ckLength, min: 0, max: 256},
		{path: "Tags[].Value", kind: ckRequired},
		{path: "VectorIndexes[].Dimensions", kind: ckRange, min: 1, max: noMax},
		{path: "VectorIndexes[].Dimensions", kind: ckRequired},
		{path: "VectorIndexes[].DistanceFunction", kind: ckEnum, enum: []string{"EUCLIDEAN", "COSINE", "DOT_PRODUCT"}},
		{path: "VectorIndexes[].DistanceFunction", kind: ckRequired},
		{path: "VectorIndexes[].IndexName", kind: ckLength, min: 3, max: 255},
		{path: "VectorIndexes[].IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "VectorIndexes[].IndexName", kind: ckRequired},
		{path: "VectorIndexes[].Projection", kind: ckRequired},
		{path: "VectorIndexes[].Projection.ProjectionType", kind: ckEnum, enum: []string{"KEYS_ONLY", "INCLUDE", "ALL"}},
		{path: "VectorIndexes[].SearchSchema[].AttributeName", kind: ckRequired},
		{path: "VectorIndexes[].SearchSchema[].SearchSchemaElementType", kind: ckRequired},
		{path: "VectorIndexes[].VectorAttribute", kind: ckRequired},
		{path: "VectorIndexes[].VectorAttribute.AttributeName", kind: ckLength, min: 1, max: 255},
		{path: "VectorIndexes[].VectorAttribute.AttributeName", kind: ckRequired},
	},
	"DeleteItem": {
		{path: "ConditionalOperator", kind: ckEnum, enum: []string{"AND", "OR"}},
		{path: "Expected{}.ComparisonOperator", kind: ckEnum, enum: []string{"NOT_CONTAINS", "BEGINS_WITH", "NE", "IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL", "EQ", "BETWEEN", "CONTAINS"}},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "Key", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "ReturnItemCollectionMetrics", kind: ckEnum, enum: []string{"SIZE", "NONE"}},
		{path: "ReturnValues", kind: ckEnum, enum: []string{"UPDATED_NEW", "NONE", "ALL_OLD", "UPDATED_OLD", "ALL_NEW"}},
		{path: "ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"DeleteTable": {
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"DescribeContinuousBackups": {
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"DescribeContributorInsights": {
		{path: "IndexName", kind: ckLength, min: 3, max: 255},
		{path: "IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"DescribeTable": {
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"DescribeTimeToLive": {
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"ExecuteStatement": {
		{path: "Limit", kind: ckRange, min: 1, max: noMax},
		{path: "NextToken", kind: ckLength, min: 1, max: 32768},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "Statement", kind: ckLength, min: 1, max: 8192},
		{path: "Statement", kind: ckRequired},
	},
	"ExecuteTransaction": {
		{path: "ClientRequestToken", kind: ckLength, min: 1, max: 36},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "TransactStatements", kind: ckRequired},
		{path: "TransactStatements[].ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TransactStatements[].Statement", kind: ckLength, min: 1, max: 8192},
		{path: "TransactStatements[].Statement", kind: ckRequired},
	},
	"GetItem": {
		{path: "AttributesToGet[]", kind: ckLength, min: 0, max: 65535},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "Key", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"ListTables": {
		{path: "ExclusiveStartTableName", kind: ckLength, min: 3, max: 255},
		{path: "ExclusiveStartTableName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "Limit", kind: ckRange, min: 1, max: 100},
	},
	"ListTagsOfResource": {
		{path: "ResourceArn", kind: ckLength, min: 1, max: 1283},
		{path: "ResourceArn", kind: ckRequired},
	},
	"PutItem": {
		{path: "ConditionalOperator", kind: ckEnum, enum: []string{"AND", "OR"}},
		{path: "Expected{}.ComparisonOperator", kind: ckEnum, enum: []string{"EQ", "BETWEEN", "CONTAINS", "NOT_CONTAINS", "BEGINS_WITH", "NE", "IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL"}},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "Item", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "ReturnItemCollectionMetrics", kind: ckEnum, enum: []string{"SIZE", "NONE"}},
		{path: "ReturnValues", kind: ckEnum, enum: []string{"ALL_NEW", "UPDATED_NEW", "NONE", "ALL_OLD", "UPDATED_OLD"}},
		{path: "ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"Query": {
		{path: "AttributesToGet[]", kind: ckLength, min: 0, max: 65535},
		{path: "ConditionalOperator", kind: ckEnum, enum: []string{"AND", "OR"}},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "IndexName", kind: ckLength, min: 3, max: 255},
		{path: "IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "KeyConditions{}.ComparisonOperator", kind: ckEnum, enum: []string{"NE", "IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL", "EQ", "BETWEEN", "CONTAINS", "NOT_CONTAINS", "BEGINS_WITH"}},
		{path: "KeyConditions{}.ComparisonOperator", kind: ckRequired},
		{path: "Limit", kind: ckRange, min: 1, max: noMax},
		{path: "QueryFilter{}.ComparisonOperator", kind: ckEnum, enum: []string{"NE", "IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL", "EQ", "BETWEEN", "CONTAINS", "NOT_CONTAINS", "BEGINS_WITH"}},
		{path: "QueryFilter{}.ComparisonOperator", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "Select", kind: ckEnum, enum: []string{"ALL_ATTRIBUTES", "ALL_PROJECTED_ATTRIBUTES", "SPECIFIC_ATTRIBUTES", "COUNT"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"Scan": {
		{path: "AttributesToGet[]", kind: ckLength, min: 0, max: 65535},
		{path: "ConditionalOperator", kind: ckEnum, enum: []string{"OR", "AND"}},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "IndexName", kind: ckLength, min: 3, max: 255},
		{path: "IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "Limit", kind: ckRange, min: 1, max: noMax},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "ScanFilter{}.ComparisonOperator", kind: ckEnum, enum: []string{"CONTAINS", "NOT_CONTAINS", "BEGINS_WITH", "NE", "IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL", "EQ", "BETWEEN"}},
		{path: "ScanFilter{}.ComparisonOperator", kind: ckRequired},
		{path: "Segment", kind: ckRange, min: 0, max: 999999},
		{path: "Select", kind: ckEnum, enum: []string{"ALL_ATTRIBUTES", "ALL_PROJECTED_ATTRIBUTES", "SPECIFIC_ATTRIBUTES", "COUNT"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
		{path: "TotalSegments", kind: ckRange, min: 1, max: 1000000},
	},
	"TagResource": {
		{path: "ResourceArn", kind: ckLength, min: 1, max: 1283},
		{path: "ResourceArn", kind: ckRequired},
		{path: "Tags", kind: ckRequired},
		{path: "Tags[].Key", kind: ckLength, min: 1, max: 128},
		{path: "Tags[].Key", kind: ckRequired},
		{path: "Tags[].Value", kind: ckLength, min: 0, max: 256},
		{path: "Tags[].Value", kind: ckRequired},
	},
	"TransactGetItems": {
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "TransactItems", kind: ckRequired},
		{path: "TransactItems[].Get", kind: ckRequired},
		{path: "TransactItems[].Get.Key", kind: ckRequired},
		{path: "TransactItems[].Get.TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TransactItems[].Get.TableName", kind: ckRequired},
	},
	"TransactWriteItems": {
		{path: "ClientRequestToken", kind: ckLength, min: 1, max: 36},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"TOTAL", "NONE", "INDEXES"}},
		{path: "ReturnItemCollectionMetrics", kind: ckEnum, enum: []string{"SIZE", "NONE"}},
		{path: "TransactItems", kind: ckRequired},
		{path: "TransactItems[].ConditionCheck.ConditionExpression", kind: ckRequired},
		{path: "TransactItems[].ConditionCheck.Key", kind: ckRequired},
		{path: "TransactItems[].ConditionCheck.ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TransactItems[].ConditionCheck.TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TransactItems[].ConditionCheck.TableName", kind: ckRequired},
		{path: "TransactItems[].Delete.Key", kind: ckRequired},
		{path: "TransactItems[].Delete.ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TransactItems[].Delete.TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TransactItems[].Delete.TableName", kind: ckRequired},
		{path: "TransactItems[].Put.Item", kind: ckRequired},
		{path: "TransactItems[].Put.ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TransactItems[].Put.TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TransactItems[].Put.TableName", kind: ckRequired},
		{path: "TransactItems[].Update.Key", kind: ckRequired},
		{path: "TransactItems[].Update.ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"ALL_OLD", "NONE"}},
		{path: "TransactItems[].Update.TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TransactItems[].Update.TableName", kind: ckRequired},
		{path: "TransactItems[].Update.UpdateExpression", kind: ckRequired},
	},
	"UntagResource": {
		{path: "ResourceArn", kind: ckLength, min: 1, max: 1283},
		{path: "ResourceArn", kind: ckRequired},
		{path: "TagKeys", kind: ckRequired},
		{path: "TagKeys[]", kind: ckLength, min: 1, max: 128},
	},
	"UpdateContinuousBackups": {
		{path: "PointInTimeRecoverySpecification", kind: ckRequired},
		{path: "PointInTimeRecoverySpecification.PointInTimeRecoveryEnabled", kind: ckRequired},
		{path: "PointInTimeRecoverySpecification.RecoveryPeriodInDays", kind: ckRange, min: 1, max: 35},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"UpdateContributorInsights": {
		{path: "ContributorInsightsAction", kind: ckEnum, enum: []string{"ENABLE", "DISABLE"}},
		{path: "ContributorInsightsAction", kind: ckRequired},
		{path: "ContributorInsightsMode", kind: ckEnum, enum: []string{"ACCESSED_AND_THROTTLED_KEYS", "THROTTLED_KEYS"}},
		{path: "IndexName", kind: ckLength, min: 3, max: 255},
		{path: "IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"UpdateItem": {
		{path: "AttributeUpdates{}.Action", kind: ckEnum, enum: []string{"DELETE", "ADD", "PUT"}},
		{path: "ConditionalOperator", kind: ckEnum, enum: []string{"AND", "OR"}},
		{path: "Expected{}.ComparisonOperator", kind: ckEnum, enum: []string{"IN", "LE", "LT", "GE", "GT", "NOT_NULL", "NULL", "EQ", "BETWEEN", "CONTAINS", "NOT_CONTAINS", "BEGINS_WITH", "NE"}},
		{path: "ExpressionAttributeNames{}", kind: ckLength, min: 0, max: 65535},
		{path: "Key", kind: ckRequired},
		{path: "ReturnConsumedCapacity", kind: ckEnum, enum: []string{"INDEXES", "TOTAL", "NONE"}},
		{path: "ReturnItemCollectionMetrics", kind: ckEnum, enum: []string{"SIZE", "NONE"}},
		{path: "ReturnValues", kind: ckEnum, enum: []string{"UPDATED_NEW", "NONE", "ALL_OLD", "UPDATED_OLD", "ALL_NEW"}},
		{path: "ReturnValuesOnConditionCheckFailure", kind: ckEnum, enum: []string{"NONE", "ALL_OLD"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
	},
	"UpdateTable": {
		{path: "AttributeDefinitions[].AttributeName", kind: ckLength, min: 1, max: 255},
		{path: "AttributeDefinitions[].AttributeName", kind: ckRequired},
		{path: "AttributeDefinitions[].AttributeType", kind: ckEnum, enum: []string{"S", "N", "B"}},
		{path: "AttributeDefinitions[].AttributeType", kind: ckRequired},
		{path: "BillingMode", kind: ckEnum, enum: []string{"PROVISIONED", "PAY_PER_REQUEST"}},
		{path: "GlobalSecondaryIndexUpdates[].Create.IndexName", kind: ckLength, min: 3, max: 255},
		{path: "GlobalSecondaryIndexUpdates[].Create.IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "GlobalSecondaryIndexUpdates[].Create.IndexName", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Create.KeySchema", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Create.Projection", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Create.ProvisionedThroughput.ReadCapacityUnits", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Create.ProvisionedThroughput.WriteCapacityUnits", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Delete.IndexName", kind: ckLength, min: 3, max: 255},
		{path: "GlobalSecondaryIndexUpdates[].Delete.IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "GlobalSecondaryIndexUpdates[].Delete.IndexName", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Update.IndexName", kind: ckLength, min: 3, max: 255},
		{path: "GlobalSecondaryIndexUpdates[].Update.IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "GlobalSecondaryIndexUpdates[].Update.IndexName", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Update.ProvisionedThroughput.ReadCapacityUnits", kind: ckRequired},
		{path: "GlobalSecondaryIndexUpdates[].Update.ProvisionedThroughput.WriteCapacityUnits", kind: ckRequired},
		{path: "GlobalTableSettingsReplicationMode", kind: ckEnum, enum: []string{"ENABLED", "DISABLED", "ENABLED_WITH_OVERRIDES"}},
		{path: "GlobalTableWitnessUpdates[].Create.RegionName", kind: ckRequired},
		{path: "GlobalTableWitnessUpdates[].Delete.RegionName", kind: ckRequired},
		{path: "MultiRegionConsistency", kind: ckEnum, enum: []string{"EVENTUAL", "STRONG"}},
		{path: "ProvisionedThroughput.ReadCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "ProvisionedThroughput.ReadCapacityUnits", kind: ckRequired},
		{path: "ProvisionedThroughput.WriteCapacityUnits", kind: ckRange, min: 1, max: noMax},
		{path: "ProvisionedThroughput.WriteCapacityUnits", kind: ckRequired},
		{path: "ReplicaUpdates[].Create.RegionName", kind: ckRequired},
		{path: "ReplicaUpdates[].Create.TableClassOverride", kind: ckEnum, enum: []string{"STANDARD", "STANDARD_INFREQUENT_ACCESS"}},
		{path: "ReplicaUpdates[].Delete.RegionName", kind: ckRequired},
		{path: "ReplicaUpdates[].Update.RegionName", kind: ckRequired},
		{path: "ReplicaUpdates[].Update.TableClassOverride", kind: ckEnum, enum: []string{"STANDARD_INFREQUENT_ACCESS", "STANDARD"}},
		{path: "SSESpecification.SSEType", kind: ckEnum, enum: []string{"AES256", "KMS"}},
		{path: "StreamSpecification.StreamEnabled", kind: ckRequired},
		{path: "StreamSpecification.StreamViewType", kind: ckEnum, enum: []string{"NEW_AND_OLD_IMAGES", "KEYS_ONLY", "NEW_IMAGE", "OLD_IMAGE"}},
		{path: "TableClass", kind: ckEnum, enum: []string{"STANDARD", "STANDARD_INFREQUENT_ACCESS"}},
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.Dimensions", kind: ckRange, min: 1, max: noMax},
		{path: "VectorIndexUpdates[].Create.Dimensions", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.DistanceFunction", kind: ckEnum, enum: []string{"COSINE", "DOT_PRODUCT", "EUCLIDEAN"}},
		{path: "VectorIndexUpdates[].Create.DistanceFunction", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.IndexName", kind: ckLength, min: 3, max: 255},
		{path: "VectorIndexUpdates[].Create.IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "VectorIndexUpdates[].Create.IndexName", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.Projection", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.VectorAttribute", kind: ckRequired},
		{path: "VectorIndexUpdates[].Create.VectorAttribute.AttributeName", kind: ckRequired},
		{path: "VectorIndexUpdates[].Delete.IndexName", kind: ckLength, min: 3, max: 255},
		{path: "VectorIndexUpdates[].Delete.IndexName", kind: ckPattern, pat: regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)},
		{path: "VectorIndexUpdates[].Delete.IndexName", kind: ckRequired},
	},
	"UpdateTimeToLive": {
		{path: "TableName", kind: ckLength, min: 1, max: 1024},
		{path: "TableName", kind: ckRequired},
		{path: "TimeToLiveSpecification", kind: ckRequired},
		{path: "TimeToLiveSpecification.AttributeName", kind: ckLength, min: 1, max: 255},
		{path: "TimeToLiveSpecification.AttributeName", kind: ckRequired},
		{path: "TimeToLiveSpecification.Enabled", kind: ckRequired},
	},
}
