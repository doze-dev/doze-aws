package console

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/awsident"
)

// ---- Step Functions: Express, TestState, redrive, Map Runs, activities ----
//
// The operations past the Standard-workflow core. Each answers a shape the
// console renders as one panel, so the structs here are those panels' rows
// rather than the wire's full output.

// SyncResult is what StartSyncExecution answers: the whole run, output
// included, in one response — there is no execution record to visit after.
type SyncResult struct {
	ARN      string
	Name     string
	Status   string
	Started  string
	Stopped  string
	Input    string
	Output   string
	Error    string
	Cause    string
	BilledMs int64
	BilledMB int
}

func (b *backend) StartSyncExecution(ctx context.Context, machineARN, name, input string) (SyncResult, error) {
	in := map[string]any{"stateMachineArn": machineARN}
	if name != "" {
		in["name"] = name
	}
	if strings.TrimSpace(input) != "" {
		in["input"] = input
	}
	body, err := b.sfnCall(ctx, "StartSyncExecution", in)
	if err != nil {
		return SyncResult{}, err
	}
	var out struct {
		ARN       string  `json:"executionArn"`
		Name      string  `json:"name"`
		Status    string  `json:"status"`
		StartDate float64 `json:"startDate"`
		StopDate  float64 `json:"stopDate"`
		Input     string  `json:"input"`
		Output    string  `json:"output"`
		Error     string  `json:"error"`
		Cause     string  `json:"cause"`
		Billing   struct {
			Ms int64 `json:"billedDurationInMilliseconds"`
			MB int   `json:"billedMemoryUsedInMB"`
		} `json:"billingDetails"`
	}
	json.Unmarshal(body, &out)
	return SyncResult{
		ARN: out.ARN, Name: out.Name, Status: out.Status,
		Started: epochToTime(out.StartDate), Stopped: epochToTime(out.StopDate),
		Input: prettyJSON(out.Input), Output: prettyJSON(out.Output), Error: out.Error, Cause: out.Cause,
		BilledMs: out.Billing.Ms, BilledMB: out.Billing.MB,
	}, nil
}

// TestMock stands in for a Task's integration during TestState: a result,
// or an error and cause. A nil mock lets the Task call its real resource.
type TestMock struct {
	Result string
	Error  string
	Cause  string
}

// Inspection is one row of inspectionData, in the order the state applied
// them — input, after InputPath, after Parameters, the result, after
// ResultSelector, after ResultPath — so the panel reads as the pipeline.
type Inspection struct {
	Key   string
	Value string
}

// TestResult is TestState's answer.
type TestResult struct {
	Status     string
	NextState  string
	Output     string
	Error      string
	Cause      string
	Inspection []Inspection
}

// inspectionOrder is the pipeline order; request and response (TRACE) last.
var inspectionOrder = []string{"input", "afterInputPath", "afterParameters", "result", "afterResultSelector", "afterResultPath", "request", "response"}

func (b *backend) TestState(ctx context.Context, definition, state, input, level string, mock *TestMock) (TestResult, error) {
	in := map[string]any{"definition": definition, "stateName": state, "inspectionLevel": level}
	if strings.TrimSpace(input) != "" {
		in["input"] = input
	}
	if mock != nil {
		m := map[string]any{}
		if mock.Error != "" {
			m["errorOutput"] = map[string]any{"error": mock.Error, "cause": mock.Cause}
		} else {
			m["result"] = mock.Result
		}
		in["mock"] = m
	}
	body, err := b.sfnCall(ctx, "TestState", in)
	if err != nil {
		return TestResult{}, err
	}
	var out struct {
		Status     string         `json:"status"`
		NextState  string         `json:"nextState"`
		Output     string         `json:"output"`
		Error      string         `json:"error"`
		Cause      string         `json:"cause"`
		Inspection map[string]any `json:"inspectionData"`
	}
	json.Unmarshal(body, &out)
	res := TestResult{Status: out.Status, NextState: out.NextState, Output: prettyJSON(out.Output), Error: out.Error, Cause: out.Cause}
	for _, k := range inspectionOrder {
		v, ok := out.Inspection[k]
		if !ok {
			continue
		}
		// request and response are {body: "..."} envelopes; the rest are
		// JSON-encoded strings.
		if env, isEnv := v.(map[string]any); isEnv {
			v = env["body"]
		}
		res.Inspection = append(res.Inspection, Inspection{Key: k, Value: prettyJSON(str(v))})
	}
	return res, nil
}

