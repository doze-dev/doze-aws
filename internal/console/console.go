// Package console is a lightweight, server-rendered web UI for inspecting and
// managing a doze-aws Stack — an "AWS console, but local and better". It is
// itself just another client of the gateway (in-process), so it never bypasses
// the real API. HTMX (vendored, embedded) drives partial updates; there is no
// SPA build step and the whole thing ships inside the Go binary.
package console

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/restroute"
	"github.com/doze-dev/doze-aws/peers"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/* static/aws/*
var staticFS embed.FS

// EmbeddedFS returns the trees the console carries inside the binary, keyed by
// the name the lightness budget knows them as.
//
// The console is the largest single addressable thing in the binary — roughly
// 2.6 MB of assets and compiled Go, about 13% — and it is embedded whether or
// not --console is on. That is a deliberate choice (one binary, one product,
// and it works offline), but a deliberate choice deserves a number somebody
// re-approves rather than one that drifts. This is how testdata/lightness.json
// weighs it without this package exporting its embed.FS values as API.
func EmbeddedFS() map[string]fs.FS {
	return map[string]fs.FS{
		"internal/console/templates": templateFS,
		"internal/console/static":    staticFS,
	}
}

// Console is the web-UI http.Handler. Mount it under a path prefix (default
// "/_console") alongside the AWS gateway.
type Console struct {
	be     *backend
	mux    *chi.Mux
	tmpl   *template.Template
	prefix string
	rec    *Recorder
}

// Options configures the console.
type Options struct {
	// Peers resolves each AWS service to an endpoint the console reads and writes
	// through. Embedded: peers.InProcess over the stack's service handlers. Module
	// topology: peers.FromEnv() (per-service unix sockets). The console routes
	// each request to the owning service via gateway.Route, so one console fronts
	// either topology unchanged.
	Peers peers.Directory
	// Suffix is the instance's DNS suffix, standing in for amazonaws.com.
	Suffix string
	// Identity is the region and account the stack behind this console mints
	// ARNs for. The zero value means the conventional local identity.
	Identity awsident.Identity
	// Recorder, if set, feeds the Traffic surface. Wrap the gateway with
	// NewRecorder for external SDK/CLI calls and pass that recorder here. Leave
	// nil in topologies where the console doesn't sit in the external request
	// path (the Traffic surface then reports capture off).
	Recorder *Recorder
	// Prefix is the URL path the console is mounted under; defaults to
	// "/_console".
	Prefix string
}

// New builds a console over the given gateway handler.
func New(opts Options) (*Console, error) {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "/_console"
	}
	prefix = "/" + strings.Trim(prefix, "/")

	tmpl, err := template.New("").Funcs(templateFuncs(prefix, opts.Identity)).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	c := &Console{be: newBackend(opts.Peers, opts.Identity, opts.Suffix), tmpl: tmpl, prefix: prefix, rec: opts.Recorder}
	c.routes()
	return c, nil
}

func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The console is a router of its own, never a sub-router of whatever calls
	// it: chi reads a route context already on the request as a parent's and
	// would route by its method and path.
	if chi.RouteContext(r.Context()) != nil {
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil)))
	}
	c.mux.ServeHTTP(w, r)
}

// param is a path label's value as the client meant it: decoded, with an
// escaped slash kept inside its segment. It stands where r.PathValue stood.
func param(r *http.Request, name string) string { return restroute.Param(r, name) }

// setParam sets a path label, for a list page that opens its first item by
// handing the request to the detail handler.
func setParam(r *http.Request, name, value string) { restroute.SetParam(r, name, value) }

// originMatchesHost reports whether an Origin header's host authority matches the
// request Host (the console's own address).
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == host
}

// render writes a full page (layout + named content template). The request is
// consulted for a ?flash= success banner (set by redirects after creates).
func (c *Console) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Prefix"] = c.prefix
	data["Page"] = page
	data["Endpoint"] = endpointHost(r)
	// The rail's counts ship with the markup instead of arriving up to five
	// seconds later on a poll. Cheap enough to do on every render — see
	// serviceCounts in live.go for the measurement.
	data["Counts"] = c.serviceCounts(r.Context())
	if f := r.URL.Query().Get("flash"); f != "" {
		data["Flash"] = f
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.tmpl.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// redirect sends the browser to `to` with an optional flash banner — via
// HX-Redirect for htmx requests, 303 See Other for plain forms.
// redirectSticky is for a value the user must read before it leaves the screen.
// There is exactly one: a new access key's secret, which AWS never shows again.
// A 3.2s toast would destroy it, so this one gets a banner that stays until
// dismissed — and a copy button, since the whole point is that it is
// unrecoverable.
func (c *Console) redirectSticky(w http.ResponseWriter, r *http.Request, to, flash string) {
	c.redirectMode(w, r, to, flash, true)
}

func (c *Console) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	c.redirectMode(w, r, to, flash, false)
}

func (c *Console) redirectMode(w http.ResponseWriter, r *http.Request, to, flash string, sticky bool) {
	if flash != "" {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + "flash=" + url.QueryEscape(flash)
	}
	if r.Header.Get("HX-Request") == "true" {
		// HX-Redirect is a full window.location navigation: it throws away the
		// scroll position, the filter box, any open drawer, and re-fetches the
		// whole page — for forty-five mutations, nineteen of which redirect to
		// the page the user is already on.
		//
		// HX-Location swaps in place and still goes through htmx's history
		// machinery, so back/forward keep working. The flash rides an HX-Trigger
		// beside it rather than in the URL, which is what stops a refresh
		// re-showing a stale success banner. Both headers are processed before
		// the HX-Location early return.
		//
		// Changing the transport rather than the call sites is deliberate: all
		// forty-five improve without touching one of them, and the non-htmx path
		// below is untouched, so the mutation sweep still sees what it saw.
		if flash != "" {
			kind := "doze:flash"
			if sticky {
				kind = "doze:flash-sticky"
			}
			w.Header().Set("HX-Trigger", `{"`+kind+`":`+strconv.QuoteToASCII(flash)+`}`)
		}
		w.Header().Set("HX-Location", `{"path":`+strconv.QuoteToASCII(stripFlash(to))+
			`,"target":"#workspace","select":"#workspace","swap":"outerHTML"}`)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// stripFlash removes the flash query parameter. On the htmx path the message
// travels as a trigger, so leaving it in the URL would mean a refresh or a back
// navigation re-showing a success that already happened.
func stripFlash(to string) string {
	i := strings.Index(to, "flash=")
	if i < 0 {
		return to
	}
	cut := i - 1 // the ? or & that introduced it
	if cut < 0 {
		return to
	}
	rest := ""
	if j := strings.IndexByte(to[i:], '&'); j >= 0 {
		rest = to[i+j:]
		if to[cut] == '?' {
			rest = "?" + rest[1:]
		}
	}
	return to[:cut] + rest
}

// endpointHost is the host:port the browser reached the console on — the same
// address an SDK/CLI would target. Used for the endpoint chip and copyable
// resource URLs so they don't lie about the actual listen address.
func endpointHost(r *http.Request) string {
	// No fallback. This used to return "127.0.0.1:4566" for a request with no
	// Host — an address doze-aws no longer binds by default, so the chip would
	// have shown a copyable URL that reaches nothing. The console is reached by
	// a browser, which always sends Host; an empty one renders as empty, which
	// is at least true.
	return r.Host
}

// partial renders a single named template (for HTMX swaps).
func (c *Console) partial(w http.ResponseWriter, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Prefix"] = c.prefix
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// toast asks the client to show a transient notification. htmx turns the
// HX-Trigger header into a "toast" event whose detail.value the layout's Alpine
// listener renders. Call before writing the body.
func toast(w http.ResponseWriter, msg string) {
	// QuoteToASCII (not Quote): HTTP header values are latin-1, so any non-ASCII
	// rune (arrows, curly quotes, …) must be backslash-u escaped to survive the
	// header — the browser JSON.parse decodes it back before showing the toast.
	w.Header().Set("HX-Trigger", `{"toast":`+strconv.QuoteToASCII(msg)+`}`)
}

// fail renders a console-driven call's failure the way the wire renders a
// client's: the code, the message, and a line about where the fix lives.
//
// The status stays 400 and no htmx config changes. The header is what marks
// the response: shell.js reads it on htmx:after:request to place this body
// next to the control that failed, and cancels htmx:before:swap so it never
// reaches the success target the request was aimed at. That is why none of the
// ~180 call sites had to change, and why an error can never clobber a target it
// was not addressed to.
//
// Under htmx 2 the cancel was belt and braces, because 4xx did not swap by
// default. htmx 4 swaps every status except 204 and 304, so it now carries the
// whole guarantee on its own.
func (c *Console) fail(w http.ResponseWriter, err error) {
	w.Header().Set("HX-Doze-Error", "1")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	if err := c.tmpl.ExecuteTemplate(w, "fail_inline", failView(err)); err != nil {
		io.WriteString(w, `<div class="err">`+template.HTMLEscapeString(err.Error())+`</div>`)
	}
}

// failReason is what the user is shown when a console action fails.
type failReason struct {
	Code    string
	Message string
	State   string // served | refused | denied | error — same vocabulary as the wire
}

// failView decodes an error into the same shape the wire's inspector uses. An
// error that is not an AWS refusal (a form validation, a bad parameter) still
// gets a Message, so the template has one branch rather than two.
func failView(err error) failReason {
	var ae *apiErr
	if errors.As(err, &ae) {
		if r := parseRefusal(ae.status, ae.body); r != nil {
			return failReason{Code: r.Code, Message: r.Message, State: callState(ae.status, r)}
		}
		return failReason{Message: strings.TrimSpace(ae.body), State: callState(ae.status, nil)}
	}
	return failReason{Message: err.Error(), State: "refused"}
}

func templateFuncs(prefix string, id awsident.Identity) template.FuncMap {
	return template.FuncMap{
		"prefix": func() string { return prefix },
		// splitPath cuts "/aws/lambda/orders" into its directory and leaf, so
		// a list can let the shared part shrink and keep the part that
		// tells the entries apart.
		"splitPath": func(p string) []string {
			i := strings.LastIndex(p, "/")
			return []string{p[:i+1], p[i+1:]}
		},
		// account and region are the INSTANCE's, not the package defaults. A
		// template that hardcodes 000000000000 renders an ARN a user cannot
		// paste anywhere under --account-id, and several of these sites are
		// copy-as-CLI snippets and form values rather than placeholders.
		"account":   id.Account,
		"region":    id.RegionName,
		"icon":      icon,
		"count":     humanCount,
		"hasPrefix": strings.HasPrefix,
		// has reports membership, for rendering a checked box against a set the
		// resource already carries.
		// mul indents the route tree by depth without the template counting
		// path separators itself.
		"mul": func(a, b int) int { return a * b },
		"ge":  func(a, b int) bool { return a >= b },
		"has": func(set []string, v string) bool {
			return slices.Contains(set, v)
		},
		"slug":      resSlug,
		"tagsJSON":  tagsJSON,
		"secs":      humanSecs,
		"ago":       ago,
		"list":      func(items ...any) []any { return items },
		"join":      strings.Join,
		"masked":    maskedValue,
		"add":       func(a, b int) int { return a + b },
		"addOne":    func(n int64) int64 { return n + 1 },
		"ssmGroups": ssmGroups,
		// patternPreview flattens an event pattern to a scannable one-liner:
		// {"source":["shop.orders"]} → source=[shop.orders]
		"patternPreview": patternPreview,
		// awsIcon renders an official AWS Architecture service icon (embedded).
		// resolve turns an ARN, a queue URL or a bare identifier into a link.
		// One resolver, so a target renders the same wherever it appears.
		"resolve": func(id string) resourceRef { return resourceFromARN(id) },
		// emptyCopy hands a template a service's empty-state voice. The copy lives
		// in copy.go so the thirteen read as one person wrote them.
		"emptyCopy": emptyFor,
		"resolveIn": resourceURL,
		"awsIcon": func(svc string) template.HTML {
			return template.HTML(`<img class="aws-ic" src="` + prefix + `/static/aws/` + svc + `.svg" alt="" loading="lazy">`)
		},
		// sharePct renders a shard's slice of the hash space. The raw bounds are
		// 39-digit integers; a percentage is the only readable form.
		"sharePct": pct,
		// midpoint is the split key halfway through a shard, so the split
		// control does not ask anyone to type a 128-bit number.
		"midpoint": MidpointOf,
		// dict builds a map for passing several values to a nested template.
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					m[k] = kv[i+1]
				}
			}
			return m
		},
		// trimPrefixKey strips the current folder prefix from a key so the table
		// shows just the leaf ("photos/2024/a.jpg" under "photos/2024/" -> "a.jpg").
		"trimPrefixKey": func(key, keyPrefix string) string {
			return strings.TrimPrefix(key, keyPrefix)
		},
		"humanSize": func(n int64) string {
			const u = "BKMGT"
			f := float64(n)
			i := 0
			for f >= 1024 && i < len(u)-1 {
				f /= 1024
				i++
			}
			if i == 0 {
				return strconv.FormatInt(n, 10) + " B"
			}
			return trimFloat(f) + " " + string(u[i]) + "B"
		},
	}
}
