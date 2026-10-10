package awslocal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/doze-dev/doze-sdk/engine"
)

// The console of the local AWS stack, as data. The stack's own web console
// serves a one-call feed for exactly this (api/glance: the services in use and
// the recent API calls) and a resource index (api/resources); the dash used to
// make those calls itself, from a page that knew about AWS. They live here now,
// behind the module, and the dash only draws the description.

const consoleBase = "http://aws.doze.internal/_console"

// glanceFeed is the part of the glance response a console needs. The server
// adds fields freely, so only these are decoded.
type glanceFeed struct {
	Services []struct {
		Svc   string `json:"svc"`
		Label string `json:"label"`
		State string `json:"state"`
		Warn  bool   `json:"warn"`
		Calls int    `json:"calls"`
	} `json:"services"`
	Wire []struct {
		Seq    int64   `json:"seq"`
		T      string  `json:"t"`
		Svc    string  `json:"svc"`
		Action string  `json:"action"`
		Res    string  `json:"res"`
		Code   int     `json:"code"`
		Millis float64 `json:"ms"`
		Err    bool    `json:"err"`
	} `json:"wire"`
	Recorder bool `json:"recorder"`
}

type resourceEntry struct {
	S string `json:"s"` // service slug
	N string `json:"n"` // name
	U string `json:"u"` // console path
}

// serviceName is the display name of a service slug.
var serviceName = map[string]string{
	"s3": "s3", "ddb": "dynamodb", "sqs": "sqs", "sns": "sns", "eb": "eventbridge",
	"lambda": "lambda", "kms": "kms", "sm": "secretsmanager", "ssm": "ssm",
	"kinesis": "kinesis", "logs": "cloudwatch logs", "cw": "cloudwatch", "sfn": "step functions",
	"apigw": "api gateway", "apigw-http": "api gateway v2",
}

func name(slug string) string {
	if n, ok := serviceName[slug]; ok {
		return n
	}
	return slug
}

func getJSON(ctx context.Context, backend, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, consoleBase+path, nil)
	if err != nil {
		return err
	}
	resp, err := UnixHTTPClient(backend).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return json.Unmarshal(body, into)
}

// ConsoleResources lists what the console shows as tables and counters: one
// "service" per service in use, and one "resource" per thing in the stack.
func ConsoleResources(ctx context.Context, backend string) ([]engine.Resource, error) {
	var g glanceFeed
	if err := getJSON(ctx, backend, "/api/glance", &g); err != nil {
		return nil, err
	}
	var out []engine.Resource
	for _, s := range g.Services {
		info := map[string]string{"calls": strconv.Itoa(s.Calls), "console": "/_console/" + s.Svc}
		if s.State != "" {
			info["state"] = s.State
		}
		out = append(out, engine.Resource{Kind: "service", Name: name(s.Svc), Status: s.Label, Info: info})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	var rs []resourceEntry
	if err := getJSON(ctx, backend, "/api/resources", &rs); err == nil {
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].S != rs[j].S {
				return name(rs[i].S) < name(rs[j].S)
			}
			return rs[i].N < rs[j].N
		})
		for _, r := range rs {
			out = append(out, engine.Resource{
				Kind: "resource", Name: r.N, Status: name(r.S),
				Info: map[string]string{"service": name(r.S), "console": r.U},
			})
		}
	}
	return out, nil
}

// ConsoleSpec describes the console: services in use as counters, the
// resources as a table, and the wire as a stream of every API call.
func ConsoleSpec() engine.ConsoleSpec {
	return engine.ConsoleSpec{
		Front:  "console",
		WebURL: "http://{address}/_console",
		Panels: []engine.Panel{
			{ID: "services", Title: "Services in use", Kind: "counters", Source: "service", Columns: []engine.Column{
				{Key: "name", Title: "Service"}, {Key: "status", Title: "In use"},
			}},
			{ID: "resources", Title: "Resources", Kind: "table", Source: "resource", Columns: []engine.Column{
				{Key: "service", Title: "Service"}, {Key: "name", Title: "Name"},
			}},
			{ID: "wire", Title: "The wire", Kind: "stream", Columns: []engine.Column{
				{Key: "time", Title: "Time", Role: "time"},
				{Key: "service", Title: "Service"},
				{Key: "action", Title: "Call"},
				{Key: "resource", Title: "Resource"},
				{Key: "code", Title: "Status", Align: "right", Role: "code"},
				{Key: "ms", Title: "ms", Align: "right"},
			}},
		},
	}
}

// wireEvery is how often the stack's recent-calls ring is read.
const wireEvery = time.Second

// TailWire streams the stack's API calls as they happen: the first read shows
// the last few, then each call newer than the last one seen. It is a read of
// the stack's in-memory ring, which only exists while the recorder is on.
func TailWire(ctx context.Context, backend string, out chan<- engine.Row) error {
	var last int64 = -1
	t := time.NewTicker(wireEvery)
	defer t.Stop()
	for {
		var g glanceFeed
		if err := getJSON(ctx, backend, "/api/glance", &g); err == nil {
			first := last < 0
			for _, w := range g.Wire {
				if w.Seq <= last {
					continue
				}
				last = w.Seq
				tone := ""
				switch {
				case w.Err || w.Code >= 500:
					tone = "bad"
				case w.Code >= 400:
					tone = "warn"
				}
				row := engine.Row{Cells: map[string]string{
					"time": w.T, "service": name(w.Svc), "action": w.Action, "resource": w.Res,
					"code": strconv.Itoa(w.Code), "ms": strconv.FormatFloat(w.Millis, 'f', 0, 64),
				}, Tone: tone}
				select {
				case out <- row:
				case <-ctx.Done():
					return nil
				}
			}
			if first && last < 0 {
				last = 0
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
