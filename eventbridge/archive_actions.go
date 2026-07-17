package eventbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/eventpattern"
)

// pnum reads a numeric field (SDKs send epoch times and counts as JSON numbers).
func pnum(p map[string]any, key string) (int64, bool) {
	switch v := p[key].(type) {
	case float64:
		return int64(v), true
	case json.Number:
		n, _ := v.Int64()
		return n, true
	}
	return 0, false
}

func archiveFromArn(arn string) string {
	if i := strings.Index(arn, ":archive/"); i >= 0 {
		return arn[i+len(":archive/"):]
	}
	return arn
}

// captureToArchives appends an event to every enabled archive over its bus
// whose pattern (if any) matches. Called for real PutEvents, not replays.
func (s *Server) captureToArchives(bus string, eventJSON []byte) {
	arcs, err := s.store.ListArchives("", busARN(bus))
	if err != nil || len(arcs) == 0 {
		return
	}
	now := s.now().Unix()
	for _, a := range arcs {
		if a.State != "ENABLED" {
			continue
		}
		if a.Pattern != "" {
			pat, perr := eventpattern.Parse([]byte(a.Pattern))
			if perr != nil {
				continue
			}
			if ok, _ := pat.Match(eventJSON); !ok {
				continue
			}
		}
		if err := s.store.AppendArchiveEvent(a.Name, now, eventJSON); err != nil {
			s.logf("eventbridge: archive %s append: %v", a.Name, err)
		}
	}
}

// ---- archive handlers ----

func (s *Server) createArchive(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "ArchiveName")
	if name == "" {
		return nil, awshttp.Errf(400, "ValidationException", "ArchiveName is required")
	}
	src := awsjson.Str(p, "EventSourceArn")
	if src == "" {
		return nil, awshttp.Errf(400, "ValidationException", "EventSourceArn is required")
	}
	ret := 0
	if n, ok := pnum(p, "RetentionDays"); ok {
		ret = int(n)
	}
	a := Archive{
		Name: name, EventSourceArn: src, Pattern: awsjson.Str(p, "EventPattern"),
		RetentionDays: ret, Desc: awsjson.Str(p, "Description"),
		State: "ENABLED", CreationTime: s.now().Unix(),
	}
	if err := s.store.CreateArchive(a); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ArchiveArn": a.ARN(), "State": a.State, "CreationTime": float64(a.CreationTime),
	}, nil
}

func archiveView(a *Archive) map[string]any {
	out := map[string]any{
		"ArchiveName":    a.Name,
		"ArchiveArn":     a.ARN(),
		"EventSourceArn": a.EventSourceArn,
		"State":          a.State,
		"RetentionDays":  float64(a.RetentionDays),
		"SizeBytes":      float64(a.SizeBytes),
		"EventCount":     float64(a.EventCount),
		"CreationTime":   float64(a.CreationTime),
	}
	if a.Pattern != "" {
		out["EventPattern"] = a.Pattern
	}
	if a.Desc != "" {
		out["Description"] = a.Desc
	}
	return out
}

