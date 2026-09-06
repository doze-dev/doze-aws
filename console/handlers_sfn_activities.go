package console

import (
	"net/http"
	"strings"
)

// ---- Step Functions: activities ----
//
// An activity is a queue: a Task state whose Resource is the activity ARN
// parks with a token, and a worker collects {token, input} with
// GetActivityTask and answers through SendTaskSuccess/Failure. Activities
// belong to no machine, so they get a page of their own — reached from the
// bottom of the list pane rather than a rail entry, because they are a
// facet of Step Functions and not a fourteenth service.
//
// The console can be the worker. "Take a task" is GetActivityTask with a
// short budget (client_sfn_ops.go says why), and what comes back is the
// token already filled into the same form the history panel offers.

// sfnActivityCount is the number the list pane's pseudo-row shows.
func (c *Console) sfnActivityCount(r *http.Request) int {
	acts, _ := c.be.ListActivities(r.Context())
	return len(acts)
}

func (c *Console) sfnActivities(w http.ResponseWriter, r *http.Request) {
	acts, err := c.be.ListActivities(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	sms, _ := c.be.ListStateMachines(r.Context())
	c.render(w, r, "sfn_activities", map[string]any{
		"Activities": acts, "ActCount": len(acts), "List": sms,
		"Title": "Activities · Step Functions",
	})
}

func (c *Console) sfnActivityCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if _, err := c.be.CreateActivity(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/activities", "Activity “"+name+"” created — a Task whose Resource is its ARN queues work here")
}

func (c *Console) sfnActivityDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("activity")
	if err := c.be.DeleteActivity(r.Context(), activityARNOf(name)); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/activities", "Activity “"+name+"” deleted")
}

// sfnActivityTake claims the next queued task, or says nothing is waiting.
// DescribeActivity first: an activity that is not there is that error, not
// an empty poll, and the two read very differently.
func (c *Console) sfnActivityTake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("activity")
	act, err := c.be.DescribeActivity(r.Context(), activityARNOf(name))
	if err != nil {
		c.fail(w, err)
		return
	}
	task, err := c.be.GetActivityTask(r.Context(), act.ARN, "console")
	if err != nil {
		c.fail(w, err)
		return
	}
	if task != nil {
		toast(w, "Took a task from “"+name+"” — the execution now waits on you")
	}
	c.partial(w, "sfn_activity_task", map[string]any{"Activity": act, "Task": task})
}

// sfnActivityTaskResult redeems the token a taken task carried. Same call
// as the execution page's form; this one renders a receipt rather than a
// history, because there is no one execution page to re-render — the task
// came from a queue, not from a page.
func (c *Console) sfnActivityTaskResult(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	success := r.FormValue("outcome") != "failure"
	if err := c.be.SendTaskResult(r.Context(), token, success, r.FormValue("output"), r.FormValue("error"), r.FormValue("cause")); err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_activity_done", map[string]any{"Success": success, "Activity": r.FormValue("activity")})
}
