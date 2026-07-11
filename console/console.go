// Package console is a lightweight, server-rendered web UI for inspecting and
// managing a doze-aws Stack — an "AWS console, but local and better". It is
// itself just another client of the gateway (in-process), so it never bypasses
// the real API. HTMX (vendored, embedded) drives partial updates; there is no
// SPA build step and the whole thing ships inside the Go binary.
package console

import (
	"embed"
	"html/template"
	"io"
	"net/http"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Console is the web-UI http.Handler. Mount it under a path prefix (default
// "/_console") alongside the AWS gateway.
type Console struct {
	be     *backend
	mux    *http.ServeMux
	tmpl   *template.Template
	prefix string
}

// Options configures the console.
type Options struct {
	// Gateway is the AWS endpoint handler the console reads and writes through
	// (typically stack.Handler()).
	Gateway http.Handler
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

	tmpl, err := template.New("").Funcs(templateFuncs(prefix)).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	c := &Console{be: newBackend(opts.Gateway), tmpl: tmpl, prefix: prefix}
	c.routes()
	return c, nil
}

func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) { c.mux.ServeHTTP(w, r) }

func (c *Console) routes() {
	m := http.NewServeMux()
	p := c.prefix

	// Static assets (htmx, css) — embedded, served locally (no CDN).
	m.Handle("GET "+p+"/static/", http.StripPrefix(p+"/", http.FileServerFS(staticFS)))

	m.HandleFunc("GET "+p+"/", c.overview)
	m.HandleFunc("GET "+p, c.overview)

	// S3.
	m.HandleFunc("GET "+p+"/s3", c.s3Buckets)
	m.HandleFunc("POST "+p+"/s3/create", c.s3CreateBucket)
	m.HandleFunc("POST "+p+"/s3/{bucket}/delete-bucket", c.s3DeleteBucket)
	m.HandleFunc("GET "+p+"/s3/{bucket}", c.s3Objects)
	m.HandleFunc("GET "+p+"/s3/{bucket}/object", c.s3GetObject)
	m.HandleFunc("POST "+p+"/s3/{bucket}/upload", c.s3Upload)
	m.HandleFunc("POST "+p+"/s3/{bucket}/delete", c.s3DeleteObject)

	// SQS.
	m.HandleFunc("GET "+p+"/sqs", c.sqsQueues)
	m.HandleFunc("POST "+p+"/sqs/create", c.sqsCreateQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}", c.sqsQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}/messages", c.sqsMessages) // HTMX partial (polled)
	m.HandleFunc("POST "+p+"/sqs/{queue}/send", c.sqsSend)
	m.HandleFunc("POST "+p+"/sqs/{queue}/purge", c.sqsPurge)
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-queue", c.sqsDeleteQueue)

	c.mux = m
}

// render writes a full page (layout + named content template).
func (c *Console) render(w http.ResponseWriter, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Prefix"] = c.prefix
	data["Page"] = page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.tmpl.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
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

func (c *Console) fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, `<div class="err">`+template.HTMLEscapeString(err.Error())+`</div>`)
}

func templateFuncs(prefix string) template.FuncMap {
	return template.FuncMap{
		"prefix": func() string { return prefix },
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
				return itoaSize(n) + " B"
			}
			return trimFloat(f) + " " + string(u[i]) + "B"
		},
	}
}
