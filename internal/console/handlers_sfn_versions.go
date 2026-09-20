package console

import (
	"net/http"
	"strconv"
	"strings"
)

// ---- Step Functions: the Versions & aliases tab ----
//
// One tab, four things on it: a publish form, the versions table, the
// aliases table (with inline routing edits), and a create-alias form. Every
// mutation redirects back to the tab with a flash, the way the definition
// save does — none of them is a live region, and a tab that re-renders whole
// after a change is easier to trust than one that patches itself.

// sfnVersionsData is what the tab renders. It is also what the Start tab's
// "Run as" select reads, so it is one function for both.
func (c *Console) sfnVersionsData(r *http.Request, name string) map[string]any {
	arn := stateMachineARNOf(name)
	versions, _ := c.be.ListVersions(r.Context(), arn)
	aliases, _ := c.be.ListMachineAliases(r.Context(), arn)
	return map[string]any{"Versions": versions, "Aliases": aliases}
}

func (c *Console) sfnPublish(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	arn, err := c.be.PublishVersion(r.Context(), stateMachineARNOf(name), strings.TrimSpace(r.FormValue("description")))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=versions", "Published version "+qualifierOf(arn)+" — the definition as it stands now, frozen")
}

// sfnVersion is the detail partial for one version: DescribeStateMachine on
// the version ARN, which answers the snapshot rather than the machine.
func (c *Console) sfnVersion(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		c.fail(w, err)
		return
	}
	sm, err := c.be.DescribeStateMachine(r.Context(), versionARNOf(name, n))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sfn_version_detail", map[string]any{"Name": name, "Version": n, "Machine": sm})
}

func (c *Console) sfnVersionDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.DeleteVersion(r.Context(), versionARNOf(name, n)); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=versions", "Version "+strconv.Itoa(n)+" deleted")
}

// routesFromForm reads the one or two version+weight rows an alias form
// carries (v1/w1, v2/w2). A second row with no version is not a row. The
// weights are the service's to check — one entry summing to 100 or two
// that do — so a bad split is refused with the wire's own message rather
// than a second, console-only rule.
func routesFromForm(r *http.Request, machine string) []Route {
	var routes []Route
	for _, i := range []string{"1", "2"} {
		v := strings.TrimSpace(r.FormValue("v" + i))
		if v == "" {
			continue
		}
		n, _ := strconv.Atoi(v)
		weight, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("w" + i)))
		routes = append(routes, Route{VersionARN: versionARNOf(machine, n), Version: n, Weight: weight})
	}
	// One row with no weight typed means all of it: the form's single-version
	// case should not make anyone type 100.
	if len(routes) == 1 && routes[0].Weight == 0 {
		routes[0].Weight = 100
	}
	return routes
}

func (c *Console) sfnAliasCreate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	alias := strings.TrimSpace(r.FormValue("name"))
	_, err := c.be.CreateMachineAlias(r.Context(), alias, strings.TrimSpace(r.FormValue("description")), routesFromForm(r, name))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=versions", "Alias “"+alias+"” created — start an execution through it from the Start tab")
}

// sfnAliasUpdate is the inline routing edit. The description travels only
// when the form carries the field, so the routing row cannot blank it.
func (c *Console) sfnAliasUpdate(w http.ResponseWriter, r *http.Request) {
	name, alias := r.PathValue("machine"), r.PathValue("alias")
	r.ParseForm()
	var description *string
	if _, ok := r.Form["description"]; ok {
		d := strings.TrimSpace(r.FormValue("description"))
		description = &d
	}
	if err := c.be.UpdateMachineAlias(r.Context(), aliasARNOf(name, alias), description, routesFromForm(r, name)); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=versions", "Alias “"+alias+"” updated — the next execution through it follows the new routing")
}

func (c *Console) sfnAliasDelete(w http.ResponseWriter, r *http.Request) {
	name, alias := r.PathValue("machine"), r.PathValue("alias")
	if err := c.be.DeleteMachineAlias(r.Context(), aliasARNOf(name, alias)); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sfn/"+name+"?tab=versions", "Alias “"+alias+"” deleted — its versions stay")
}

// startTargetOf reads the Start tab's "Run as" choice: the machine itself
// (""), or a version or alias qualifier. The qualifier is appended to the
// machine's own ARN rather than taken as an ARN from the form, so the form
// cannot start a different machine than the page it is on.
func startTargetOf(r *http.Request, machine string) string {
	arn := stateMachineARNOf(machine)
	if q := strings.TrimSpace(r.FormValue("target")); q != "" {
		arn += ":" + q
	}
	return arn
}