func (b *backend) RedriveExecution(ctx context.Context, arn string) error {
	_, err := b.sfnCall(ctx, "RedriveExecution", map[string]any{"executionArn": arn})
	return err
}

// SendTaskHeartbeat keeps a parked token alive past its HeartbeatSeconds.
func (b *backend) SendTaskHeartbeat(ctx context.Context, token string) error {
	_, err := b.sfnCall(ctx, "SendTaskHeartbeat", map[string]any{"taskToken": token})
	return err
}

// MapCounts is a Map Run's item tally.
type MapCounts struct {
	Pending, Running, Succeeded, Failed, TimedOut, Aborted, Total int
}

// MapRun is what a Distributed Map state leaves behind.
type MapRun struct {
	ARN            string
	Label          string // the ARN's mapRun:<machine>/<exec>[/<label>]:<id> middle
	ExecARN        string
	Status         string
	Started        string
	Stopped        string
	MaxConcurrency int
	Counts         MapCounts
	RedriveCount   int
	TolCount       int
	TolPct         float64
}

// mapRunLabel reads the state label out of a Map Run ARN, "" when the Map
// state carried none: arn:...:mapRun:<machine>/<exec>/<label>:<id>.
func mapRunLabel(arn string) string {
	_, rest, ok := strings.Cut(arn, ":mapRun:")
	if !ok {
		return ""
	}
	path, _, _ := strings.Cut(rest, ":")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func (b *backend) DescribeMapRun(ctx context.Context, arn string) (MapRun, error) {
	body, err := b.sfnCall(ctx, "DescribeMapRun", map[string]any{"mapRunArn": arn})
	if err != nil {
		return MapRun{}, err
	}
	var out struct {
		ARN            string  `json:"mapRunArn"`
		ExecARN        string  `json:"executionArn"`
		Status         string  `json:"status"`
		StartDate      float64 `json:"startDate"`
		StopDate       float64 `json:"stopDate"`
		MaxConcurrency int     `json:"maxConcurrency"`
		RedriveCount   int     `json:"redriveCount"`
		TolCount       int     `json:"toleratedFailureCount"`
		TolPct         float64 `json:"toleratedFailurePercentage"`
		Counts         struct {
			Pending, Running, Succeeded, Failed, TimedOut, Aborted, Total int
		} `json:"itemCounts"`
	}
	json.Unmarshal(body, &out)
	return MapRun{
		ARN: out.ARN, Label: mapRunLabel(out.ARN), ExecARN: out.ExecARN, Status: out.Status,
		Started: epochToTime(out.StartDate), Stopped: epochToTime(out.StopDate),
		MaxConcurrency: out.MaxConcurrency, RedriveCount: out.RedriveCount,
		TolCount: out.TolCount, TolPct: out.TolPct,
		Counts: MapCounts(out.Counts),
	}, nil
}

// ListMapRuns lists an execution's Map Runs, each described: the list call
// is ARNs and dates, and the counts are the panel.
func (b *backend) ListMapRuns(ctx context.Context, execARN string) ([]MapRun, error) {
	body, err := b.sfnCall(ctx, "ListMapRuns", map[string]any{"executionArn": execARN, "maxResults": 1000})
	if err != nil {
		return nil, err
	}
	var out struct {
		Runs []struct {
			ARN string `json:"mapRunArn"`
		} `json:"mapRuns"`
	}
	json.Unmarshal(body, &out)
	runs := make([]MapRun, 0, len(out.Runs))
	for _, item := range out.Runs {
		mr, err := b.DescribeMapRun(ctx, item.ARN)
		if err != nil {
			mr = MapRun{ARN: item.ARN, Label: mapRunLabel(item.ARN)}
		}
		runs = append(runs, mr)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started < runs[j].Started })
	return runs, nil
}

