package console

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- CloudWatch ----
//
// Two surfaces: a metrics browser with a server-rendered sparkline, and the
// alarms watching them. The list pane holds alarms rather than metrics,
// because an alarm is the thing a person acts on — a metric is something they
// look at on the way to writing one.

// cwWindows are the chart ranges the stat selector offers. An hour is the
// default because that is roughly how far back a local metric store goes
// before anyone has left the machine alone.
var cwWindows = []struct {
	Key   string
	Label string
	Dur   time.Duration
}{
	{"1h", "1 hour", time.Hour},
	{"3h", "3 hours", 3 * time.Hour},
	{"24h", "24 hours", 24 * time.Hour},
}

var cwStats = []string{"Sum", "Average", "Maximum", "Minimum", "SampleCount", "p95", "p99"}

func windowFor(key string) (string, time.Duration) {
	for _, w := range cwWindows {
		if w.Key == key {
			return w.Key, w.Dur
		}
	}
	return cwWindows[0].Key, cwWindows[0].Dur
}

// cwHome is the service page: the alarms, and the metric browser beside them.
func (c *Console) cwHome(w http.ResponseWriter, r *http.Request) {
	alarms, err := c.be.ListAlarms(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	metrics, _ := c.be.ListMetrics(r.Context(), "")
	c.render(w, r, "cw_home", map[string]any{
		"List": alarms, "Metrics": metrics, "Namespaces": Namespaces(metrics),
		"Title": "CloudWatch",
	})
}

// cwAlarm is one alarm's page: what it watches, its state, and its history.
func (c *Console) cwAlarm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	alarms, err := c.be.ListAlarms(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	var found *Alarm
	for i := range alarms {
		if alarms[i].Name == name {
			found = &alarms[i]
		}
	}
	if found == nil {
		c.notFound(w, r)
		return
	}
	data := map[string]any{
		"List": alarms, "Sel": name, "A": found,
		"Tab": tabOf(r, "state"), "Title": name + " · CloudWatch",
	}
	switch data["Tab"] {
	case "history":
		data["History"], _ = c.be.AlarmHistory(r.Context(), name)
	case "metric":
		// The chart of the metric this alarm watches, so "why did it fire"
		// is one click from the alarm rather than a hunt through the browser.
		window, dur := windowFor(r.URL.Query().Get("window"))
		stat := found.Statistic
		data["Series"], _ = c.be.MetricSeries(r.Context(), parseMetricKey(found.MetricKey), stat, dur)
		data["Window"], data["Windows"] = window, cwWindows
	case "tags":
		// The shared tags panel reads through client_tags.go.
	}
	c.render(w, r, "cw_alarm", data)
}

// cwMetric is one metric's chart: GET /cw/metric?key=&stat=&window=
func (c *Console) cwMetric(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m := parseMetricKey(q.Get("key"))
	if m.Namespace == "" || m.Name == "" {
		c.notFound(w, r)
		return
	}
	stat := q.Get("stat")
	if stat == "" {
		stat = "Sum"
	}
	window, dur := windowFor(q.Get("window"))
	series, err := c.be.MetricSeries(r.Context(), m, stat, dur)
	if err != nil {
		c.fail(w, err)
		return
	}
	alarms, _ := c.be.AlarmsForMetric(r.Context(), m)
	allAlarms, _ := c.be.ListAlarms(r.Context())
	metrics, _ := c.be.ListMetrics(r.Context(), "")
	c.render(w, r, "cw_metric", map[string]any{
		"List": allAlarms, "Metrics": metrics, "Namespaces": Namespaces(metrics),
		"M": m, "Series": series, "Stat": stat, "Stats": cwStats,
		"Window": window, "Windows": cwWindows, "Alarms": alarms,
		"Title": m.Namespace + "/" + m.Name + " · CloudWatch",
	})
}

// cwCreateAlarm puts an alarm from the create form: POST /cw/create-alarm
func (c *Console) cwCreateAlarm(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		c.fail(w, fmt.Errorf("an alarm needs a name"))
		return
	}
	// The metric comes back as the same key the browser links with, so the
	// form cannot disagree with the chart about which series is meant.
	m := parseMetricKey(r.FormValue("metric"))
	if m.Namespace == "" || m.Name == "" {
		c.fail(w, fmt.Errorf("pick a metric for the alarm to watch"))
		return
	}
	threshold, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("threshold")), 64)
	if err != nil {
		c.fail(w, fmt.Errorf("the threshold must be a number"))
		return
	}
	a := Alarm{
		Name: name, Description: strings.TrimSpace(r.FormValue("description")),
		Namespace: m.Namespace, MetricName: m.Name, Dimensions: m.Dimensions,
		Statistic: r.FormValue("statistic"), Operator: r.FormValue("operator"),
		Threshold: threshold, Missing: r.FormValue("missing"),
		Period:     atoiOr(r.FormValue("period"), 60),
		Evaluation: atoiOr(r.FormValue("evaluation"), 1),
	}
	if topic := r.FormValue("topic"); topic != "" {
		a.Actions = []string{topic}
	}
	if err := c.be.PutAlarm(r.Context(), a); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/cw/alarm/"+name, "Alarm “"+name+"” created")
}

// cwSetState flips an alarm by hand: POST /cw/alarm/{name}/state
//
// This is the button that makes alarms testable locally — it fires the
// alarm's actions without waiting for a metric to breach, which is how a
// developer checks that the topic behind the alarm is wired up.
func (c *Console) cwSetState(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	state := r.FormValue("state")
	if state != "OK" && state != "ALARM" && state != "INSUFFICIENT_DATA" {
		c.fail(w, fmt.Errorf("state must be OK, ALARM or INSUFFICIENT_DATA"))
		return
	}
	if err := c.be.SetAlarmState(r.Context(), name, state, r.FormValue("reason")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/cw/alarm/"+name, "Alarm “"+name+"” set to "+state)
}

// cwSetActions enables or disables an alarm's actions: POST /cw/alarm/{name}/actions
func (c *Console) cwSetActions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	enabled := r.FormValue("enabled") == "true"
	if err := c.be.SetAlarmActions(r.Context(), name, enabled); err != nil {
		c.fail(w, err)
		return
	}
	word := "disabled"
	if enabled {
		word = "enabled"
	}
	c.redirect(w, r, c.prefix+"/cw/alarm/"+name, "Actions "+word+" for “"+name+"”")
}

// cwDeleteAlarm removes one: POST /cw/alarm/{name}/delete
func (c *Console) cwDeleteAlarm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := c.be.DeleteAlarm(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/cw", "Alarm “"+name+"” deleted")
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
