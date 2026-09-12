package logs

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

var handlers = map[string]handler{
	"CreateLogGroup":              (*Server).createLogGroup,
	"DeleteLogGroup":              (*Server).deleteLogGroup,
	"DescribeLogGroups":           (*Server).describeLogGroups,
	"ListLogGroups":               (*Server).listLogGroups,
	"PutRetentionPolicy":          (*Server).putRetentionPolicy,
	"DeleteRetentionPolicy":       (*Server).deleteRetentionPolicy,
	"CreateLogStream":             (*Server).createLogStream,
	"DeleteLogStream":             (*Server).deleteLogStream,
	"DescribeLogStreams":          (*Server).describeLogStreams,
	"PutLogEvents":                (*Server).putLogEvents,
	"GetLogEvents":                (*Server).getLogEvents,
	"FilterLogEvents":             (*Server).filterLogEvents,
	"TagResource":                 (*Server).tagResource,
	"UntagResource":               (*Server).untagResource,
	"ListTagsForResource":         (*Server).listTagsForResource,
	"TagLogGroup":                 (*Server).tagLogGroup,
	"UntagLogGroup":               (*Server).untagLogGroup,
	"ListTagsLogGroup":            (*Server).listTagsLogGroup,
	"PutSubscriptionFilter":       (*Server).putSubscriptionFilter,
	"DeleteSubscriptionFilter":    (*Server).deleteSubscriptionFilter,
	"DescribeSubscriptionFilters": (*Server).describeSubscriptionFilters,
	"PutMetricFilter":             (*Server).putMetricFilter,
	"DeleteMetricFilter":          (*Server).deleteMetricFilter,
	"DescribeMetricFilters":       (*Server).describeMetricFilters,
	"TestMetricFilter":            (*Server).testMetricFilter,
}

// groupARN is the ARN CloudWatch Logs reports for a group.
func (s *Store) groupARN(name string) string {
	return s.id.ARN("logs", "log-group:"+name+":*")
}

// groupOf resolves logGroupName or logGroupIdentifier (a name or an ARN).
func groupOf(p map[string]any) string {
	if name := awsjson.Str(p, "logGroupName"); name != "" {
		return name
	}
	id := awsjson.Str(p, "logGroupIdentifier")
	if strings.HasPrefix(id, "arn:") {
		if i := strings.Index(id, ":log-group:"); i >= 0 {
			id = strings.TrimSuffix(id[i+len(":log-group:"):], ":*")
		}
	}
	return id
}

func (s *Server) mustGroup(p map[string]any) (*Group, *awshttp.APIError) {
	name := groupOf(p)
	if name == "" {
		return nil, errParam("logGroupName or logGroupIdentifier is required")
	}
	g, err := s.store.GetGroup(name)
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	if g == nil {
		return nil, errNotFound("The specified log group does not exist.")
	}
	return g, nil
}

// ---- groups ----

