package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (c *Console) ebBuses(w http.ResponseWriter, r *http.Request) {
	buses, err := c.be.ListBuses(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(buses) > 0 {
		r.SetPathValue("bus", buses[0].Name)
		c.ebBus(w, r)
		return
	}
	c.render(w, r, "eb_home", map[string]any{"List": buses, "Title": "EventBridge"})
}

func (c *Console) ebCreateBus(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateBus(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/eb/"+name, "Event bus “"+name+"” created")
}

func (c *Console) ebDeleteBus(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteBus(r.Context(), r.PathValue("bus")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/eb", "Event bus deleted")
}

func (c *Console) ebBus(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	rules, err := c.be.ListRules(r.Context(), bus)
	if err != nil {
		c.fail(w, err)
		return
	}
	buses, _ := c.be.ListBuses(r.Context())
	arcs, _ := c.be.ListArchives(r.Context(), busARN(bus))
	reps, _ := c.be.ListReplays(r.Context())
	c.render(w, r, "eb_bus", map[string]any{
		"Bus": bus, "Rules": rules, "BusARN": busARN(bus), "List": buses,
		"Archives": arcs, "Replays": reps, "Title": bus + " · EventBridge",
	})
}

// ebArchivesPartial re-renders the archives+replays panel after a mutation.
func (c *Console) ebArchivesPartial(w http.ResponseWriter, r *http.Request, bus string) {
	arcs, _ := c.be.ListArchives(r.Context(), busARN(bus))
	reps, _ := c.be.ListReplays(r.Context())
	c.partial(w, "eb_archives", map[string]any{"Bus": bus, "BusARN": busARN(bus), "Archives": arcs, "Replays": reps})
}

func (c *Console) ebCreateArchive(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	if err := c.be.CreateArchive(r.Context(), r.FormValue("name"), busARN(bus), r.FormValue("pattern")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Archive created")
	c.ebArchivesPartial(w, r, bus)
}

func (c *Console) ebDeleteArchive(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	if err := c.be.DeleteArchive(r.Context(), r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Archive deleted")
	c.ebArchivesPartial(w, r, bus)
}

func (c *Console) ebReplay(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	arc := r.FormValue("name")
	// Replay the whole archive: a window from the epoch to now covers every
	// stored event. The replay name must be unique per run.
	name := arc + "-replay-" + shortID(arc+time.Now().String())
	start := int64(946684800) // 2000-01-01, before any local event
	end := time.Now().Add(time.Hour).Unix()
	if err := c.be.StartReplay(r.Context(), name, archiveARN(arc), busARN(bus), start, end); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Replaying “"+arc+"” → "+bus)
	c.ebArchivesPartial(w, r, bus)
}

func (c *Console) ebRulesPartial(w http.ResponseWriter, r *http.Request, bus string) {
	rules, _ := c.be.ListRules(r.Context(), bus)
	c.partial(w, "eb_rule_list", map[string]any{"Bus": bus, "Rules": rules})
}

func (c *Console) ebCreateRule(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	name := r.FormValue("name")
	if err := c.be.PutRule(r.Context(), bus, name, r.FormValue("pattern"), r.FormValue("schedule")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/eb/"+bus+"/rule/"+name, "Rule “"+name+"” created")
}

func (c *Console) ebDeleteRule(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	if err := c.be.DeleteRule(r.Context(), bus, r.PathValue("rule")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Rule deleted")
	w.Header().Set("HX-Redirect", c.prefix+"/eb/"+bus)
}

func (c *Console) ebTestEvent(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	detail := r.FormValue("detail")
	if detail == "" {
		detail = "{}"
	}
	if err := c.be.PutTestEvent(r.Context(), bus, r.FormValue("source"), r.FormValue("detail_type"), detail); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Event published to "+bus)
	c.ebRulesPartial(w, r, bus)
}

// ebMatch answers "which rules on this bus catch this event?" live, using the
// service's own TestEventPattern evaluator — the loop the SDK can't close
// without a hand-rolled script.
func (c *Console) ebMatch(w http.ResponseWriter, r *http.Request) {
	bus := r.PathValue("bus")
	rules, _ := c.be.ListRules(r.Context(), bus)
	detail := strings.TrimSpace(r.FormValue("detail"))
	if detail == "" {
		detail = "{}"
	}
	data := map[string]any{"Bus": bus, "Rules": rules, "Matching": true}
	if !json.Valid([]byte(detail)) {
		data["MatchNote"] = "detail isn't valid JSON yet"
		c.partial(w, "eb_rule_list", data)
		return
	}
	event, _ := json.Marshal(map[string]any{
		"id": "console-test", "account": "000000000000", "region": "us-east-1",
		"time": time.Now().UTC().Format(time.RFC3339), "resources": []string{},
		"source": r.FormValue("source"), "detail-type": r.FormValue("detail_type"),
		"detail": json.RawMessage(detail),
	})
	verdicts := map[string]string{}
	hits := 0
	for _, rl := range rules {
		if rl.Pattern == "" { // schedule rules don't pattern-match
			verdicts[rl.Name] = "schedule"
			continue
		}
		ok, err := c.be.TestEventPattern(r.Context(), rl.Pattern, string(event))
		switch {
		case err != nil:
			verdicts[rl.Name] = "error"
		case ok:
			verdicts[rl.Name] = "match"
			hits++
		default:
			verdicts[rl.Name] = "no match"
		}
	}
	data["Verdicts"] = verdicts
	data["Hits"] = hits
	c.partial(w, "eb_rule_list", data)
}

// ebToggleRule flips a rule between ENABLED and DISABLED.
func (c *Console) ebToggleRule(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	rule, err := c.be.GetRule(r.Context(), bus, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	enable := rule.State != "ENABLED"
	if err := c.be.SetRuleState(r.Context(), bus, name, enable); err != nil {
		c.fail(w, err)
		return
	}
	if enable {
		toast(w, "Rule enabled — matching events deliver again")
	} else {
		toast(w, "Rule disabled — events pass it by")
	}
	c.redirect(w, r, c.prefix+"/eb/"+bus+"/rule/"+name, "")
}

func (c *Console) ebRule(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	rule, err := c.be.GetRule(r.Context(), bus, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	dests, _ := c.be.ListDestinations(r.Context())
	buses, _ := c.be.ListBuses(r.Context())
	c.render(w, r, "eb_rule", map[string]any{
		"Bus": bus, "Rule": rule, "Queues": queues, "Functions": fns, "Destinations": dests, "List": buses, "Title": name + " · EventBridge",
		// bus/rule, not rule. The graph keys an EventBridge rule by both —
		// client_flow.go builds the node as bus.Name+"/"+rl.Name, because two
		// buses may each hold a rule called "orders" — so looking it up by the
		// bare rule name never matched. Every rule page reported "nothing feeds
		// this yet" and "no targets yet" however many targets the rule had.
		"Conn": c.be.Neighbors(r.Context(), "eb", bus+"/"+name),
	})
}

func (c *Console) ebTargetsPartial(w http.ResponseWriter, r *http.Request, bus, name string) {
	rule, err := c.be.GetRule(r.Context(), bus, name)
	if err != nil {
		c.fail(w, err)
		return
	}
	queues, _ := c.be.ListQueues(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	dests, _ := c.be.ListDestinations(r.Context())
	c.partial(w, "eb_targets", map[string]any{
		"Bus": bus, "Rule": rule, "Queues": queues, "Functions": fns, "Destinations": dests,
	})
}

func (c *Console) ebAddTarget(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	arn := r.FormValue("arn")
	id := "t" + shortID(arn)
	if err := c.be.AddTarget(r.Context(), bus, name, id, arn); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Target added")
	c.ebTargetsPartial(w, r, bus, name)
}

func (c *Console) ebRemoveTarget(w http.ResponseWriter, r *http.Request) {
	bus, name := r.PathValue("bus"), r.PathValue("rule")
	if err := c.be.RemoveTarget(r.Context(), bus, name, r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Target removed")
	c.ebTargetsPartial(w, r, bus, name)
}

func busARN(name string) string {
	return "arn:aws:events:us-east-1:000000000000:event-bus/" + name
}

// shortID derives a small stable id from a string (for target ids).
func shortID(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 6)
	for i := range out {
		out[i] = hex[h&0xf]
		h >>= 4
	}
	return string(out)
}

// ebArchiveDetail is DescribeArchive plus the editor for what it shows.
//
// The list says an archive has zero events; the pattern says why, and only
// DescribeArchive carries it. Updating that pattern matters because the
// alternative is deleting the archive, which discards everything already
// captured.
func (c *Console) ebArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("archive")
	arc, reason, err := c.be.DescribeArchive(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "eb_archive_detail", map[string]any{
		"Prefix": c.prefix, "Bus": r.PathValue("bus"), "A": arc, "Reason": reason,
	})
}

func (c *Console) ebUpdateArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("archive")
	if err := c.be.UpdateArchive(r.Context(), name,
		strings.TrimSpace(r.FormValue("pattern")), atoiDefault(r.FormValue("retention"), 0)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Archive updated")
	c.ebArchive(w, r)
}

// ebReplayDetail explains a replay that reports COMPLETED having delivered
// nothing — the window and the state reason, neither of which the list carries.
func (c *Console) ebReplayDetail(w http.ResponseWriter, r *http.Request) {
	fields, err := c.be.DescribeReplay(r.Context(), r.PathValue("replay"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "eb_kv_detail", map[string]any{"Title": "Replay " + r.PathValue("replay"), "Fields": fields})
}

// ebBusDetail carries the bus's resource policy, which nothing else shows.
func (c *Console) ebBusDetail(w http.ResponseWriter, r *http.Request) {
	fields, err := c.be.DescribeEventBus(r.Context(), r.PathValue("bus"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "eb_kv_detail", map[string]any{"Title": "Bus " + r.PathValue("bus"), "Fields": fields})
}

// ebRulesByTarget is the reverse lookup — which rules fire into this ARN. The
// forward direction is a rule's target list; this is the question you ask while
// standing on a queue that is receiving something you did not expect.
func (c *Console) ebRulesByTarget(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.FormValue("target"))
	if target == "" {
		c.fail(w, errors.New("Paste the ARN of the queue, function or stream you want traced back."))
		return
	}
	names, err := c.be.RuleNamesByTarget(r.Context(), target, r.PathValue("bus"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "eb_rules_by_target", map[string]any{
		"Prefix": c.prefix, "Bus": r.PathValue("bus"), "Target": target, "Names": names,
	})
}

// ebTestPattern answers whether a sample event matches a draft pattern — the
// service's own TestEventPattern, wired to the builder's check row.
func (c *Console) ebTestPattern(w http.ResponseWriter, r *http.Request) {
	ok, err := c.be.TestEventPattern(r.Context(), r.FormValue("pattern"), r.FormValue("event"))
	if err != nil {
		c.partial(w, "eb_pattern_verdict", map[string]any{"Err": err.Error()})
		return
	}
	c.partial(w, "eb_pattern_verdict", map[string]any{"Match": ok, "Ran": true})
}