func (b *backend) UpdateMapRun(ctx context.Context, arn string, maxConcurrency int) error {
	_, err := b.sfnCall(ctx, "UpdateMapRun", map[string]any{"mapRunArn": arn, "maxConcurrency": maxConcurrency})
	return err
}

// Activity is a queue a worker polls; the console can be that worker.
type Activity struct {
	Name    string
	ARN     string
	Created string
}

// ActivityTask is one claimed unit of work: the token to redeem and the
// input the Task state sent.
type ActivityTask struct {
	Token string
	Input string
}

func (b *backend) CreateActivity(ctx context.Context, name string) (string, error) {
	body, err := b.sfnCall(ctx, "CreateActivity", map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	var out struct {
		ARN string `json:"activityArn"`
	}
	json.Unmarshal(body, &out)
	return out.ARN, nil
}

func (b *backend) DescribeActivity(ctx context.Context, arn string) (Activity, error) {
	body, err := b.sfnCall(ctx, "DescribeActivity", map[string]any{"activityArn": arn})
	if err != nil {
		return Activity{}, err
	}
	var out struct {
		ARN          string  `json:"activityArn"`
		Name         string  `json:"name"`
		CreationDate float64 `json:"creationDate"`
	}
	json.Unmarshal(body, &out)
	return Activity{Name: out.Name, ARN: out.ARN, Created: epochToTime(out.CreationDate)}, nil
}

func (b *backend) DeleteActivity(ctx context.Context, arn string) error {
	_, err := b.sfnCall(ctx, "DeleteActivity", map[string]any{"activityArn": arn})
	return err
}

func (b *backend) ListActivities(ctx context.Context) ([]Activity, error) {
	body, err := b.sfnCall(ctx, "ListActivities", map[string]any{"maxResults": 1000})
	if err != nil {
		return nil, err
	}
	var out struct {
		Activities []struct {
			ARN          string  `json:"activityArn"`
			Name         string  `json:"name"`
			CreationDate float64 `json:"creationDate"`
		} `json:"activities"`
	}
	json.Unmarshal(body, &out)
	acts := make([]Activity, 0, len(out.Activities))
	for _, a := range out.Activities {
		acts = append(acts, Activity{Name: a.Name, ARN: a.ARN, Created: epochToTime(a.CreationDate)})
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].Name < acts[j].Name })
	return acts, nil
}

// activityPollBudget is how long the console waits on GetActivityTask. The
// service holds the connection for a minute, as AWS does, because a worker
// is a loop that wants to be woken; a page is not, so the request is cut
// short and an empty answer means "nothing queued right now".
const activityPollBudget = 2 * time.Second

// GetActivityTask claims the next queued task of an activity, or nil when
// none arrives within the budget. The deadline rides the request context,
// which the service honours: it answers the empty structure on
// cancellation rather than erroring, so a short poll is a clean poll.
func (b *backend) GetActivityTask(ctx context.Context, arn, worker string) (*ActivityTask, error) {
	ctx, cancel := context.WithTimeout(ctx, activityPollBudget)
	defer cancel()
	body, err := b.sfnCall(ctx, "GetActivityTask", map[string]any{"activityArn": arn, "workerName": worker})
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil // the budget ran out before the service answered
		}
		return nil, err
	}
	var out struct {
		Token string `json:"taskToken"`
		Input string `json:"input"`
	}
	json.Unmarshal(body, &out)
	if out.Token == "" {
		return nil, nil
	}
	return &ActivityTask{Token: out.Token, Input: prettyJSON(out.Input)}, nil
}

// activityARNOf rebuilds an activity ARN from its console path segment.
func activityARNOf(name string) string { return awsident.ARN("states", "activity:"+name) }
