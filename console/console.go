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
	"strconv"
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
	m.HandleFunc("GET "+p+"/s3/{bucket}/meta", c.s3Meta)
	m.HandleFunc("POST "+p+"/s3/{bucket}/upload", c.s3Upload)
	m.HandleFunc("POST "+p+"/s3/{bucket}/delete", c.s3DeleteObject)
	m.HandleFunc("POST "+p+"/s3/{bucket}/versioning", c.s3Versioning)

	// SQS.
	m.HandleFunc("GET "+p+"/sqs", c.sqsQueues)
	m.HandleFunc("POST "+p+"/sqs/create", c.sqsCreateQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}", c.sqsQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}/messages", c.sqsMessages) // HTMX partial (polled)
	m.HandleFunc("POST "+p+"/sqs/{queue}/send", c.sqsSend)
	m.HandleFunc("POST "+p+"/sqs/{queue}/purge", c.sqsPurge)
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-queue", c.sqsDeleteQueue)

	// DynamoDB.
	m.HandleFunc("GET "+p+"/ddb", c.ddbTables)
	m.HandleFunc("POST "+p+"/ddb/create", c.ddbCreateTable)
	m.HandleFunc("GET "+p+"/ddb/{table}", c.ddbTable)
	m.HandleFunc("GET "+p+"/ddb/{table}/items", c.ddbItems) // HTMX partial
	m.HandleFunc("POST "+p+"/ddb/{table}/put", c.ddbPutItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-item", c.ddbDeleteItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-table", c.ddbDeleteTable)

	// SNS.
	m.HandleFunc("GET "+p+"/sns", c.snsTopics)
	m.HandleFunc("POST "+p+"/sns/create", c.snsCreateTopic)
	m.HandleFunc("GET "+p+"/sns/{topic}", c.snsTopic)
	m.HandleFunc("POST "+p+"/sns/{topic}/publish", c.snsPublish)
	m.HandleFunc("POST "+p+"/sns/{topic}/subscribe", c.snsSubscribe)
	m.HandleFunc("POST "+p+"/sns/{topic}/unsubscribe", c.snsUnsubscribe)
	m.HandleFunc("POST "+p+"/sns/{topic}/delete-topic", c.snsDeleteTopic)

	// EventBridge.
	m.HandleFunc("GET "+p+"/eb", c.ebBuses)
	m.HandleFunc("POST "+p+"/eb/create-bus", c.ebCreateBus)
	m.HandleFunc("POST "+p+"/eb/{bus}/delete-bus", c.ebDeleteBus)
	m.HandleFunc("GET "+p+"/eb/{bus}", c.ebBus)
	m.HandleFunc("POST "+p+"/eb/{bus}/create-rule", c.ebCreateRule)
	m.HandleFunc("POST "+p+"/eb/{bus}/test-event", c.ebTestEvent)
	m.HandleFunc("GET "+p+"/eb/{bus}/rule/{rule}", c.ebRule)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/add-target", c.ebAddTarget)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/remove-target", c.ebRemoveTarget)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/delete-rule", c.ebDeleteRule)

	// Lambda.
	m.HandleFunc("GET "+p+"/lambda", c.lambdaFns)
	m.HandleFunc("GET "+p+"/lambda/{fn}", c.lambdaFn)
	m.HandleFunc("POST "+p+"/lambda/{fn}/invoke", c.lambdaInvoke)
	m.HandleFunc("POST "+p+"/lambda/{fn}/delete-fn", c.lambdaDelete)
	m.HandleFunc("POST "+p+"/lambda/{fn}/delete-mapping", c.lambdaDeleteMapping)

	// KMS.
	m.HandleFunc("GET "+p+"/kms", c.kmsKeys)
	m.HandleFunc("POST "+p+"/kms/create", c.kmsCreateKey)
	m.HandleFunc("GET "+p+"/kms/{key}", c.kmsKey)
	m.HandleFunc("POST "+p+"/kms/{key}/toggle-enabled", c.kmsToggleEnabled)
	m.HandleFunc("POST "+p+"/kms/{key}/toggle-rotation", c.kmsToggleRotation)
	m.HandleFunc("POST "+p+"/kms/{key}/rotate-now", c.kmsRotateNow)
	m.HandleFunc("POST "+p+"/kms/{key}/schedule-deletion", c.kmsScheduleDeletion)
	m.HandleFunc("POST "+p+"/kms/{key}/encrypt", c.kmsEncrypt)
	m.HandleFunc("POST "+p+"/kms/{key}/decrypt", c.kmsDecrypt)

	// SSM Parameter Store (names contain slashes -> query params).
	m.HandleFunc("GET "+p+"/ssm", c.ssmParams)
	m.HandleFunc("POST "+p+"/ssm/create", c.ssmCreate)
	m.HandleFunc("GET "+p+"/ssm/param", c.ssmParam)
	m.HandleFunc("POST "+p+"/ssm/put", c.ssmPut)
	m.HandleFunc("POST "+p+"/ssm/delete", c.ssmDelete)

	// Secrets Manager (names may contain slashes -> query params).
	m.HandleFunc("GET "+p+"/sm", c.smSecrets)
	m.HandleFunc("POST "+p+"/sm/create", c.smCreate)
	m.HandleFunc("GET "+p+"/sm/secret", c.smSecret)
	m.HandleFunc("POST "+p+"/sm/put", c.smPut)
	m.HandleFunc("POST "+p+"/sm/delete", c.smDelete)

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

// toast asks the client to show a transient notification. htmx turns the
// HX-Trigger header into a "toast" event whose detail.value the layout's Alpine
// listener renders. Call before writing the body.
func toast(w http.ResponseWriter, msg string) {
	w.Header().Set("HX-Trigger", `{"toast":`+strconv.Quote(msg)+`}`)
}

func (c *Console) fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, `<div class="err">`+template.HTMLEscapeString(err.Error())+`</div>`)
}

func templateFuncs(prefix string) template.FuncMap {
	return template.FuncMap{
		"prefix":    func() string { return prefix },
		"icon":      icon,
		"count":     humanCount,
		"hasPrefix": strings.HasPrefix,
		"addOne":    func(n int64) int64 { return n + 1 },
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
				return itoaSize(n) + " B"
			}
			return trimFloat(f) + " " + string(u[i]) + "B"
		},
	}
}
