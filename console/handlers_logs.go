package console

import (
	"net/http"
	"strconv"
	"strings"
)

// ---- CloudWatch Logs ----
//
// Two surfaces read the same partial: the Logs tab on a function's page,
// and the logs service's own group page. Both are live regions on the
// history pane's cadence, hashed on the newest event so a quiet function
// costs a 204 a tick.

// lambdaLogs is the Lambda page's Logs tab partial:
// GET /lambda/{fn}/logs?rid=&q=&h=
func (c *Console) lambdaLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("fn")
	data := c.logTailData(r, "/aws/lambda/"+name, c.prefix+"/lambda/"+name+"/logs")
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "log_tail", data)
}

// logTailData is the shared model: the newest lines of a group, filtered.
func (c *Console) logTailData(r *http.Request, group, liveURL string) map[string]any {
	q := r.URL.Query()
	rid, pattern := q.Get("rid"), q.Get("q")
	lines, err := c.be.FilterLogs(r.Context(), group, rid, pattern, 0, 500)
	missing := err != nil // the group does not exist yet: nothing printed
	if missing {
		lines = nil
	}
	url := liveURL
	sep := "?"
	for _, kv := range [][2]string{{"rid", rid}, {"q", pattern}} {
		if kv[1] != "" {
			url += sep + kv[0] + "=" + urlQuery(kv[1])
			sep = "&"
		}
	}
	return map[string]any{
		"Group": group, "Lines": lines, "RID": rid, "Query": pattern, "Missing": missing,
		"LiveURL": url, "BaseURL": liveURL, "Hash": logsHash(lines),
	}
}

// logsHome is the service page: every group.
func (c *Console) logsHome(w http.ResponseWriter, r *http.Request) {
	groups, err := c.be.ListLogGroups(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "logs_home", map[string]any{"List": groups, "Title": "CloudWatch Logs"})
}

// logsGroup is one group's page: its streams and a tail.
// GET /logs/group?name=
func (c *Console) logsGroup(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	groups, err := c.be.ListLogGroups(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	var g *LogGroup
	for i := range groups {
		if groups[i].Name == name {
			g = &groups[i]
		}
	}
	if g == nil {
		c.notFound(w, r)
		return
	}
	streams, _ := c.be.ListLogStreams(r.Context(), name)
	tail := c.logTailData(r, name, c.prefix+"/logs/tail?name="+urlQuery(name))
	data := map[string]any{
		"List": groups, "Sel": name, "G": g, "Streams": streams, "Tail": tail,
		"Tab": tabOf(r, "tail"), "Title": name + " · CloudWatch Logs",
	}
	if data["Tab"] == "subscriptions" {
		data["Subs"], _ = c.be.ListLogSubscriptions(r.Context(), name)
		data["Functions"], _ = c.be.ListFunctions(r.Context())
		data["KinesisStreams"], _ = c.be.ListStreams(r.Context())
	}
	c.render(w, r, "logs_group", data)
}

// logsSubscribe puts a subscription filter on a group: POST /logs/subscribe
func (c *Console) logsSubscribe(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	filter := strings.TrimSpace(r.FormValue("filter"))
	if filter == "" {
		filter = "console"
	}
	if err := c.be.PutLogSubscription(r.Context(), name, filter, strings.TrimSpace(r.FormValue("pattern")), r.FormValue("destination")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/logs/group?name="+urlQuery(name)+"&tab=subscriptions", "Subscription “"+filter+"” added to "+name)
}

// logsUnsubscribe removes one filter: POST /logs/unsubscribe
func (c *Console) logsUnsubscribe(w http.ResponseWriter, r *http.Request) {
	name, filter := r.FormValue("name"), r.FormValue("filter")
	if err := c.be.DeleteLogSubscription(r.Context(), name, filter); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/logs/group?name="+urlQuery(name)+"&tab=subscriptions", "Subscription “"+filter+"” removed")
}

// logsTail is the group page's polled partial: GET /logs/tail?name=&rid=&q=&h=
func (c *Console) logsTail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	data := c.logTailData(r, name, c.prefix+"/logs/tail?name="+urlQuery(name))
	if liveUnchanged(w, r, data["Hash"].(string)) {
		return
	}
	c.partial(w, "log_tail", data)
}

// logsRetention sets or clears a group's retention: POST /logs/retention
func (c *Console) logsRetention(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	days, _ := strconv.Atoi(r.FormValue("days"))
	if err := c.be.SetLogRetention(r.Context(), name, days); err != nil {
		c.fail(w, err)
		return
	}
	msg := "Retention cleared for " + name
	if days > 0 {
		msg = "Retention set to " + strconv.Itoa(days) + " days for " + name
	}
	c.redirect(w, r, c.prefix+"/logs/group?name="+urlQuery(name)+"&tab=settings", msg)
}

// logsDelete removes a group: POST /logs/delete
func (c *Console) logsDelete(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.DeleteLogGroup(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/logs", "Log group “"+name+"” deleted")
}

// logsCreate makes a group: POST /logs/create
func (c *Console) logsCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	days, _ := strconv.Atoi(r.FormValue("days"))
	if err := c.be.CreateLogGroup(r.Context(), name, days); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/logs/group?name="+urlQuery(name), "Log group “"+name+"” created")
}

// logsDeleteStream removes one stream: POST /logs/delete-stream
func (c *Console) logsDeleteStream(w http.ResponseWriter, r *http.Request) {
	name, stream := r.FormValue("name"), r.FormValue("stream")
	// A stream already gone is the outcome asked for, not a failure.
	if err := c.be.DeleteLogStream(r.Context(), name, stream); err != nil && !strings.Contains(err.Error(), "ResourceNotFoundException") {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/logs/group?name="+urlQuery(name)+"&tab=streams", "Stream deleted")
}
