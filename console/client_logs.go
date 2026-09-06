package console

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// ---- CloudWatch Logs ----
//
// What the console reads: a function's lines, newest at the bottom, each
// with the request that printed it, and the groups the logs service holds.

// LogLine is one event, shaped for a row.
type LogLine struct {
	TS        int64
	When      string // HH:MM:SS.mmm local
	Message   string
	Stream    string
	RequestID string
	Short     string // the first segment of the request id, the chip text
	Kind      string // start | end | report | err | line — the row's colour
}

// LogGroup is one group with what a list shows of it.
type LogGroup struct {
	Name          string
	Created       string
	RetentionDays int
	ARN           string
}

// LogStream is one stream with its span.
type LogStream struct {
	Name      string
	LastEvent string
	Created   string
}

// FilterLogs answers the newest `limit` events of a group in time order,
// optionally for one request id and matching a filter pattern. It reads the
// last hour by default, which is what a developer tailing a function wants;
// `since` (epoch ms) widens or narrows it.
func (b *backend) FilterLogs(ctx context.Context, group, rid, pattern string, since int64, limit int) ([]LogLine, error) {
	if since == 0 {
		since = time.Now().Add(-time.Hour).UnixMilli()
	}
	in := map[string]any{"logGroupName": group, "interleaved": true, "startTime": since, "limit": 10000}
	if rid != "" {
		in["requestId"] = rid
		in["startTime"] = 0
	}
	if pattern != "" {
		in["filterPattern"] = pattern
	}
	body, err := b.json11(ctx, "Logs_20140328", "FilterLogEvents", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Events []struct {
			TS      int64  `json:"timestamp"`
			Message string `json:"message"`
			Stream  string `json:"logStreamName"`
			RID     string `json:"requestId"`
		} `json:"events"`
	}
	json.Unmarshal(body, &out)
	if limit > 0 && len(out.Events) > limit {
		out.Events = out.Events[len(out.Events)-limit:]
	}
	lines := make([]LogLine, 0, len(out.Events))
	for _, ev := range out.Events {
		lines = append(lines, LogLine{
			TS: ev.TS, When: time.UnixMilli(ev.TS).Local().Format("15:04:05.000"),
			Message: ev.Message, Stream: ev.Stream, RequestID: ev.RID,
			Short: shortRID(ev.RID), Kind: lineKind(ev.Message),
		})
	}
	return lines, nil
}

func shortRID(rid string) string {
	if i := strings.IndexByte(rid, '-'); i > 0 {
		return rid[:i]
	}
	return rid
}

// lineKind classes a line for colour: the runtime's brackets, an error, or a
// plain line.
func lineKind(msg string) string {
	switch {
	case strings.HasPrefix(msg, "START RequestId:"):
		return "start"
	case strings.HasPrefix(msg, "END RequestId:"):
		return "end"
	case strings.HasPrefix(msg, "REPORT RequestId:"):
		return "report"
	case strings.Contains(msg, "[ERROR]") || strings.Contains(msg, "\tERROR\t") || strings.HasPrefix(msg, "Traceback") ||
		strings.Contains(msg, `"level":"error"`) || strings.Contains(msg, `"level":"ERROR"`):
		return "err"
	}
	return "line"
}

// ListLogGroups lists every group, sorted by name.
func (b *backend) ListLogGroups(ctx context.Context) ([]LogGroup, error) {
	var groups []LogGroup
	token := ""
	for {
		in := map[string]any{"limit": 50}
		if token != "" {
			in["nextToken"] = token
		}
		body, err := b.json11(ctx, "Logs_20140328", "DescribeLogGroups", in)
		if err != nil {
			return nil, err
		}
		var out struct {
			Groups []struct {
				Name      string `json:"logGroupName"`
				Created   int64  `json:"creationTime"`
				Retention int    `json:"retentionInDays"`
				ARN       string `json:"arn"`
			} `json:"logGroups"`
			Next string `json:"nextToken"`
		}
		json.Unmarshal(body, &out)
		for _, g := range out.Groups {
			groups = append(groups, LogGroup{Name: g.Name, Created: time.UnixMilli(g.Created).Local().Format("2006-01-02 15:04"),
				RetentionDays: g.Retention, ARN: g.ARN})
		}
		if out.Next == "" {
			return groups, nil
		}
		token = out.Next
	}
}

// ListLogStreams lists a group's streams, newest last event first.
func (b *backend) ListLogStreams(ctx context.Context, group string) ([]LogStream, error) {
	body, err := b.json11(ctx, "Logs_20140328", "DescribeLogStreams", map[string]any{
		"logGroupName": group, "orderBy": "LastEventTime", "descending": true, "limit": 50,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Streams []struct {
			Name    string `json:"logStreamName"`
			Last    int64  `json:"lastEventTimestamp"`
			Created int64  `json:"creationTime"`
		} `json:"logStreams"`
	}
	json.Unmarshal(body, &out)
	streams := make([]LogStream, 0, len(out.Streams))
	for _, st := range out.Streams {
		s := LogStream{Name: st.Name, Created: time.UnixMilli(st.Created).Local().Format("2006-01-02 15:04")}
		if st.Last > 0 {
			s.LastEvent = time.UnixMilli(st.Last).Local().Format("2006-01-02 15:04:05")
		}
		streams = append(streams, s)
	}
	return streams, nil
}

// DeleteLogGroup removes a group and everything in it.
func (b *backend) DeleteLogGroup(ctx context.Context, group string) error {
	_, err := b.json11(ctx, "Logs_20140328", "DeleteLogGroup", map[string]any{"logGroupName": group})
	return err
}

// SetLogRetention sets or clears a group's retention.
func (b *backend) SetLogRetention(ctx context.Context, group string, days int) error {
	if days <= 0 {
		_, err := b.json11(ctx, "Logs_20140328", "DeleteRetentionPolicy", map[string]any{"logGroupName": group})
		return err
	}
	_, err := b.json11(ctx, "Logs_20140328", "PutRetentionPolicy", map[string]any{"logGroupName": group, "retentionInDays": days})
	return err
}

// logsHash is the live region's change detector: the newest event's
// timestamp and the count, cheap enough to run every tick.
func logsHash(lines []LogLine) string {
	if len(lines) == 0 {
		return "0"
	}
	last := lines[len(lines)-1]
	return contentHash(strconv.FormatInt(last.TS, 10), strconv.Itoa(len(lines)), last.Message)
}

// CreateLogGroup makes a group, optionally with retention.
func (b *backend) CreateLogGroup(ctx context.Context, name string, days int) error {
	if _, err := b.json11(ctx, "Logs_20140328", "CreateLogGroup", map[string]any{"logGroupName": name}); err != nil {
		return err
	}
	if days > 0 {
		return b.SetLogRetention(ctx, name, days)
	}
	return nil
}

// DeleteLogStream removes one stream and its events.
func (b *backend) DeleteLogStream(ctx context.Context, group, stream string) error {
	_, err := b.json11(ctx, "Logs_20140328", "DeleteLogStream", map[string]any{"logGroupName": group, "logStreamName": stream})
	return err
}