func (s *Server) createLogGroup(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	name := awsjson.Str(p, "logGroupName")
	if g, _ := s.store.GetGroup(name); g != nil {
		return nil, errExists("The specified log group already exists")
	}
	g := Group{Name: name, CreatedMs: s.store.now(), Tags: awsjson.StrMap(p, "tags")}
	if err := s.store.PutGroup(g); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func (s *Server) deleteLogGroup(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	if err := s.store.DeleteGroup(g.Name); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	// The group's filters went with it; the fan-out must not keep shipping
	// on their compiled copies once the name is reused.
	s.fan.forget(g.Name)
	return map[string]any{}, nil
}

func (s *Server) groupView(g Group) map[string]any {
	v := map[string]any{
		"logGroupName":      g.Name,
		"creationTime":      g.CreatedMs,
		"arn":               s.store.groupARN(g.Name),
		"logGroupArn":       strings.TrimSuffix(s.store.groupARN(g.Name), ":*"),
		"metricFilterCount": 0,
		"storedBytes":       0,
		"logGroupClass":     "STANDARD",
	}
	if g.RetentionDays > 0 {
		v["retentionInDays"] = g.RetentionDays
	}
	return v
}

func (s *Server) describeLogGroups(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	groups, err := s.store.ListGroups(awsjson.Str(p, "logGroupNamePrefix"))
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	if pat := awsjson.Str(p, "logGroupNamePattern"); pat != "" {
		var kept []Group
		for _, g := range groups {
			if strings.Contains(g.Name, pat) {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	if ids := awsjson.Strs(p, "logGroupIdentifiers"); len(ids) > 0 {
		want := map[string]bool{}
		for _, id := range ids {
			want[groupOf(map[string]any{"logGroupIdentifier": id})] = true
		}
		var kept []Group
		for _, g := range groups {
			if want[g.Name] {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	items := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		items = append(items, s.groupView(g))
	}
	return pageByName(items, "logGroups", "logGroupName", awsjson.Str(p, "nextToken"), awsjson.Int(p, "limit", 50)), nil
}

func (s *Server) listLogGroups(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	groups, err := s.store.ListGroups("")
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	pat := awsjson.Str(p, "logGroupNamePattern")
	items := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		if pat != "" && !matchesAnyPattern(g.Name, pat) {
			continue
		}
		items = append(items, map[string]any{
			"logGroupName": g.Name, "logGroupArn": strings.TrimSuffix(s.store.groupARN(g.Name), ":*"), "logGroupClass": "STANDARD",
		})
	}
	return pageByName(items, "logGroups", "logGroupName", awsjson.Str(p, "nextToken"), awsjson.Int(p, "limit", 50)), nil
}

// matchesAnyPattern is ListLogGroups' `a|^b` form: substrings, or prefixes
// with a leading caret, any of which may match.
func matchesAnyPattern(name, pat string) bool {
	for _, alt := range strings.Split(pat, "|") {
		if strings.HasPrefix(alt, "^") {
			if strings.HasPrefix(name, alt[1:]) {
				return true
			}
		} else if strings.Contains(name, alt) {
			return true
		}
	}
	return false
}

// pageByName pages a sorted list: the token is the last name on the page.
func pageByName(items []map[string]any, listKey, nameKey, token string, limit int) map[string]any {
	start := 0
	if token != "" {
		for i, it := range items {
			if it[nameKey].(string) > token {
				start = i
				break
			}
			start = i + 1
		}
	}
	if limit <= 0 {
		limit = 50
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	out := map[string]any{listKey: items[start:end]}
	if end < len(items) {
		out["nextToken"] = items[end-1][nameKey]
	}
	return out
}

// validRetention is the set AWS accepts for retentionInDays.
var validRetention = map[int]bool{1: true, 3: true, 5: true, 7: true, 14: true, 30: true, 60: true, 90: true, 120: true, 150: true, 180: true,
	365: true, 400: true, 545: true, 731: true, 1096: true, 1827: true, 2192: true, 2557: true, 2922: true, 3288: true, 3653: true}

func (s *Server) putRetentionPolicy(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	days := awsjson.Int(p, "retentionInDays", 0)
	if !validRetention[days] {
		return nil, errParam("Invalid retentionInDays value: %d. Valid values are 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, and 3653.", days)
	}
	g.RetentionDays = days
	if err := s.store.PutGroup(*g); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func (s *Server) deleteRetentionPolicy(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	g.RetentionDays = 0
	if err := s.store.PutGroup(*g); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

// ---- streams ----

func (s *Server) createLogStream(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	name := awsjson.Str(p, "logStreamName")
	if st, _ := s.store.GetStream(g.Name, name); st != nil {
		return nil, errExists("The specified log stream already exists")
	}
	if err := s.store.PutStream(Stream{Group: g.Name, Name: name, CreatedMs: s.store.now()}); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func (s *Server) deleteLogStream(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	name := awsjson.Str(p, "logStreamName")
	if st, _ := s.store.GetStream(g.Name, name); st == nil {
		return nil, errNotFound("The specified log stream does not exist.")
	}
	if err := s.store.DeleteStream(g.Name, name); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func (s *Server) streamView(g string, st Stream) map[string]any {
	v := map[string]any{
		"logStreamName": st.Name,
		"creationTime":  st.CreatedMs,
		"arn":           s.store.id.ARN("logs", "log-group:"+g+":log-stream:"+st.Name),
		"storedBytes":   0,
	}
	if st.FirstMs > 0 {
		v["firstEventTimestamp"] = st.FirstMs
		v["lastEventTimestamp"] = st.LastMs
		v["lastIngestionTime"] = st.LastIngestMs
	}
	return v
}

func (s *Server) describeLogStreams(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	prefix := awsjson.Str(p, "logStreamNamePrefix")
	byTime := awsjson.Str(p, "orderBy") == "LastEventTime"
	if byTime && prefix != "" {
		return nil, errParam("Cannot order by LastEventTime with a logStreamNamePrefix.")
	}
	streams, err := s.store.ListStreams(g.Name, prefix)
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	if byTime {
		sort.SliceStable(streams, func(i, j int) bool { return streams[i].LastMs < streams[j].LastMs })
	}
	if awsjson.Bool(p, "descending") {
		for i, j := 0, len(streams)-1; i < j; i, j = i+1, j-1 {
			streams[i], streams[j] = streams[j], streams[i]
		}
	}
	items := make([]map[string]any, 0, len(streams))
	for _, st := range streams {
		items = append(items, s.streamView(g.Name, st))
	}
	// The token is the page offset: orderBy and descending change the order,
	// so a name token would not survive them.
	start, _ := strconv.Atoi(awsjson.Str(p, "nextToken"))
	limit := awsjson.Int(p, "limit", 50)
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	out := map[string]any{"logStreams": items[start:end]}
	if end < len(items) {
		out["nextToken"] = strconv.Itoa(end)
	}
	return out, nil
}

// ---- events ----

func (s *Server) putLogEvents(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	stream := awsjson.Str(p, "logStreamName")
	raw, _ := p["logEvents"].([]any)
	if len(raw) == 0 {
		return nil, errParam("logEvents must not be empty")
	}
	events := make([]Event, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		events = append(events, Event{TS: awsjson.Int64(m, "timestamp", 0), Msg: awsjson.Str(m, "message"), RequestID: awsjson.Str(m, "requestId")})
	}
	if st, _ := s.store.GetStream(g.Name, stream); st == nil {
		// AWS requires CreateLogStream first; locally a first PutLogEvents
		// creates it, so a function's first line never bounces.
		_ = s.store.PutStream(Stream{Group: g.Name, Name: stream, CreatedMs: s.store.now()})
	}
	stored, err := s.store.PutEvents(g.Name, stream, events)
	if err != nil {
		if errors.Is(err, ErrNoGroup) {
			return nil, errNotFound("The specified log group does not exist.")
		}
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	s.fan.enqueue(ctx, g.Name, stream, stored)
	// Beside the fan-out rather than inside it: a subscription ships the line
	// onward and a metric filter turns it into a number, and a group may have
	// either, both, or neither.
	s.met.enqueue(ctx, g.Name, stored)
	return map[string]any{"nextSequenceToken": strconv.FormatInt(s.store.now(), 10)}, nil
}

func (s *Server) getLogEvents(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	stream := awsjson.Str(p, "logStreamName")
	if st, _ := s.store.GetStream(g.Name, stream); st == nil {
		return nil, errNotFound("The specified log stream does not exist.")
	}
	from, to := awsjson.Int64(p, "startTime", 0), awsjson.Int64(p, "endTime", 0)
	limit := awsjson.Int(p, "limit", 10000)
	// Tokens are f/<key> and b/<key>: the direction and the cursor. Without a
	// token the default reads the newest page (startFromHead false).
	head := awsjson.Bool(p, "startFromHead")
	token := awsjson.Str(p, "nextToken")
	var after []byte
	if token != "" {
		if dir, key, ok := strings.Cut(token, "/"); ok {
			head = dir == "f"
			after = []byte(key)
		}
	}
	page, more, err := s.store.Scan(g.Name, from, to, after, limit, !head, func(st string, _ Event) bool { return st == stream })
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	if !head {
		for i, j := 0, len(page)-1; i < j; i, j = i+1, j-1 {
			page[i], page[j] = page[j], page[i]
		}
	}
	items := make([]map[string]any, 0, len(page))
	for _, ev := range page {
		items = append(items, map[string]any{"timestamp": ev.TS, "message": ev.Msg, "ingestionTime": ev.Ingest})
	}
	// The forward token continues after the newest event on the page, the
	// backward one before the oldest. An empty page answers the token it was
	// given: SDK paginators stop when a token equals the one they sent, and
	// a fresh token for nothing would spin them.
	_ = more
	fwd, bwd := token, token
	switch {
	case len(page) > 0:
		fwd = "f/" + string(page[len(page)-1].Key())
		bwd = "b/" + string(page[0].Key())
	case token == "":
		fwd, bwd = "f/0", "b/0"
	}
	return map[string]any{"events": items, "nextForwardToken": fwd, "nextBackwardToken": bwd}, nil
}

func (s *Server) filterLogEvents(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	match, err := compile(awsjson.Str(p, "filterPattern"))
	if err != nil {
		return nil, errParam("%v", err)
	}
	names := awsjson.Strs(p, "logStreamNames")
	prefix := awsjson.Str(p, "logStreamNamePrefix")
	if len(names) > 0 && prefix != "" {
		return nil, errParam("logStreamNames and logStreamNamePrefix cannot both be set")
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	rid := awsjson.Str(p, "requestId") // doze extension: one invocation's lines
	var after []byte
	if t := awsjson.Str(p, "nextToken"); t != "" {
		after = []byte(t)
	}
	page, more, err := s.store.Scan(g.Name, awsjson.Int64(p, "startTime", 0), awsjson.Int64(p, "endTime", 0), after,
		awsjson.Int(p, "limit", 10000), false, func(st string, ev Event) bool {
			if len(want) > 0 && !want[st] {
				return false
			}
			if prefix != "" && !strings.HasPrefix(st, prefix) {
				return false
			}
			if rid != "" && ev.RequestID != rid {
				return false
			}
			return match(ev.Msg)
		})
	if err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	items := make([]map[string]any, 0, len(page))
	searched := map[string]bool{}
	for _, ev := range page {
		searched[ev.Stream] = true
		item := map[string]any{"logStreamName": ev.Stream, "timestamp": ev.TS, "message": ev.Msg, "ingestionTime": ev.Ingest, "eventId": ev.ID()}
		if ev.RequestID != "" {
			item["requestId"] = ev.RequestID
		}
		items = append(items, item)
	}
	streams := make([]map[string]any, 0, len(searched))
	for name := range searched {
		streams = append(streams, map[string]any{"logStreamName": name, "searchedCompletely": !more})
	}
	sort.Slice(streams, func(i, j int) bool {
		return streams[i]["logStreamName"].(string) < streams[j]["logStreamName"].(string)
	})
	out := map[string]any{"events": items, "searchedLogStreams": streams}
	if more {
		out["nextToken"] = string(page[len(page)-1].Key())
	}
	return out, nil
}

// ---- tags ----

func (s *Server) groupOfARN(arn string) (*Group, *awshttp.APIError) {
	i := strings.Index(arn, ":log-group:")
	if i < 0 {
		return nil, errParam("resourceArn must be a log group ARN")
	}
	return s.mustGroup(map[string]any{"logGroupName": strings.TrimSuffix(arn[i+len(":log-group:"):], ":*")})
}

func (s *Server) tagResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.groupOfARN(awsjson.Str(p, "resourceArn"))
	if aerr != nil {
		return nil, aerr
	}
	return s.addTags(g, awsjson.StrMap(p, "tags"))
}

func (s *Server) untagResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.groupOfARN(awsjson.Str(p, "resourceArn"))
	if aerr != nil {
		return nil, aerr
	}
	return s.removeTags(g, awsjson.Strs(p, "tagKeys"))
}

func (s *Server) listTagsForResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.groupOfARN(awsjson.Str(p, "resourceArn"))
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{"tags": orEmpty(g.Tags)}, nil
}

func (s *Server) tagLogGroup(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	return s.addTags(g, awsjson.StrMap(p, "tags"))
}

func (s *Server) untagLogGroup(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	return s.removeTags(g, awsjson.Strs(p, "tags"))
}

func (s *Server) listTagsLogGroup(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	g, aerr := s.mustGroup(p)
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{"tags": orEmpty(g.Tags)}, nil
}

func (s *Server) addTags(g *Group, tags map[string]string) (any, *awshttp.APIError) {
	if g.Tags == nil {
		g.Tags = map[string]string{}
	}
	for k, v := range tags {
		g.Tags[k] = v
	}
	if err := s.store.PutGroup(*g); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func (s *Server) removeTags(g *Group, keys []string) (any, *awshttp.APIError) {
	for _, k := range keys {
		delete(g.Tags, k)
	}
	if err := s.store.PutGroup(*g); err != nil {
		return nil, awshttp.Errf(500, "ServiceUnavailableException", "%v", err)
	}
	return map[string]any{}, nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
