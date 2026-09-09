package console

import (
	"net/http"
	"strings"
)

// Lambda authorizers on the API's Settings tab: list, add, edit the TTL,
// delete; the add-method dialog picks one for a CUSTOM method.

func (c *Console) apigwAuthorizersPartial(w http.ResponseWriter, r *http.Request, apiID string) {
	auths, err := c.be.APIAuthorizers(r.Context(), apiID)
	if err != nil {
		c.fail(w, err)
		return
	}
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "apigw_authorizers", map[string]any{"API": map[string]any{"ID": apiID}, "Authorizers": auths, "Functions": fns})
}

func (c *Console) apigwCreateAuthorizer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.CreateAPIAuthorizer(r.Context(), id, strings.TrimSpace(r.FormValue("name")), r.FormValue("type"),
		strings.TrimSpace(r.FormValue("function")), strings.TrimSpace(r.FormValue("source")), atoiDefault(r.FormValue("ttl"), 300)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Authorizer added — pick it when adding a method")
	c.apigwAuthorizersPartial(w, r, id)
}

func (c *Console) apigwUpdateAuthorizer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.UpdateAPIAuthorizer(r.Context(), id, r.FormValue("id"), atoiDefault(r.FormValue("ttl"), 300)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Authorizer updated; its cached verdicts are dropped")
	c.apigwAuthorizersPartial(w, r, id)
}

func (c *Console) apigwDeleteAuthorizer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("api")
	if err := c.be.DeleteAPIAuthorizer(r.Context(), id, r.FormValue("id")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Authorizer deleted")
	c.apigwAuthorizersPartial(w, r, id)
}

// apigwAuthorizer is one authorizer's detail row (GetAuthorizer).
func (c *Console) apigwAuthorizer(w http.ResponseWriter, r *http.Request) {
	a, err := c.be.GetAPIAuthorizer(r.Context(), r.PathValue("api"), r.PathValue("auth"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "apigw_authorizer_detail", map[string]any{"API": map[string]any{"ID": r.PathValue("api")}, "A": a})
}
