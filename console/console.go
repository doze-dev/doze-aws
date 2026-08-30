// Package console is a lightweight, server-rendered web UI for inspecting and
// managing a doze-aws Stack — an "AWS console, but local and better". It is
// itself just another client of the gateway (in-process), so it never bypasses
// the real API. HTMX (vendored, embedded) drives partial updates; there is no
// SPA build step and the whole thing ships inside the Go binary.
package console

import (
	"embed"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/peers"
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
	// Peers resolves each AWS service to an endpoint the console reads and writes
	// through. Embedded: peers.InProcess over the stack's service handlers. Module
	// topology: peers.FromEnv() (per-service unix sockets). The console routes
	// each request to the owning service via gateway.Route, so one console fronts
	// either topology unchanged.
	Peers peers.Directory
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

	tmpl, err := template.New("").Funcs(templateFuncs(prefix)).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	c := &Console{be: newBackend(opts.Peers), tmpl: tmpl, prefix: prefix, rec: opts.Recorder}
	c.routes()
	return c, nil
}

func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CSRF / DNS-rebinding defense: a state-changing request must originate from
	// the console itself. Browsers always send Origin on cross-origin (and most
	// same-origin) POSTs; when present it must match the Host we're serving on.
	// This blocks a malicious page from driving destructive actions against a
	// developer's localhost console.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if origin := r.Header.Get("Origin"); origin != "" && !originMatchesHost(origin, r.Host) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
	}
	c.mux.ServeHTTP(w, r)
}

// originMatchesHost reports whether an Origin header's host authority matches the
// request Host (the console's own address).
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == host
}

