package dynamodb_test

// Rejection parity for DynamoDB, driven by the cases dzaudit derives from
// AWS's own service model (`dzaudit cases dynamodb`).
//
// The cases are COMMITTED rather than generated at test time. Generating would
// mean fetching a model over the network in CI, and would let the expectations
// change without anyone reviewing the diff — the point of an audit is that
// what it asserts is visible.
//
// # The rule every case obeys
//
// A request refused for the WRONG reason looks exactly like a pass. A mutation
// of a request that was already invalid tells you nothing about the mutation.
// So every case starts from a baseline this test proves the service accepts,
// and a case whose baseline fails is reported unusable rather than counted.
//
// # Nested paths, and the second trap
//
// Two thirds of the model's constraints are not on top-level members but
// inside structures: GlobalSecondaryIndexes[].IndexName,
// TransactItems[].Put.Item. Mutating one means first sending a valid enclosing
// structure — which reopens the same trap one level down. If the exemplar
// standing in for that structure is itself invalid, every case that uses it is
// refused for the exemplar rather than the mutation, and the whole group reads
// as enforced when nothing was tested.
//
// So exemplars are PROBED: for each container path an operation's cases
// descend through, the baseline carrying that exemplar and no mutation at all
// must be accepted. Only then are the mutations under it meaningful. An
// exemplar that fails its probe fails the test rather than quietly passing.
//
// Exemplars are hand-written rather than synthesised from the model for the
// same reason baselines are — valid-to-the-service is more than the model
// states, and a generated "valid" GSI the service rejects would turn every
// nested case into a false pass.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/doze-dev/doze-aws/dynamodb"
)

type auditCase struct {
	Operation       string   `json:"operation"`
	Target          string   `json:"target"`
	Path            string   `json:"path"`
	Why             string   `json:"why"`
	Value           any      `json:"value"`
	Constraint      string   `json:"constraint"`
	RequiredMembers []string `json:"required_members"`
}

// fixture is the state the baselines are written against: one table with a
// partition and sort key, a GSI, and one item in it.
type fixture struct {
	table string
	arn   string
	key   map[string]any
}

// opSpec is one operation's audit: how to build a request the service accepts,
// and known-good values for the containers its nested paths descend through.
//
// baseline may create state of its own — DeleteTable consumes a table, and
// UpdateTable mutates one — which is why it takes the server and a per-call
// sequence number rather than being a plain literal.
type opSpec struct {
	baseline  func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any
	exemplars map[string]any
}

func item(pk, sk string) map[string]any {
	return map[string]any{
		"pk": map[string]any{"S": pk},
		"sk": map[string]any{"S": sk},
	}
}

// freshTable creates a table nothing else will touch, for the operations whose
// baseline changes or destroys one.
func freshTable(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	body := map[string]any{
		"TableName": name,
		"AttributeDefinitions": []any{
			map[string]any{"AttributeName": "pk", "AttributeType": "S"},
			map[string]any{"AttributeName": "sk", "AttributeType": "S"},
		},
		"KeySchema": []any{
			map[string]any{"AttributeName": "pk", "KeyType": "HASH"},
			map[string]any{"AttributeName": "sk", "KeyType": "RANGE"},
		},
		"BillingMode": "PAY_PER_REQUEST",
	}
	if code, resp := send(t, ts, "DynamoDB_20120810.CreateTable", body); code != http.StatusOK {
		t.Fatalf("fixture CreateTable(%s) = %d: %s", name, code, resp)
	}
	return name
}

