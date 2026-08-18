package kinesis

// Rejection parity for Kinesis, driven by the cases dzaudit derives from AWS's
// own service model (`dzaudit cases kinesis`).
//
// Same method as DynamoDB's, and the same rule underneath it: a request refused
// for the WRONG reason looks exactly like a pass, so every case is a mutation
// of a baseline this test first proves the service accepts.
//
// Kinesis is a much easier shape than DynamoDB — 395 of its 425 constraints sit
// on top-level members rather than inside nested structures — so the exemplar
// machinery is not needed here. What it needs instead is state: most operations
// address a stream, a shard or a consumer that has to exist, and a baseline
// naming a stream that does not exist is refused with ResourceNotFoundException
// and proves nothing about validation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"

	"github.com/doze-dev/doze-aws/internal/auditkit"
	"testing"
)

type auditCase struct {
	Operation  string `json:"operation"`
	Target     string `json:"target"`
	Path       string `json:"path"`
	Why        string `json:"why"`
	Value      any    `json:"value"`
	Constraint string `json:"constraint"`
}

// fixture is the state the baselines address: one stream with a known shard,
// and one registered consumer on it.
type fixture struct {
	stream    string
	streamARN string
	shardID   string
	consumer  string
}

func kinesisServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

// call posts one awsJson1_1 request: the target header plus a JSON body.
func call(t *testing.T, ts *httptest.Server, target string, body map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/", bytes.NewReader(raw))
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20200101/us-east-1/kinesis/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// The whole body: the fixture parses a real DescribeStream response, and a
	// bounded read truncated it into an unmarshal error that looked like a
	// service failure.
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func setUpFixture(t *testing.T, ts *httptest.Server) fixture {
	t.Helper()
	f := fixture{stream: "audit", consumer: "auditor"}
	if code, body := call(t, ts, "Kinesis_20131202.CreateStream",
		map[string]any{"StreamName": f.stream, "ShardCount": 2}); code != http.StatusOK {
		t.Fatalf("fixture CreateStream = %d: %s", code, body)
	}
	code, body := call(t, ts, "Kinesis_20131202.DescribeStream", map[string]any{"StreamName": f.stream})
	if code != http.StatusOK {
		t.Fatalf("fixture DescribeStream = %d: %s", code, body)
	}
	var desc struct {
		StreamDescription struct {
			StreamARN string `json:"StreamARN"`
			Shards    []struct {
				ShardID string `json:"ShardId"`
			} `json:"Shards"`
		} `json:"StreamDescription"`
	}
	if err := json.Unmarshal([]byte(body), &desc); err != nil {
		t.Fatalf("fixture DescribeStream body: %v\n%s", err, body)
	}
	if len(desc.StreamDescription.Shards) == 0 {
		t.Fatalf("fixture stream has no shards: %s", body)
	}
	f.streamARN = desc.StreamDescription.StreamARN
	f.shardID = desc.StreamDescription.Shards[0].ShardID

	call(t, ts, "Kinesis_20131202.RegisterStreamConsumer",
		map[string]any{"StreamARN": f.streamARN, "ConsumerName": f.consumer})
	return f
}

// baselines are requests the service must accept, one per operation.
//
// Several are deliberately harmless rather than representative: DeleteStream
// names a throwaway stream, and the reshard operations name shards that make a
// valid request without reshaping the fixture every other case depends on.
func baselines(f fixture) map[string]map[string]any {
	stream := map[string]any{"StreamName": f.stream}
	arn := map[string]any{"ResourceARN": f.streamARN}
	return map[string]map[string]any{
		"CreateStream":                  {"StreamName": "made-by-baseline", "ShardCount": 1},
		"DeleteStream":                  {"StreamName": "made-by-baseline"},
		"ListStreams":                   {},
		"DescribeStream":                stream,
		"DescribeStreamSummary":         stream,
		"UpdateStreamMode":              {"StreamARN": f.streamARN, "StreamModeDetails": map[string]any{"StreamMode": "PROVISIONED"}},
		"UpdateMaxRecordSize":           {"StreamName": f.stream, "MaxRecordSizeInKiB": 1024},
		"IncreaseStreamRetentionPeriod": {"StreamName": f.stream, "RetentionPeriodHours": 48},
		"DecreaseStreamRetentionPeriod": {"StreamName": f.stream, "RetentionPeriodHours": 24},
		"UpdateShardCount":              {"StreamName": f.stream, "TargetShardCount": 2, "ScalingType": "UNIFORM_SCALING"},
		"ListShards":                    stream,
		"GetShardIterator":              {"StreamName": f.stream, "ShardId": f.shardID, "ShardIteratorType": "TRIM_HORIZON"},
		"PutRecord":                     {"StreamName": f.stream, "PartitionKey": "k", "Data": "eA=="},
		"PutRecords": {"StreamName": f.stream, "Records": []any{
			map[string]any{"PartitionKey": "k", "Data": "eA=="},
		}},
		"AddTagsToStream":           {"StreamName": f.stream, "Tags": map[string]any{"env": "dev"}},
		"RemoveTagsFromStream":      {"StreamName": f.stream, "TagKeys": []any{"env"}},
		"ListTagsForStream":         stream,
		"TagResource":               {"ResourceARN": f.streamARN, "Tags": map[string]any{"env": "dev"}},
		"UntagResource":             {"ResourceARN": f.streamARN, "TagKeys": []any{"env"}},
		"ListTagsForResource":       arn,
		"EnableEnhancedMonitoring":  {"StreamName": f.stream, "ShardLevelMetrics": []any{"ALL"}},
		"DisableEnhancedMonitoring": {"StreamName": f.stream, "ShardLevelMetrics": []any{"ALL"}},
		"StartStreamEncryption":     {"StreamName": f.stream, "EncryptionType": "KMS", "KeyId": "alias/aws/kinesis"},
		"StopStreamEncryption":      {"StreamName": f.stream, "EncryptionType": "KMS", "KeyId": "alias/aws/kinesis"},
		"RegisterStreamConsumer":    {"StreamARN": f.streamARN, "ConsumerName": "baseline-consumer"},
		"DeregisterStreamConsumer":  {"StreamARN": f.streamARN, "ConsumerName": "baseline-consumer"},
		"DescribeStreamConsumer":    {"StreamARN": f.streamARN, "ConsumerName": f.consumer},
		"ListStreamConsumers":       {"StreamARN": f.streamARN},
		"PutResourcePolicy":         {"ResourceARN": f.streamARN, "Policy": `{"Version":"2012-10-17","Statement":[]}`},
		"GetResourcePolicy":         arn,
		"DeleteResourcePolicy":      arn,
		"UpdateAccountSettings":     {"MinimumThroughputBillingCommitment": map[string]any{"Status": "DISABLED"}},
	}
}

// exemplars stand in for containers a baseline does not already carry. Kinesis
// needs very few — most of its nested paths reach into a structure the baseline
// already sends, so the walker only has to navigate.
func exemplars(f fixture) map[string]any {
	return map[string]any{
		"ShardFilter":                        map[string]any{"Type": "AT_TRIM_HORIZON"},
		"MinimumThroughputBillingCommitment": map[string]any{"Status": "DISABLED"},
		// CreateStream takes this optionally, so its baseline does not carry it.
		"StreamModeDetails": map[string]any{"StreamMode": "PROVISIONED"},
	}
}

// prepare adjusts a request just before it is sent, for the two operations
// whose baseline is not idempotent.
//
// CreateStream needs a name nobody has used, or the second request in the
// group is refused with ResourceInUseException — a refusal for the wrong reason,
// which is the failure this whole file is built to avoid. DeleteStream needs
// its target to exist, so it makes one.
func prepare(t *testing.T, ts *httptest.Server, op, mutating string, body map[string]any, n int) {
	t.Helper()
	// Never touch the member the case is about: overwriting it would make the
	// request valid again and report a gap that is purely this helper's doing.
	if mutating == "StreamName" || mutating == "ConsumerName" {
		return
	}
	switch op {
	case "CreateStream":
		body["StreamName"] = fmt.Sprintf("created-%d", n)
	case "DeleteStream":
		name := fmt.Sprintf("doomed-%d", n)
		call(t, ts, "Kinesis_20131202.CreateStream",
			map[string]any{"StreamName": name, "ShardCount": 1})
		body["StreamName"] = name
	case "RegisterStreamConsumer":
		// A consumer name nobody has used, or the second request in the group
		// is refused with ResourceInUseException.
		body["ConsumerName"] = fmt.Sprintf("consumer-%d", n)
	case "DeregisterStreamConsumer":
		// Operations run in alphabetical order, so this one is reached before
		// RegisterStreamConsumer has made anything. Every baseline creates its
		// own preconditions rather than depending on another group having run.
		if arn, ok := body["StreamARN"].(string); ok {
			name := fmt.Sprintf("doomed-consumer-%d", n)
			call(t, ts, "Kinesis_20131202.RegisterStreamConsumer",
				map[string]any{"StreamARN": arn, "ConsumerName": name})
			body["ConsumerName"] = name
		}
	case "GetResourcePolicy", "DeleteResourcePolicy":
		// DeleteResourcePolicy sorts first and removes what Get would read.
		if arn, ok := body["ResourceARN"].(string); ok {
			call(t, ts, "Kinesis_20131202.PutResourcePolicy", map[string]any{
				"ResourceARN": arn, "Policy": `{"Version":"2012-10-17","Statement":[]}`,
			})
		}
	}
}

// knownGaps are constraints AWS enforces and doze-aws does not, as of the last
// run. Listed rather than tolerated silently, so the set is reviewable and
// anything NOT on the list fails the moment it appears.
var knownGaps = map[string]bool{
	// Empty, and that is the goal.
}

// needState are operations whose baseline the audit cannot construct without
// reshaping the fixture every other case reads. They are skipped WITH A REASON
// rather than quietly dropped, because a case nobody ran is not a case that
// passed.
var needState = map[string]string{
	"GetRecords":  "needs a live shard iterator, which expires and is consumed by the read",
	"SplitShard":  "reshapes the fixture stream every other operation addresses",
	"MergeShards": "reshapes the fixture stream every other operation addresses",
}

func loadCases(t *testing.T) []auditCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cases_kinesis.json"))
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

func TestKinesisRejectsWhatTheModelForbids(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a store")
	}
	ts := kinesisServer(t)
	f := setUpFixture(t, ts)
	base := baselines(f)
	ex := exemplars(f)

	// One counter across the run, so every non-idempotent request names
	// something nothing else has touched.
	n := 0
	seq := func() int { n++; return n }

	byOp := map[string][]auditCase{}
	for _, c := range loadCases(t) {
		byOp[c.Operation] = append(byOp[c.Operation], c)
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	var total, gaps, skipped, unbuildable int
	for _, op := range ops {
		if why, ok := needState[op]; ok {
			skipped += len(byOp[op])
			t.Logf("skipping %s (%d cases): %s", op, len(byOp[op]), why)
			continue
		}
		b, ok := base[op]
		if !ok {
			t.Errorf("%s has %d model-derived cases and no baseline — add one, or "+
				"record it in needState with a reason", op, len(byOp[op]))
			continue
		}

		t.Run(op, func(t *testing.T) {
			// The baseline has to be accepted, or every case below is a
			// mutation of something already invalid and proves nothing.
			bl := auditkit.DeepCopy(b).(map[string]any)
			prepare(t, ts, op, "", bl, seq())
			if code, body := call(t, ts, byOp[op][0].Target, bl); code != http.StatusOK {
				t.Fatalf("the baseline request was refused (%d): %s\nevery %s case would be meaningless",
					code, body, op)
			}

			// Every container an exemplar stands in for must leave the baseline
			// acceptable, or the cases under it are refused for the exemplar
			// and the group reads as enforced when nothing was tested.
			paths := make([]string, 0, len(byOp[op]))
			for _, c := range byOp[op] {
				paths = append(paths, c.Path)
			}
			for _, prefix := range auditkit.Containers(paths) {
				probe := auditkit.DeepCopy(b).(map[string]any)
				if err := auditkit.Apply(probe, ex, prefix+".probe", nil, false); err != nil {
					t.Errorf("container %s: %v", prefix, err)
					continue
				}
				prepare(t, ts, op, "", probe, seq())
				if code, resp := call(t, ts, byOp[op][0].Target, probe); code != http.StatusOK {
					t.Errorf("the exemplar for %q makes the baseline invalid (%d): %s\n"+
						"  Every case under it would be refused for the exemplar, not the mutation.",
						prefix, code, resp)
				}
			}

			for _, c := range byOp[op] {
				total++
				t.Run(c.Path+"/"+c.Why, func(t *testing.T) {
					body := auditkit.DeepCopy(b).(map[string]any)
					if err := auditkit.Apply(body, ex, c.Path, c.Value, true); err != nil {
						unbuildable++
						t.Fatalf("could not build the case: %v\n"+
							"This is a hole in the harness, not a finding about the service.", err)
					}
					prepare(t, ts, op, c.Path, body, seq())

					key := op + "/" + c.Path + "/" + c.Why
					code, resp := call(t, ts, c.Target, body)
					if code == http.StatusOK {
						// Accepted. AWS refuses this, so doze-aws is more
						// permissive than production — the direction that hides
						// bugs until deploy.
						gaps++
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
						t.Errorf("%s is enforced now — delete it from knownGaps, or the list "+
							"stops describing what is actually broken", key)
					}
				})
			}
		})
	}

	// Report the three outcomes apart. Folding unbuildable cases into the
	// enforced count would let a hole in the harness read as coverage, which is
	// the failure this whole method exists to avoid.
	t.Logf("TOTAL: %d/%d model-derived constraints enforced across %d operations "+
		"(%d skipped for state, %d unbuildable)",
		total-gaps-unbuildable, total, len(ops)-len(needState), skipped, unbuildable)
	if unbuildable > 0 {
		t.Errorf("%d cases could not be built — the harness is missing an exemplar "+
			"or a baseline, and those cases tested nothing", unbuildable)
	}
	if gaps > len(knownGaps) {
		t.Errorf("%d gaps but only %d are known", gaps, len(knownGaps))
	}
}
