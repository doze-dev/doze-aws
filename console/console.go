// Package console is a lightweight, server-rendered web UI for inspecting and
// managing a doze-aws Stack — an "AWS console, but local and better". It is
// itself just another client of the gateway (in-process), so it never bypasses
// the real API. HTMX (vendored, embedded) drives partial updates; there is no
// SPA build step and the whole thing ships inside the Go binary.
package console

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/* static/aws/*
var staticFS embed.FS

// Console is the web-UI http.Handler. Mount it under a path prefix (default
// "/_console") alongside the AWS gateway.
type Console struct {
	be     *backend
	mux    *http.ServeMux
	tmpl   *template.Template
	prefix string
	rec    *Recorder
}

// Options configures the console.
type Options struct {
	// Gateway is the AWS endpoint handler the console reads and writes through
	// (typically stack.Handler()). Pass the RAW gateway, not the traffic-wrapped
	// one, so the console's own calls don't appear in the Traffic tail.
	Gateway http.Handler
	// Recorder, if set, feeds the Traffic surface. Wrap the gateway with
	// NewRecorder for external SDK/CLI calls and pass that recorder here.
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

	tmpl, err := template.New("").Funcs(templateFuncs(prefix)).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	c := &Console{be: newBackend(opts.Gateway), tmpl: tmpl, prefix: prefix, rec: opts.Recorder}
	c.routes()
	return c, nil
}

func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) { c.mux.ServeHTTP(w, r) }

func (c *Console) routes() {
	m := http.NewServeMux()
	p := c.prefix

	// Static assets (htmx, css) — embedded, served locally (no CDN).
	m.Handle("GET "+p+"/static/", http.StripPrefix(p+"/", http.FileServerFS(staticFS)))

	// Flows is the home surface; Traffic is the live API tail.
	m.HandleFunc("GET "+p+"/", c.flows)
	m.HandleFunc("GET "+p, c.flows)
	m.HandleFunc("GET "+p+"/flows.json", c.flowsData) // polled live refresh
	m.HandleFunc("GET "+p+"/traffic", c.traffic)
	m.HandleFunc("GET "+p+"/traffic/feed", c.trafficFeed) // polled live tail

	// Resource index for the command palette.
	m.HandleFunc("GET "+p+"/api/resources", c.apiResources)
	m.HandleFunc("GET "+p+"/api/counts", c.apiCounts)
	m.HandleFunc("GET "+p+"/tags/view", c.tagsView)
	m.HandleFunc("POST "+p+"/tags/set", c.tagsSet)
	m.HandleFunc("POST "+p+"/tags/remove", c.tagsRemove)

	// Create forms render inside the shell (list pane + detail).
	m.HandleFunc("GET "+p+"/s3/create", c.createPage("s3", "s3_create"))
	m.HandleFunc("GET "+p+"/sqs/create", c.createPage("sqs", "sqs_create"))
	m.HandleFunc("GET "+p+"/ddb/create", c.createPage("ddb", "ddb_create"))
	m.HandleFunc("GET "+p+"/sns/create", c.createPage("sns", "sns_create"))
	m.HandleFunc("GET "+p+"/eb/create-bus", c.createPage("eb", "eb_bus_create"))
	m.HandleFunc("GET "+p+"/eb/{bus}/create-rule", c.ebRuleCreatePage)
	m.HandleFunc("GET "+p+"/kms/create", c.createPage("kms", "kms_create"))
	m.HandleFunc("GET "+p+"/ssm/create", c.createPage("ssm", "ssm_create"))
	m.HandleFunc("GET "+p+"/sm/create", c.createPage("sm", "sm_create"))

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
	m.HandleFunc("POST "+p+"/s3/{bucket}/add-tag", c.s3AddTag)
	m.HandleFunc("POST "+p+"/s3/{bucket}/remove-tag", c.s3RemoveTag)
	m.HandleFunc("POST "+p+"/s3/{bucket}/presign", c.s3Presign)
	m.HandleFunc("POST "+p+"/s3/{bucket}/copy", c.s3Copy)
	m.HandleFunc("GET "+p+"/s3/{bucket}/versions", c.s3Versions)
	m.HandleFunc("POST "+p+"/s3/{bucket}/restore-version", c.s3RestoreVersion)
	m.HandleFunc("POST "+p+"/s3/{bucket}/delete-version", c.s3DeleteVersion)
	m.HandleFunc("POST "+p+"/s3/{bucket}/notify-add", c.s3NotifyAdd)
	m.HandleFunc("POST "+p+"/s3/{bucket}/notify-remove", c.s3NotifyRemove)
	m.HandleFunc("POST "+p+"/s3/{bucket}/cors", c.s3SaveCORS)
	m.HandleFunc("POST "+p+"/s3/{bucket}/lifecycle", c.s3SaveLifecycle)

	// SQS.
	m.HandleFunc("GET "+p+"/sqs", c.sqsQueues)
	m.HandleFunc("POST "+p+"/sqs/create", c.sqsCreateQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}", c.sqsQueue)
	m.HandleFunc("GET "+p+"/sqs/{queue}/messages", c.sqsMessages) // HTMX partial (polled)
	m.HandleFunc("POST "+p+"/sqs/{queue}/send", c.sqsSend)
	m.HandleFunc("POST "+p+"/sqs/{queue}/purge", c.sqsPurge)
	m.HandleFunc("POST "+p+"/sqs/{queue}/attributes", c.sqsSetAttributes)
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-message", c.sqsDeleteMessage)
	m.HandleFunc("POST "+p+"/sqs/{queue}/redrive", c.sqsRedrive)
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-queue", c.sqsDeleteQueue)

	// DynamoDB.
	m.HandleFunc("GET "+p+"/ddb", c.ddbTables)
	m.HandleFunc("POST "+p+"/ddb/create", c.ddbCreateTable)
	m.HandleFunc("GET "+p+"/ddb/{table}", c.ddbTable)
	m.HandleFunc("POST "+p+"/ddb/{table}/explore", c.ddbExplore) // HTMX partial (scan/query/partiql)
	m.HandleFunc("POST "+p+"/ddb/{table}/put", c.ddbPutItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-item", c.ddbDeleteItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-table", c.ddbDeleteTable)
	m.HandleFunc("POST "+p+"/ddb/{table}/ttl", c.ddbSetTTL)
	m.HandleFunc("POST "+p+"/ddb/{table}/add-gsi", c.ddbAddGSI)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-gsi", c.ddbDeleteGSI)

	// SNS.
	m.HandleFunc("GET "+p+"/sns", c.snsTopics)
	m.HandleFunc("POST "+p+"/sns/create", c.snsCreateTopic)
	m.HandleFunc("GET "+p+"/sns/{topic}", c.snsTopic)
	m.HandleFunc("POST "+p+"/sns/{topic}/publish", c.snsPublish)
	m.HandleFunc("POST "+p+"/sns/{topic}/subscribe", c.snsSubscribe)
	m.HandleFunc("POST "+p+"/sns/{topic}/unsubscribe", c.snsUnsubscribe)
	m.HandleFunc("POST "+p+"/sns/{topic}/sub-filter", c.snsSubFilter)
	m.HandleFunc("POST "+p+"/sns/{topic}/sub-raw", c.snsSubRaw)
	m.HandleFunc("POST "+p+"/sns/{topic}/delete-topic", c.snsDeleteTopic)

	// EventBridge.
	m.HandleFunc("GET "+p+"/eb", c.ebBuses)
	m.HandleFunc("POST "+p+"/eb/create-bus", c.ebCreateBus)
	m.HandleFunc("POST "+p+"/eb/{bus}/delete-bus", c.ebDeleteBus)
	m.HandleFunc("GET "+p+"/eb/{bus}", c.ebBus)
	m.HandleFunc("POST "+p+"/eb/{bus}/create-rule", c.ebCreateRule)
	m.HandleFunc("POST "+p+"/eb/{bus}/test-event", c.ebTestEvent)
	m.HandleFunc("POST "+p+"/eb/{bus}/match", c.ebMatch) // HTMX partial (live rule matcher)
	m.HandleFunc("POST "+p+"/eb/{bus}/create-archive", c.ebCreateArchive)
	m.HandleFunc("POST "+p+"/eb/{bus}/delete-archive", c.ebDeleteArchive)
	m.HandleFunc("POST "+p+"/eb/{bus}/replay", c.ebReplay)
	m.HandleFunc("GET "+p+"/eb/{bus}/rule/{rule}", c.ebRule)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/add-target", c.ebAddTarget)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/remove-target", c.ebRemoveTarget)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/delete-rule", c.ebDeleteRule)
	m.HandleFunc("POST "+p+"/eb/{bus}/rule/{rule}/toggle", c.ebToggleRule)

	// Lambda.
	m.HandleFunc("GET "+p+"/lambda", c.lambdaFns)
	m.HandleFunc("GET "+p+"/lambda/create", c.createPage("lambda", "lambda_create"))
	m.HandleFunc("POST "+p+"/lambda/create", c.lambdaCreate)
	m.HandleFunc("GET "+p+"/lambda/{fn}", c.lambdaFn)
	m.HandleFunc("GET "+p+"/lambda/{fn}/runtime", c.lambdaRuntimeBadge) // HTMX partial (polled)
	m.HandleFunc("POST "+p+"/lambda/{fn}/invoke", c.lambdaInvoke)
	m.HandleFunc("POST "+p+"/lambda/{fn}/delete-fn", c.lambdaDelete)
	m.HandleFunc("POST "+p+"/lambda/{fn}/delete-mapping", c.lambdaDeleteMapping)
	m.HandleFunc("POST "+p+"/lambda/{fn}/add-mapping", c.lambdaAddMapping)
	m.HandleFunc("POST "+p+"/lambda/{fn}/config", c.lambdaSaveConfig)
	m.HandleFunc("POST "+p+"/lambda/{fn}/create-url", c.lambdaCreateURL)
	m.HandleFunc("POST "+p+"/lambda/{fn}/delete-url", c.lambdaDeleteURL)

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
	m.HandleFunc("POST "+p+"/kms/{key}/sign", c.kmsSign)
	m.HandleFunc("POST "+p+"/kms/{key}/verify", c.kmsVerify)
	m.HandleFunc("POST "+p+"/kms/{key}/mac", c.kmsMac)
	m.HandleFunc("POST "+p+"/kms/{key}/verify-mac", c.kmsVerifyMac)
	m.HandleFunc("POST "+p+"/kms/{key}/add-alias", c.kmsAddAlias)
	m.HandleFunc("POST "+p+"/kms/{key}/delete-alias", c.kmsDeleteAlias)
	m.HandleFunc("POST "+p+"/kms/{key}/cancel-deletion", c.kmsCancelDeletion)

	// SSM Parameter Store (names contain slashes -> query params).
	m.HandleFunc("GET "+p+"/ssm", c.ssmParams)
	m.HandleFunc("POST "+p+"/ssm/create", c.ssmCreate)
	m.HandleFunc("GET "+p+"/ssm/param", c.ssmParam)
	m.HandleFunc("GET "+p+"/ssm/diff", c.ssmDiff)
	m.HandleFunc("POST "+p+"/ssm/put", c.ssmPut)
	m.HandleFunc("POST "+p+"/ssm/delete", c.ssmDelete)
	m.HandleFunc("POST "+p+"/ssm/label", c.ssmLabel)

	// Secrets Manager (names may contain slashes -> query params).
	m.HandleFunc("GET "+p+"/sm", c.smSecrets)
	m.HandleFunc("POST "+p+"/sm/create", c.smCreate)
	m.HandleFunc("POST "+p+"/sm/restore", c.smRestore)
	m.HandleFunc("POST "+p+"/sm/rotation", c.smConfigureRotation)
	m.HandleFunc("POST "+p+"/sm/rotate-now", c.smRotateNow)
	m.HandleFunc("GET "+p+"/sm/password", c.smPassword)
	m.HandleFunc("GET "+p+"/sm/secret", c.smSecret)
	m.HandleFunc("GET "+p+"/sm/diff", c.smDiff)
	m.HandleFunc("POST "+p+"/sm/put", c.smPut)
	m.HandleFunc("POST "+p+"/sm/delete", c.smDelete)

	c.mux = m
}

// render writes a full page (layout + named content template). The request is
// consulted for a ?flash= success banner (set by redirects after creates).
func (c *Console) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Prefix"] = c.prefix
	data["Page"] = page
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
func (c *Console) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if flash != "" {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + "flash=" + url.QueryEscape(flash)
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
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
		"ago":       ago,
		"list":      func(items ...any) []any { return items },
		"masked":    maskedValue,
		"add":       func(a, b int) int { return a + b },
		"sub":       func(a, b int) int { return a - b },
		"nodeAt": func(ns []FlowNode, id string) *FlowNode {
			for i := range ns {
				if ns[i].ID == id {
					return &ns[i]
				}
			}
			return nil
		},
		"edgePath": func(f, t *FlowNode) string {
			// forward: right edge → next left edge. backward (redrive/dlq to a
			// node at/behind us): drop from the bottom into the target's top.
			if t.X <= f.X {
				x1, y1 := f.X+88, f.Y+46
				x2, y2 := t.X+88, t.Y
				return fmt.Sprintf("M%d %d C %d %d %d %d %d %d", x1, y1, x1, (y1+y2)/2, x2, (y1+y2)/2, x2, y2)
			}
			x1, y1 := f.X+176, f.Y+23
			x2, y2 := t.X, t.Y+23
			mx := (x1 + x2) / 2
			return fmt.Sprintf("M%d %d C %d %d %d %d %d %d", x1, y1, mx, y1, mx, y2, x2, y2)
		},
		"svcGlyph": func(svc string) string {
			return map[string]string{"s3": "▦", "sqs": "▤", "sns": "▲", "eb": "◇", "lambda": "λ"}[svc]
		},
		"addOne":    func(n int64) int64 { return n + 1 },
		"ssmGroups": ssmGroups,
		// awsIcon renders an official AWS Architecture service icon (embedded).
		"awsIcon": func(svc string) template.HTML {
			return template.HTML(`<img class="aws-ic" src="` + prefix + `/static/aws/` + svc + `.svg" alt="" loading="lazy">`)
		},
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