func (c *Console) routes() {
	m := http.NewServeMux()
	p := c.prefix

	// Static assets (htmx, css) — embedded, served locally (no CDN). Embedded
	// files have a zero modtime, so plain FileServerFS gives the browser no
	// validator at all and every hard reload re-downloads ~700KB (font,
	// CodeMirror, htmx, Alpine). cacheStatic adds content ETags + max-age.
	m.Handle("GET "+p+"/static/", http.StripPrefix(p+"/", cacheStatic(http.FileServerFS(staticFS))))

	// The wire is the home surface: the question people open this to answer is
	// "what did my app just do", not "what resources exist".
	m.HandleFunc("GET "+p+"/", c.traffic)
	m.HandleFunc("GET "+p, c.traffic)
	m.HandleFunc("GET "+p+"/traffic", c.traffic)
	m.HandleFunc("GET "+p+"/connect", c.connect)
	m.HandleFunc("POST "+p+"/connect/verify", c.connectVerify)
	m.HandleFunc("GET "+p+"/deck", c.deck)                   // the stack at a glance
	m.HandleFunc("GET "+p+"/traffic/feed", c.trafficFeed)    // polled live tail
	m.HandleFunc("GET "+p+"/traffic/entry", c.trafficEntry)  // inspector drawer
	m.HandleFunc("POST "+p+"/traffic/clear", c.trafficClear) // empty the ring

	// Resource index for the command palette.
	m.HandleFunc("GET "+p+"/api/resources", c.apiResources)
	m.HandleFunc("GET "+p+"/api/palette", c.apiPalette)
	m.HandleFunc("GET "+p+"/api/resolve", c.apiResolve)
	m.HandleFunc("GET "+p+"/api/counts", c.apiCounts)
	m.HandleFunc("GET "+p+"/api/glance", c.apiGlance) // one-call feed for the doze dash page
	m.HandleFunc("GET "+p+"/tags/view", c.tagsView)
	m.HandleFunc("POST "+p+"/tags/set", c.tagsSet)
	m.HandleFunc("POST "+p+"/tags/remove", c.tagsRemove)
	m.HandleFunc("POST "+p+"/tags/save", c.tagsSave) // the whole set, explicitly

	// Create forms render inside the shell (list pane + detail).
	m.HandleFunc("GET "+p+"/s3/create", c.createPage("s3", "s3_create"))
	m.HandleFunc("GET "+p+"/sqs/create", c.createPage("sqs", "sqs_create"))
	m.HandleFunc("GET "+p+"/ddb/create", c.createPage("ddb", "ddb_create"))
	m.HandleFunc("GET "+p+"/sns/create", c.createPage("sns", "sns_create"))
	m.HandleFunc("GET "+p+"/kinesis/create", c.createPage("kinesis", "kinesis_create"))
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
	m.HandleFunc("POST "+p+"/s3/{bucket}/folder", c.s3NewFolder)
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
	m.HandleFunc("POST "+p+"/sqs/{queue}/receive", c.sqsReceive)                    // a REAL receive, not a peek
	m.HandleFunc("POST "+p+"/sqs/{queue}/visibility", c.sqsChangeVisibility)        // re-hide or release one
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-batch", c.sqsDeleteBatch)           // DeleteMessageBatch
	m.HandleFunc("POST "+p+"/sqs/{queue}/visibility-batch", c.sqsVisibilityBatch)   // ChangeMessageVisibilityBatch
	m.HandleFunc("POST "+p+"/sqs/{queue}/send-batch", c.sqsSendBatch)               // SendMessageBatch
	m.HandleFunc("POST "+p+"/sqs/{queue}/permission", c.sqsAddPermission)           // AddPermission (C-tier)
	m.HandleFunc("POST "+p+"/sqs/{queue}/permission/delete", c.sqsRemovePermission) // RemovePermission (C-tier)
	m.HandleFunc("POST "+p+"/sqs/{queue}/cancel-move", c.sqsCancelMove)             // CancelMessageMoveTask
	m.HandleFunc("POST "+p+"/sqs/{queue}/delete-queue", c.sqsDeleteQueue)

	// DynamoDB.
	m.HandleFunc("GET "+p+"/ddb", c.ddbTables)
	m.HandleFunc("POST "+p+"/ddb/create", c.ddbCreateTable)
	m.HandleFunc("GET "+p+"/ddb/{table}", c.ddbTable)
	m.HandleFunc("POST "+p+"/ddb/{table}/explore", c.ddbExplore) // HTMX partial (scan/query/partiql)
	m.HandleFunc("POST "+p+"/ddb/{table}/put", c.ddbPutItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-item", c.ddbDeleteItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/batch-get", c.ddbBatchGet)       // HTMX partial (BatchGetItem / TransactGetItems)
	m.HandleFunc("POST "+p+"/ddb/{table}/batch-delete", c.ddbBatchDelete) // HTMX partial (BatchWriteItem / TransactWriteItems)
	m.HandleFunc("POST "+p+"/ddb/{table}/update-item", c.ddbUpdateItem)   // HTMX partial (UpdateItem)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-table", c.ddbDeleteTable)
	m.HandleFunc("POST "+p+"/ddb/{table}/ttl", c.ddbSetTTL)
	m.HandleFunc("POST "+p+"/ddb/{table}/add-gsi", c.ddbAddGSI)
	m.HandleFunc("POST "+p+"/ddb/{table}/delete-gsi", c.ddbDeleteGSI)

	// SNS.
	// Kinesis.
	// IAM.
	m.HandleFunc("GET "+p+"/iam", c.iamHome)
	m.HandleFunc("GET "+p+"/iam/policy", c.iamPolicy)
	m.HandleFunc("GET "+p+"/iam/{kind}/{name}", c.iamPrincipal)
	m.HandleFunc("POST "+p+"/iam/simulate", c.iamSimulate)
	m.HandleFunc("POST "+p+"/iam/generate", c.iamGenerate)
	m.HandleFunc("GET "+p+"/iam/create", c.iamCreatePage)
	m.HandleFunc("POST "+p+"/iam/create", c.iamCreate)
	m.HandleFunc("POST "+p+"/iam/policy/delete", c.iamDeletePolicy)
	m.HandleFunc("POST "+p+"/iam/{kind}/{name}/attach", c.iamAttach)
	m.HandleFunc("POST "+p+"/iam/{kind}/{name}/detach", c.iamDetach)
	m.HandleFunc("POST "+p+"/iam/{kind}/{name}/delete", c.iamDeletePrincipal)
	m.HandleFunc("POST "+p+"/iam/{kind}/{name}/inline", c.iamPutInline)
	m.HandleFunc("POST "+p+"/iam/{kind}/{name}/inline/delete", c.iamDeleteInline)
	m.HandleFunc("POST "+p+"/iam/user/{name}/keys", c.iamNewKey)
	m.HandleFunc("POST "+p+"/iam/user/{name}/keys/delete", c.iamDeleteKey)

	// API Gateway.
	m.HandleFunc("GET "+p+"/apigw", c.apigwList)
	m.HandleFunc("GET "+p+"/apigw/{api}", c.apigwAPI)
	m.HandleFunc("POST "+p+"/apigw/{api}/invoke", c.apigwInvoke)

	// CloudFormation.
	m.HandleFunc("GET "+p+"/cfn", c.cfnStacks)
	m.HandleFunc("GET "+p+"/cfn/create", c.createPage("cfn", "cfn_create"))
	m.HandleFunc("POST "+p+"/cfn/create", c.cfnCreate)
	m.HandleFunc("POST "+p+"/cfn/validate", c.cfnValidate) // HTMX partial (ValidateTemplate)
	m.HandleFunc("POST "+p+"/cfn/summary", c.cfnSummary)   // HTMX partial (GetTemplateSummary)
	m.HandleFunc("GET "+p+"/cfn/{stack}", c.cfnStack)
	m.HandleFunc("POST "+p+"/cfn/{stack}/delete", c.cfnDelete)
	m.HandleFunc("POST "+p+"/cfn/{stack}/update", c.cfnUpdate)
	m.HandleFunc("POST "+p+"/cfn/{stack}/changeset/{cs}/execute", c.cfnExecuteCS)
	m.HandleFunc("POST "+p+"/cfn/{stack}/changeset/{cs}/delete", c.cfnDeleteCS)
	m.HandleFunc("POST "+p+"/cfn/{stack}/resource", c.cfnResource) // HTMX partial (DescribeStackResource)

	m.HandleFunc("GET "+p+"/kinesis", c.kinesisStreams)
	m.HandleFunc("POST "+p+"/kinesis/create", c.kinesisCreate)
	m.HandleFunc("GET "+p+"/kinesis/{stream}", c.kinesisStream)
	m.HandleFunc("GET "+p+"/kinesis/{stream}/records", c.kinesisRecords)
	m.HandleFunc("GET "+p+"/kinesis/{stream}/details", c.kinesisDetails)
	m.HandleFunc("GET "+p+"/kinesis/{stream}/tags", c.kinesisTags)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/merge", c.kinesisMerge)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/scale", c.kinesisScale)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/mode", c.kinesisMode)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/encryption", c.kinesisEncryption)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/metrics", c.kinesisMetrics)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/consumers/add", c.kinesisConsumerAdd)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/consumers/del", c.kinesisConsumerDel)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/policy", c.kinesisPolicy)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/records/query", c.kinesisRecordsQuery)
	m.HandleFunc("GET "+p+"/kinesis/{stream}/record", c.kinesisRecord)
	m.HandleFunc("GET "+p+"/kinesis/{stream}/shards/{shard}/depth", c.kinesisShardDepth)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/delete", c.kinesisDelete)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/put", c.kinesisPut)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/split", c.kinesisSplit)
	m.HandleFunc("POST "+p+"/kinesis/{stream}/retention", c.kinesisRetention)

	m.HandleFunc("GET "+p+"/sns", c.snsTopics)
	m.HandleFunc("POST "+p+"/sns/create", c.snsCreateTopic)
	m.HandleFunc("GET "+p+"/sns/{topic}", c.snsTopic)
	m.HandleFunc("POST "+p+"/sns/{topic}/publish", c.snsPublish)
	m.HandleFunc("POST "+p+"/sns/{topic}/subscribe", c.snsSubscribe)
	m.HandleFunc("POST "+p+"/sns/{topic}/confirm", c.snsConfirm)
	m.HandleFunc("POST "+p+"/sns/{topic}/attribute", c.snsSetAttribute)             // SetTopicAttributes (C)
	m.HandleFunc("POST "+p+"/sns/{topic}/permission", c.snsAddPermission)           // AddPermission (C)
	m.HandleFunc("POST "+p+"/sns/{topic}/permission/delete", c.snsRemovePermission) // RemovePermission (C)
	m.HandleFunc("POST "+p+"/sns/{topic}/data-protection", c.snsDataProtection)     // PutDataProtectionPolicy (C) // ConfirmSubscription
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
	m.HandleFunc("GET  "+p+"/eb/{bus}/archive/{archive}", c.ebArchive)              // DescribeArchive
	m.HandleFunc("POST "+p+"/eb/{bus}/archive/{archive}/update", c.ebUpdateArchive) // UpdateArchive
	m.HandleFunc("GET  "+p+"/eb/{bus}/replay/{replay}", c.ebReplayDetail)           // DescribeReplay
	m.HandleFunc("GET  "+p+"/eb/{bus}/detail", c.ebBusDetail)                       // DescribeEventBus
	m.HandleFunc("POST "+p+"/eb/{bus}/rules-by-target", c.ebRulesByTarget)          // ListRuleNamesByTarget
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
	m.HandleFunc("POST "+p+"/lambda/{fn}/code", c.lambdaUpdateCode)          // UpdateFunctionCode
	m.HandleFunc("POST "+p+"/lambda/{fn}/reset-async", c.lambdaResetAsync)   // DeleteFunctionEventInvokeConfig
	m.HandleFunc("POST "+p+"/lambda/layers/publish", c.lambdaLayerPublish)   // PublishLayerVersion
	m.HandleFunc("POST "+p+"/lambda/layers/versions", c.lambdaLayerVersions) // HTMX partial (ListLayerVersions)
	m.HandleFunc("POST "+p+"/lambda/layers/version", c.lambdaLayerVersion)   // HTMX partial (GetLayerVersion)
	m.HandleFunc("POST "+p+"/lambda/layers/find", c.lambdaLayerFind)         // HTMX partial (GetLayerVersionByArn)
	m.HandleFunc("POST "+p+"/lambda/layers/delete", c.lambdaLayerDelete)     // DeleteLayerVersion
	m.HandleFunc("POST "+p+"/lambda/layers/grant", c.lambdaLayerGrant)       // AddLayerVersionPermission
	m.HandleFunc("POST "+p+"/lambda/layers/revoke", c.lambdaLayerRevoke)     // RemoveLayerVersionPermission
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
	m.HandleFunc("POST "+p+"/kms/{key}/description", c.kmsDescription)  // UpdateKeyDescription
	m.HandleFunc("POST "+p+"/kms/random", c.kmsRandom)                  // GenerateRandom — no key needed
	m.HandleFunc("POST "+p+"/kms/{key}/public-key", c.kmsPublicKey)     // GetPublicKey
	m.HandleFunc("POST "+p+"/kms/{key}/reencrypt", c.kmsReEncrypt)      // ReEncrypt
	m.HandleFunc("POST "+p+"/kms/{key}/update-alias", c.kmsUpdateAlias) // UpdateAlias
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
	m.HandleFunc("POST "+p+"/ssm/unlabel", c.ssmUnlabel)        // UnlabelParameterVersion
	m.HandleFunc("POST "+p+"/ssm/path", c.ssmPath)              // GetParametersByPath
	m.HandleFunc("POST "+p+"/ssm/delete-path", c.ssmDeletePath) // DeleteParameters

	// Secrets Manager (names may contain slashes -> query params).
	m.HandleFunc("GET "+p+"/sm", c.smSecrets)
	m.HandleFunc("POST "+p+"/sm/create", c.smCreate)
	m.HandleFunc("POST "+p+"/sm/restore", c.smRestore)
	m.HandleFunc("POST "+p+"/sm/promote", c.smPromote)   // UpdateSecretVersionStage — the rollback
	m.HandleFunc("POST "+p+"/sm/update", c.smUpdateMeta) // UpdateSecret
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
	if r.Host != "" {
		return r.Host
	}
	return "127.0.0.1:4566"
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
// The status stays 400 and no htmx config changes. htmx's default for 4xx is
// swap:false, so this body would never reach the DOM on its own — but
// htmx:beforeSwap fires anyway, even when shouldSwap is false, so shell.js can
// place it next to the control that failed. The header is what tells it to.
// Doing it that way is why none of the ~180 call sites had to change, and why
// an error can never clobber a success target it was not addressed to.
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

func templateFuncs(prefix string) template.FuncMap {
	return template.FuncMap{
		"prefix":    func() string { return prefix },
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