// specs covers every DynamoDB operation doze-aws dispatches that has
// model-stated constraints on its inputs. Operations it does not dispatch —
// the stubs, and the ones with no handler at all — refuse every request
// including the baseline, so they cannot be audited by this method; that fact
// is recorded in docs/api-support/dynamodb.md rather than papered over here.
var specs = map[string]opSpec{
	"CreateTable": {
		// Deliberately richer than the minimum: PROVISIONED billing with a
		// throughput block, and a range key, because the exemplars have to be
		// valid *in this baseline*. A GSI carrying ProvisionedThroughput is
		// invalid under PAY_PER_REQUEST, and an LSI cannot exist on a table
		// with no range key — a minimal baseline would make those cases
		// untestable rather than merely harder.
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{
				"TableName": fmt.Sprintf("ct-%d", n),
				"AttributeDefinitions": []any{
					map[string]any{"AttributeName": "pk", "AttributeType": "S"},
					map[string]any{"AttributeName": "sk", "AttributeType": "S"},
					map[string]any{"AttributeName": "gsipk", "AttributeType": "S"},
					map[string]any{"AttributeName": "lsisk", "AttributeType": "S"},
				},
				"KeySchema": []any{
					map[string]any{"AttributeName": "pk", "KeyType": "HASH"},
					map[string]any{"AttributeName": "sk", "KeyType": "RANGE"},
				},
				"BillingMode":           "PROVISIONED",
				"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 1, "WriteCapacityUnits": 1},
			}
		},
		exemplars: map[string]any{
			"AttributeDefinitions[]": []any{map[string]any{"AttributeName": "extra", "AttributeType": "S"}},
			"KeySchema[]":            []any{map[string]any{"AttributeName": "pk", "KeyType": "HASH"}},
			"GlobalSecondaryIndexes[]": []any{map[string]any{
				"IndexName":             "gsiok",
				"KeySchema":             []any{map[string]any{"AttributeName": "gsipk", "KeyType": "HASH"}},
				"Projection":            map[string]any{"ProjectionType": "ALL"},
				"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 1, "WriteCapacityUnits": 1},
			}},
			"LocalSecondaryIndexes[]": []any{map[string]any{
				"IndexName": "lsiok",
				"KeySchema": []any{
					map[string]any{"AttributeName": "pk", "KeyType": "HASH"},
					map[string]any{"AttributeName": "lsisk", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			}},
			"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 1, "WriteCapacityUnits": 1},
			"SSESpecification":      map[string]any{"Enabled": false, "SSEType": "AES256"},
			"StreamSpecification":   map[string]any{"StreamEnabled": true, "StreamViewType": "NEW_IMAGE"},
			"Tags[]":                []any{map[string]any{"Key": "k", "Value": "v"}},
			"VectorIndexes[]": []any{map[string]any{
				"IndexName":        "vecok",
				"Dimensions":       4,
				"DistanceFunction": "COSINE",
				"Projection":       map[string]any{"ProjectionType": "ALL"},
				"VectorAttribute":  map[string]any{"AttributeName": "vec"},
				"SearchSchema":     []any{map[string]any{"AttributeName": "pk", "SearchSchemaElementType": "S"}},
			}},
		},
	},

	"UpdateTable": {
		// A fresh table per call: the GSI-create cases really do create one,
		// and running them against the shared fixture would leave every later
		// operation looking at a table the earlier cases had reshaped.
		// AttributeDefinitions is in the baseline rather than left to an
		// exemplar: creating an index names key attributes, and DynamoDB
		// requires a definition for each in the same request. Without it the
		// GSI-create exemplar is refused for the missing definition, which is
		// the wrong-reason refusal this whole file is built to avoid.
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{
				"TableName": freshTable(t, ts, fmt.Sprintf("ut-%d", n)),
				"AttributeDefinitions": []any{
					map[string]any{"AttributeName": "pk", "AttributeType": "S"},
				},
			}
		},
		exemplars: map[string]any{
			"AttributeDefinitions[]": []any{map[string]any{"AttributeName": "pk", "AttributeType": "S"}},
			"GlobalSecondaryIndexUpdates[]": []any{map[string]any{"Create": map[string]any{
				"IndexName":  "gsinew",
				"KeySchema":  []any{map[string]any{"AttributeName": "pk", "KeyType": "HASH"}},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			}}},
			"GlobalSecondaryIndexUpdates[].Delete": map[string]any{"IndexName": "gsigone"},
			"GlobalSecondaryIndexUpdates[].Update": map[string]any{
				"IndexName":             "gsiupd",
				"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 1, "WriteCapacityUnits": 1},
			},
			"GlobalSecondaryIndexUpdates[].Create.ProvisionedThroughput": map[string]any{
				"ReadCapacityUnits": 1, "WriteCapacityUnits": 1,
			},
			"GlobalTableWitnessUpdates[]":        []any{map[string]any{"Create": map[string]any{"RegionName": "us-east-1"}}},
			"GlobalTableWitnessUpdates[].Delete": map[string]any{"RegionName": "us-east-1"},
			"ProvisionedThroughput":              map[string]any{"ReadCapacityUnits": 1, "WriteCapacityUnits": 1},
			"ReplicaUpdates[]":                   []any{map[string]any{"Create": map[string]any{"RegionName": "us-east-1"}}},
			"ReplicaUpdates[].Delete":            map[string]any{"RegionName": "us-east-1"},
			"ReplicaUpdates[].Update":            map[string]any{"RegionName": "us-east-1"},
			"SSESpecification":                   map[string]any{"Enabled": false, "SSEType": "AES256"},
			"StreamSpecification":                map[string]any{"StreamEnabled": false},
			"VectorIndexUpdates[]": []any{map[string]any{"Create": map[string]any{
				"IndexName":        "vecnew",
				"Dimensions":       4,
				"DistanceFunction": "COSINE",
				"Projection":       map[string]any{"ProjectionType": "ALL"},
				"VectorAttribute":  map[string]any{"AttributeName": "vec"},
			}}},
			"VectorIndexUpdates[].Delete":                 map[string]any{"IndexName": "vecgone"},
			"VectorIndexUpdates[].Create.VectorAttribute": map[string]any{"AttributeName": "vec"},
		},
	},

	"DeleteTable": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": freshTable(t, ts, fmt.Sprintf("dt-%d", n))}
		},
	},

	"DescribeTable":               {baseline: tableOnly},
	"DescribeTimeToLive":          {baseline: tableOnly},
	"DescribeContinuousBackups":   {baseline: tableOnly},
	"DescribeContributorInsights": {baseline: tableOnly},
	"Scan":                        {baseline: tableOnly, exemplars: filterExemplar("ScanFilter{}")},

	"ListTables": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{}
		},
	},

	"PutItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "Item": item(fmt.Sprintf("p-%d", n), "s")}
		},
		exemplars: expectedExemplar(),
	},
	"GetItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "Key": f.key}
		},
	},
	"DeleteItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "Key": item(fmt.Sprintf("d-%d", n), "s")}
		},
		exemplars: expectedExemplar(),
	},
	"UpdateItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{
				"TableName":                 f.table,
				"Key":                       item(fmt.Sprintf("u-%d", n), "s"),
				"UpdateExpression":          "SET #a = :v",
				"ExpressionAttributeNames":  map[string]any{"#a": "note"},
				"ExpressionAttributeValues": map[string]any{":v": map[string]any{"S": "x"}},
			}
		},
		exemplars: merge(expectedExemplar(), map[string]any{
			"AttributeUpdates{}": map[string]any{"note": map[string]any{
				"Action": "PUT", "Value": map[string]any{"S": "x"},
			}},
		}),
	},
	"Query": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{
				"TableName":                 f.table,
				"KeyConditionExpression":    "pk = :p",
				"ExpressionAttributeValues": map[string]any{":p": map[string]any{"S": "p"}},
			}
		},
		exemplars: merge(filterExemplar("QueryFilter{}"), filterExemplar("KeyConditions{}")),
	},

	"BatchGetItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"RequestItems": map[string]any{
				f.table: map[string]any{"Keys": []any{f.key}},
			}}
		},
	},
	"BatchWriteItem": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"RequestItems": map[string]any{
				f.table: []any{map[string]any{"PutRequest": map[string]any{
					"Item": item(fmt.Sprintf("bw-%d", n), "s"),
				}}},
			}}
		},
		exemplars: map[string]any{
			"RequestItems{}[].DeleteRequest": map[string]any{"Key": item("bwdel", "s")},
		},
	},
	"TransactGetItems": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TransactItems": []any{map[string]any{
				"Get": map[string]any{"TableName": f.table, "Key": f.key},
			}}}
		},
	},
	"TransactWriteItems": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TransactItems": []any{map[string]any{
				"Put": map[string]any{"TableName": f.table, "Item": item(fmt.Sprintf("tw-%d", n), "s")},
			}}}
		},
		exemplars: map[string]any{
			"TransactItems[].ConditionCheck": map[string]any{
				"TableName":           "AUDIT_TABLE",
				"Key":                 item("tcc", "s"),
				"ConditionExpression": "attribute_not_exists(pk)",
			},
			"TransactItems[].Delete": map[string]any{
				"TableName": "AUDIT_TABLE", "Key": item("tdel", "s"),
			},
			"TransactItems[].Update": map[string]any{
				"TableName":                 "AUDIT_TABLE",
				"Key":                       item("tupd", "s"),
				"UpdateExpression":          "SET #a = :v",
				"ExpressionAttributeNames":  map[string]any{"#a": "note"},
				"ExpressionAttributeValues": map[string]any{":v": map[string]any{"S": "x"}},
			},
		},
	},

	"ExecuteStatement": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"Statement": fmt.Sprintf("SELECT * FROM %q WHERE pk = 'p'", f.table)}
		},
	},
	"BatchExecuteStatement": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"Statements": []any{map[string]any{
				"Statement": fmt.Sprintf("SELECT * FROM %q WHERE pk = 'p'", f.table),
			}}}
		},
	},
	"ExecuteTransaction": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TransactStatements": []any{map[string]any{
				"Statement": fmt.Sprintf("INSERT INTO %q VALUE {'pk':'et-%d','sk':'s'}", f.table, n),
			}}}
		},
	},

	"UpdateTimeToLive": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "TimeToLiveSpecification": map[string]any{
				"Enabled": false, "AttributeName": "ttl",
			}}
		},
		exemplars: map[string]any{
			"TimeToLiveSpecification": map[string]any{"Enabled": false, "AttributeName": "ttl"},
		},
	},
	"UpdateContinuousBackups": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "PointInTimeRecoverySpecification": map[string]any{
				"PointInTimeRecoveryEnabled": false,
			}}
		},
		exemplars: map[string]any{
			"PointInTimeRecoverySpecification": map[string]any{"PointInTimeRecoveryEnabled": false},
		},
	},
	"UpdateContributorInsights": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"TableName": f.table, "ContributorInsightsAction": "ENABLE"}
		},
	},

	"TagResource": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"ResourceArn": f.arn, "Tags": []any{
				map[string]any{"Key": "k", "Value": "v"},
			}}
		},
		exemplars: map[string]any{
			"Tags[]": []any{map[string]any{"Key": "k", "Value": "v"}},
		},
	},
	"UntagResource": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"ResourceArn": f.arn, "TagKeys": []any{"k"}}
		},
	},
	"ListTagsOfResource": {
		baseline: func(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
			return map[string]any{"ResourceArn": f.arn}
		},
	},
}

