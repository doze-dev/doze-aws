package console

import (
	"net/http"
	"strings"
)

// EventBridge connections and API destinations: one page listing both, with
// create forms, and a detail panel per item for update, deauthorize, delete.

func (c *Console) connectionForm(r *http.Request) EBConnectionForm {
	return EBConnectionForm{
		Name: strings.TrimSpace(r.FormValue("name")), Description: strings.TrimSpace(r.FormValue("description")),
		AuthType: r.FormValue("auth_type"),
		Username: r.FormValue("username"), Password: r.FormValue("password"),
		APIKeyName: strings.TrimSpace(r.FormValue("api_key_name")), APIKeyValue: r.FormValue("api_key_value"),
		ClientID: r.FormValue("client_id"), ClientSecret: r.FormValue("client_secret"),
		Endpoint: strings.TrimSpace(r.FormValue("endpoint")), Method: r.FormValue("method"),
		Headers: r.FormValue("headers"), Query: r.FormValue("query"), Body: r.FormValue("body"),
	}
}

func (c *Console) ebDestinations(w http.ResponseWriter, r *http.Request) {
	conns, err := c.be.ListConnections(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	dests, _ := c.be.ListDestinations(r.Context())
	buses, _ := c.be.ListBuses(r.Context())
	c.render(w, r, "eb_destinations", map[string]any{
		"Connections": conns, "Destinations": dests, "List": buses, "Title": "API destinations · EventBridge",
	})
}

// ebDestinationsPartial re-renders both tables after a mutation.
func (c *Console) ebDestinationsPartial(w http.ResponseWriter, r *http.Request) {
	conns, err := c.be.ListConnections(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	dests, _ := c.be.ListDestinations(r.Context())
	c.partial(w, "eb_http_tables", map[string]any{"Connections": conns, "Destinations": dests})
}

func (c *Console) ebCreateConnection(w http.ResponseWriter, r *http.Request) {
	if err := c.be.CreateConnection(r.Context(), c.connectionForm(r)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Connection created")
	c.ebDestinationsPartial(w, r)
}

func (c *Console) ebDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteConnection(r.Context(), r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Connection deleted; its destinations are now INACTIVE")
	c.ebDestinationsPartial(w, r)
}

func (c *Console) ebConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := c.be.DescribeConnection(r.Context(), r.PathValue("conn"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "eb_connection_detail", map[string]any{"C": conn})
}

func (c *Console) ebUpdateConnection(w http.ResponseWriter, r *http.Request) {
	f := c.connectionForm(r)
	f.Name = r.PathValue("conn")
	if err := c.be.UpdateConnection(r.Context(), f); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Connection updated")
	c.ebConnection(w, r)
}

func (c *Console) ebDeauthorizeConnection(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeauthorizeConnection(r.Context(), r.PathValue("conn")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Credential removed; deliveries through this connection stop")
	c.ebConnection(w, r)
}

func (c *Console) ebCreateDestination(w http.ResponseWriter, r *http.Request) {
	if err := c.be.CreateDestination(r.Context(), strings.TrimSpace(r.FormValue("name")), r.FormValue("connection"),
		strings.TrimSpace(r.FormValue("endpoint")), r.FormValue("method"), strings.TrimSpace(r.FormValue("description")),
		atoiDefault(r.FormValue("rate"), 0)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API destination created")
	c.ebDestinationsPartial(w, r)
}

func (c *Console) ebDeleteDestination(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteDestination(r.Context(), r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API destination deleted")
	c.ebDestinationsPartial(w, r)
}

func (c *Console) ebDestination(w http.ResponseWriter, r *http.Request) {
	d, err := c.be.DescribeDestination(r.Context(), r.PathValue("dest"))
	if err != nil {
		c.fail(w, err)
		return
	}
	conns, _ := c.be.ListConnections(r.Context())
	c.partial(w, "eb_destination_detail", map[string]any{"D": d, "Connections": conns})
}

func (c *Console) ebUpdateDestination(w http.ResponseWriter, r *http.Request) {
	if err := c.be.UpdateDestination(r.Context(), r.PathValue("dest"), r.FormValue("connection"),
		strings.TrimSpace(r.FormValue("endpoint")), r.FormValue("method"), strings.TrimSpace(r.FormValue("description")),
		atoiDefault(r.FormValue("rate"), 0)); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "API destination updated")
	c.ebDestination(w, r)
}