func (s *Server) describeArchive(p map[string]any) (any, *awshttp.APIError) {
	a, err := s.store.GetArchive(awsjson.Str(p, "ArchiveName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return archiveView(a), nil
}

func (s *Server) listArchives(p map[string]any) (any, *awshttp.APIError) {
	arcs, err := s.store.ListArchives(awsjson.Str(p, "NamePrefix"), awsjson.Str(p, "EventSourceArn"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	list := make([]map[string]any, 0, len(arcs))
	for i := range arcs {
		list = append(list, archiveView(&arcs[i]))
	}
	return map[string]any{"Archives": list}, nil
}

func (s *Server) updateArchive(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "ArchiveName")
	err := s.store.UpdateArchive(name, func(a *Archive) error {
		if v := awsjson.Str(p, "EventPattern"); v != "" {
			if _, e := eventpattern.Parse([]byte(v)); e != nil {
				return awshttp.Errf(400, "InvalidEventPatternException", "%v", e)
			}
			a.Pattern = v
		}
		if v := awsjson.Str(p, "Description"); v != "" {
			a.Desc = v
		}
		if n, ok := pnum(p, "RetentionDays"); ok {
			a.RetentionDays = int(n)
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	a, _ := s.store.GetArchive(name)
	return map[string]any{
		"ArchiveArn": a.ARN(), "State": a.State, "CreationTime": float64(a.CreationTime),
	}, nil
}

func (s *Server) deleteArchive(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "ArchiveName")
	if _, err := s.store.GetArchive(name); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if err := s.store.DeleteArchive(name); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{}, nil
}

// ---- replay handlers ----

func (s *Server) startReplay(p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "ReplayName")
	if name == "" {
		return nil, awshttp.Errf(400, "ValidationException", "ReplayName is required")
	}
	archiveArn := awsjson.Str(p, "EventSourceArn")
	archiveName := archiveFromArn(archiveArn)
	if _, err := s.store.GetArchive(archiveName); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	dest, _ := p["Destination"].(map[string]any)
	if dest == nil {
		return nil, awshttp.Errf(400, "ValidationException", "Destination is required")
	}
	busArn := awsjson.Str(dest, "Arn")
	bus := busFromArn(busArn)
	var filter map[string]bool
	if fa, ok := dest["FilterArns"].([]any); ok && len(fa) > 0 {
		filter = map[string]bool{}
		for _, x := range fa {
			if str, ok := x.(string); ok {
				filter[str] = true
			}
		}
	}
	start, _ := pnum(p, "EventStartTime")
	end, _ := pnum(p, "EventEndTime")
	now := s.now().Unix()

	// Local replays run synchronously: re-inject each windowed event through the
	// destination bus's rules (optionally restricted to the filter rule ARNs).
	count, last, err := s.store.ReplayEvents(archiveName, start, end, func(ev []byte) {
		s.matchAndDispatch(bus, ev, filter)
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}

	r := Replay{
		Name: name, EventSourceArn: archiveArn, DestinationArn: busArn,
		EventStartTime: start, EventEndTime: end,
		State: "COMPLETED", StateReason: fmt.Sprintf("Replayed %d event(s)", count),
		StartTime: now, EndTime: now, LastEventTime: last,
	}
	for k := range filter {
		r.FilterArns = append(r.FilterArns, k)
	}
	if err := s.store.PutReplay(r); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{
		"ReplayArn": r.ARN(), "State": r.State,
		"StateReason": r.StateReason, "ReplayStartTime": float64(now),
	}, nil
}

func replayView(r *Replay) map[string]any {
	out := map[string]any{
		"ReplayName":      r.Name,
		"ReplayArn":       r.ARN(),
		"EventSourceArn":  r.EventSourceArn,
		"State":           r.State,
		"EventStartTime":  float64(r.EventStartTime),
		"EventEndTime":    float64(r.EventEndTime),
		"ReplayStartTime": float64(r.StartTime),
		"ReplayEndTime":   float64(r.EndTime),
		"Destination":     map[string]any{"Arn": r.DestinationArn, "FilterArns": r.FilterArns},
	}
	if r.LastEventTime != 0 {
		out["EventLastReplayedTime"] = float64(r.LastEventTime)
	}
	if r.StateReason != "" {
		out["StateReason"] = r.StateReason
	}
	return out
}

func (s *Server) describeReplay(p map[string]any) (any, *awshttp.APIError) {
	r, err := s.store.GetReplay(awsjson.Str(p, "ReplayName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return replayView(r), nil
}

func (s *Server) listReplays(p map[string]any) (any, *awshttp.APIError) {
	reps, err := s.store.ListReplays(awsjson.Str(p, "NamePrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	list := make([]map[string]any, 0, len(reps))
	for i := range reps {
		list = append(list, replayView(&reps[i]))
	}
	return map[string]any{"Replays": list}, nil
}

func (s *Server) cancelReplay(p map[string]any) (any, *awshttp.APIError) {
	r, err := s.store.GetReplay(awsjson.Str(p, "ReplayName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// Local replays complete synchronously, so there is never a running replay
	// to cancel — answer honestly.
	return nil, awshttp.Errf(400, "IllegalStatusException",
		"replay %s is already %s and cannot be cancelled", r.Name, r.State)
}