func tableOnly(t *testing.T, ts *httptest.Server, f fixture, n int) map[string]any {
	return map[string]any{"TableName": f.table}
}

// filterExemplar builds the legacy condition-map shape shared by ScanFilter,
// QueryFilter and KeyConditions.
func filterExemplar(path string) map[string]any {
	return map[string]any{path: map[string]any{"pk": map[string]any{
		"ComparisonOperator": "EQ",
		"AttributeValueList": []any{map[string]any{"S": "p"}},
	}}}
}

// expectedExemplar is the legacy Expected map, in the one form that is true of
// any item: an attribute that does not exist.
func expectedExemplar() map[string]any {
	return map[string]any{"Expected{}": map[string]any{"absent": map[string]any{
		"ComparisonOperator": "NULL",
	}}}
}

func merge(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// segRE parses a path segment into its member name and container markers.
//
// Deliberately a second implementation of what validate.go parses, rather than
// a call into it: a test that shares the parser it is checking cannot catch a
// parser that is wrong in the same way twice.
var segRE = regexp.MustCompile(`^([A-Za-z0-9]+)((?:\[\]|\{\})*)$`)

func splitSeg(seg string) (string, []string) {
	m := segRE.FindStringSubmatch(seg)
	if m == nil {
		return seg, nil
	}
	var mk []string
	for i := 0; i+1 < len(m[2]); i += 2 {
		mk = append(mk, m[2][i:i+2])
	}
	return m[1], mk
}

// applyCase builds a case's request: the baseline, with the constrained path
// set to the violating value — injecting exemplars for every container the
// path passes through. A nil value means the case is about the member being
// absent, so the leaf is deleted instead.
//
// mutate=false stops before the leaf, which is how a container probe asks
// "is the baseline still accepted once this exemplar is in it?".
//
// It returns an error rather than failing the test, because a path this cannot
// build is a hole in the harness, not a finding about the service, and the two
// must never be reported as the same thing.
func applyCase(body map[string]any, exemplars map[string]any, path string, value any, mutate bool) error {
	segs := strings.Split(path, ".")
	cur := body

	for i, seg := range segs[:len(segs)-1] {
		name, markers := splitSeg(seg)
		key := strings.Join(segs[:i+1], ".")
		if _, present := cur[name]; !present {
			ex, ok := exemplars[key]
			if !ok {
				return fmt.Errorf("no exemplar for container %q (needed by %q)", key, path)
			}
			cur[name] = deepCopy(ex)
		}

		v := cur[name]
		for _, mk := range markers {
			switch mk {
			case "[]":
				lst, ok := v.([]any)
				if !ok || len(lst) == 0 {
					return fmt.Errorf("container %q is not a non-empty list", key)
				}
				v = lst[0]
			case "{}":
				m, ok := v.(map[string]any)
				if !ok || len(m) == 0 {
					return fmt.Errorf("container %q is not a non-empty map", key)
				}
				ks := make([]string, 0, len(m))
				for k := range m {
					ks = append(ks, k)
				}
				sort.Strings(ks)
				v = m[ks[0]]
			}
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("container %q is not a structure", key)
		}
		cur = m
	}

	if !mutate {
		return nil
	}

	leaf, markers := splitSeg(segs[len(segs)-1])
	if value == nil {
		delete(cur, leaf) // a @required case is the member's absence
		return nil
	}
	// A constrained leaf under markers bounds the ELEMENTS — AttributesToGet[]
	// bounds the strings in the list — so the violating value is wrapped back
	// up through the markers, innermost first.
	wrapped := value
	for i := len(markers) - 1; i >= 0; i-- {
		switch markers[i] {
		case "[]":
			wrapped = []any{wrapped}
		case "{}":
			wrapped = map[string]any{"k": wrapped}
		}
	}
	cur[leaf] = wrapped
	return nil
}

// deepCopy so one case's mutation cannot leak into the next through a shared
// exemplar — the kind of cross-talk that makes a suite pass in isolation and
// fail in order.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v
	}
}

func newDDB(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := dynamodb.New(dynamodb.Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

// send posts an awsJson request: the target header plus a JSON body, which is
// the whole of the protocol.
func send(t *testing.T, ts *httptest.Server, target string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/dynamodb/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := make([]byte, 400)
	n, _ := resp.Body.Read(out)
	return resp.StatusCode, string(out[:n])
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_dynamodb.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []auditCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("no cases: the audit would pass vacuously")
	}
	return cs
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run, keyed "Operation/path/why".
//
// They are listed rather than silently tolerated so the set is reviewable, and
// so anything NOT on the list fails the build the moment it appears. Removing
// an entry is the point: fix the validation, delete the line. A gap that starts
// being enforced also fails, with a note to delete it, so the list cannot rot
// into a lie about what is broken.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// setUpFixture creates the table the baselines are written against.
func setUpFixture(t *testing.T, ts *httptest.Server) fixture {
	f := fixture{
		table: "audit",
		arn:   "arn:aws:dynamodb:us-east-1:000000000000:table/audit",
		key:   item("p", "s"),
	}
	body := map[string]any{
		"TableName": f.table,
		"AttributeDefinitions": []any{
			map[string]any{"AttributeName": "pk", "AttributeType": "S"},
			map[string]any{"AttributeName": "sk", "AttributeType": "S"},
			map[string]any{"AttributeName": "gsipk", "AttributeType": "S"},
		},
		"KeySchema": []any{
			map[string]any{"AttributeName": "pk", "KeyType": "HASH"},
			map[string]any{"AttributeName": "sk", "KeyType": "RANGE"},
		},
		"BillingMode": "PAY_PER_REQUEST",
		"GlobalSecondaryIndexes": []any{map[string]any{
			"IndexName":  "auditgsi",
			"KeySchema":  []any{map[string]any{"AttributeName": "gsipk", "KeyType": "HASH"}},
			"Projection": map[string]any{"ProjectionType": "ALL"},
		}},
	}
	if code, resp := send(t, ts, "DynamoDB_20120810.CreateTable", body); code != http.StatusOK {
		t.Fatalf("fixture CreateTable = %d: %s", code, resp)
	}
	if code, resp := send(t, ts, "DynamoDB_20120810.PutItem",
		map[string]any{"TableName": f.table, "Item": item("p", "s")}); code != http.StatusOK {
		t.Fatalf("fixture PutItem = %d: %s", code, resp)
	}
	return f
}

// resolveTable substitutes the fixture's table name into exemplars that need
// it. Exemplars are literals so they stay readable; this is the one value they
// cannot know at the time they are written.
func resolveTable(v any, table string) any {
	switch t := v.(type) {
	case string:
		if t == "AUDIT_TABLE" {
			return table
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = resolveTable(val, table)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = resolveTable(val, table)
		}
		return out
	}
	return v
}

func TestRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := newDDB(t)
	f := setUpFixture(t, ts)

	byOp := map[string][]auditCase{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
	}

	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	seq := 0
	next := func() int { seq++; return seq }

	var totalCases, totalGaps int
	for _, op := range ops {
		spec, ok := specs[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no spec — either add one, "+
				"or drop the operation from testdata with a reason", op, len(byOp[op]))
			continue
		}
		ex := map[string]any{}
		for k, v := range spec.exemplars {
			ex[k] = resolveTable(v, f.table)
		}

		t.Run(op, func(t *testing.T) {
			// 1. The baseline must be accepted, or every case below is a
			// mutation of something already invalid and proves nothing.
			base := spec.baseline(t, ts, f, next())
			if code, body := send(t, ts, byOp[op][0].Target, base); code != http.StatusOK {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless", code, body, op)
			}

			// 2. Every container an exemplar stands in for must ALSO leave the
			// baseline accepted. Without this, a case under a bad exemplar is
			// refused for the exemplar and reads as enforced.
			probed := map[string]bool{}
			for _, c := range byOp[op] {
				segs := strings.Split(c.Path, ".")
				if len(segs) == 1 {
					continue
				}
				prefix := strings.Join(segs[:len(segs)-1], ".")
				if probed[prefix] {
					continue
				}
				probed[prefix] = true
				body := spec.baseline(t, ts, f, next())
				if err := applyCase(body, ex, c.Path, nil, false); err != nil {
					t.Errorf("container %s: %v", prefix, err)
					continue
				}
				if code, resp := send(t, ts, c.Target, body); code != http.StatusOK {
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s\n"+
						"  Every case under it would be refused for the exemplar, not the mutation.",
						prefix, code, resp)
				}
			}

			// 3. The cases themselves.
			var gaps []string
			for _, c := range byOp[op] {
				totalCases++
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := spec.baseline(t, ts, f, next())
					if err := applyCase(body, ex, c.Path, c.Value, true); err != nil {
						t.Fatalf("could not build the case: %v\n"+
							"This is a hole in the harness, not a finding about the service.", err)
					}

					key := op + "/" + c.Path + "/" + c.Why
					code, resp := send(t, ts, c.Target, body)

					if code == http.StatusOK {
						// Accepted. AWS refuses this, so doze-aws is more
						// permissive than production — the direction that hides
						// bugs until deploy.
						gaps = append(gaps, fmt.Sprintf("%s (%s)", key, c.Constraint))
						if !knownGaps[key] {
							t.Errorf("accepted %s = %v\n  AWS refuses it: %s\n  constraint: %s\n"+
								"  This is a NEW gap. Fix it, or add %q to knownGaps with a reason.",
								c.Path, c.Value, c.Why, c.Constraint, key)
						}
						return
					}
					if code >= 500 {
						t.Fatalf("%s = %d (a refusal should be a 4xx): %s", c.Path, code, resp)
					}
					if knownGaps[key] {
						t.Errorf("%s is enforced now — delete it from knownGaps, or the "+
							"list stops describing what is actually broken", key)
					}
				})
			}
			totalGaps += len(gaps)
			t.Logf("%s: %d/%d enforced", op, len(byOp[op])-len(gaps), len(byOp[op]))
			for _, g := range gaps {
				t.Logf("  gap: %s", g)
			}
		})
	}

	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations",
		totalCases-totalGaps, totalCases, len(ops))
	if totalGaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", totalGaps, len(knownGaps))
	}
}
