package apigateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/logship"
)

// Stage logging, the two kinds API Gateway writes.
//
// An ACCESS log is one line per request, in the format the stage's
// accessLogSettings gives, with $context variables filled in, to the log
// group the destinationArn names — what a CDK `accessLogDestination` or a
// SAM `AccessLogSetting` asks for. EXECUTION logs are the narrative API
// Gateway writes when a stage's method settings set logging/loglevel to
// INFO or ERROR: the request as it arrived, the integration it went to,
// what came back, and the status it ended with, under
// API-Gateway-Execution-Logs_<apiId>/<stage>; logging/dataTrace adds the
// bodies. Both are written after the response, off the request's path.

// methodSettingDefaults is what AWS reports for a method setting nobody
// has patched, and the base a first patch lands on.
func methodSettingDefaults() map[string]any {
	return map[string]any{
		"metricsEnabled":                         false,
		"loggingLevel":                           "OFF",
		"dataTraceEnabled":                       false,
		"throttlingBurstLimit":                   5000,
		"throttlingRateLimit":                    10000.0,
		"cachingEnabled":                         false,
		"cacheTtlInSeconds":                      300,
		"cacheDataEncrypted":                     false,
		"requireAuthorizationForCacheControl":    true,
		"unauthorizedCacheControlHeaderStrategy": "SUCCEED_WITH_RESPONSE_HEADER",
	}
}

// methodSettingPaths maps the last two segments of an UpdateStage patch
// path to the setting they change and how its value is typed.
var methodSettingPaths = map[string]struct {
	field string
	kind  string // bool | int | float | string
}{
	"logging/loglevel":                               {"loggingLevel", "string"},
	"logging/dataTrace":                              {"dataTraceEnabled", "bool"},
	"metrics/enabled":                                {"metricsEnabled", "bool"},
	"throttling/burstLimit":                          {"throttlingBurstLimit", "int"},
	"throttling/rateLimit":                           {"throttlingRateLimit", "float"},
	"caching/enabled":                                {"cachingEnabled", "bool"},
	"caching/ttlInSeconds":                           {"cacheTtlInSeconds", "int"},
	"caching/dataEncrypted":                          {"cacheDataEncrypted", "bool"},
	"caching/requireAuthorizationForCacheControl":    {"requireAuthorizationForCacheControl", "bool"},
	"caching/unauthorizedCacheControlHeaderStrategy": {"unauthorizedCacheControlHeaderStrategy", "string"},
}

// applyMethodSettingPatch applies a patch whose path is
// /<resource>/<METHOD>/<group>/<name>, where <resource> is * or a path with
// its slashes escaped as ~1. It reports false for a path that is not one.
func applyMethodSettingPatch(st *Stage, op patchOp) (bool, error) {
	segs := strings.Split(strings.TrimPrefix(op.Path, "/"), "/")
	if len(segs) < 4 {
		return false, nil
	}
	setting, ok := methodSettingPaths[strings.Join(segs[len(segs)-2:], "/")]
	if !ok {
		return false, nil
	}
	resource := strings.ReplaceAll(strings.Join(segs[:len(segs)-2], "/"), "~1", "/")
	key := resource
	if i := strings.LastIndex(resource, "/"); i > 0 {
		key = resource[:i] + "/" + strings.ToUpper(resource[i+1:])
	}
	if st.MethodSettings == nil {
		st.MethodSettings = map[string]map[string]any{}
	}
	cur, ok := st.MethodSettings[key]
	if !ok {
		cur = methodSettingDefaults()
		st.MethodSettings[key] = cur
	}
	if op.Op == "remove" {
		cur[setting.field] = methodSettingDefaults()[setting.field]
		return true, nil
	}
	switch setting.kind {
	case "bool":
		cur[setting.field] = op.Value == "true"
	case "int":
		n, err := strconv.Atoi(op.Value)
		if err != nil {
			return true, errBadRequest("Invalid patch value '%s' for path '%s': a number is required", op.Value, op.Path)
		}
		cur[setting.field] = n
	case "float":
		f, err := strconv.ParseFloat(op.Value, 64)
		if err != nil {
			return true, errBadRequest("Invalid patch value '%s' for path '%s': a number is required", op.Value, op.Path)
		}
		cur[setting.field] = f
	default:
		if setting.field == "loggingLevel" {
			v := strings.ToUpper(op.Value)
			if v != "OFF" && v != "ERROR" && v != "INFO" {
				return true, errBadRequest("Invalid patch value '%s' for path '%s': OFF, ERROR or INFO", op.Value, op.Path)
			}
			cur[setting.field] = v
			return true, nil
		}
		cur[setting.field] = op.Value
	}
	return true, nil
}

// settingFor is the method setting that governs one request: the method's
// own, else the stage-wide "*/*", else the defaults.
func (st *Stage) settingFor(resourcePath, method string) map[string]any {
	if ms, ok := st.MethodSettings[resourcePath+"/"+strings.ToUpper(method)]; ok {
		return ms
	}
	if ms, ok := st.MethodSettings["*/*"]; ok {
		return ms
	}
	return methodSettingDefaults()
}

// ---- per-request record ----

// requestLog is what one execute-api request knows about itself, filled in
// as it goes and written once the response is out.
type requestLog struct {
	id         string
	started    time.Time
	apiID      string
	stage      string
	method     string
	resource   string // the resource path the request matched
	path       string
	protocol   string
	sourceIP   string
	userAgent  string
	query      string
	headers    http.Header
	body       []byte
	integType  string
	integURI   string
	integBody  []byte // what went to the integration
	integResp  []byte // what came back
	integStart time.Time
	integEnd   time.Time
	status     int
	respLength int
	errMessage string
	// The Lambda authorizer, when the method has one: its name, the
	// principal it answered, whether the answer came from the cache, and the
	// invoke window.
	authorizer string
	principal  string
	authCached bool
	authStart  time.Time
	authEnd    time.Time
}

// statusWriter captures the status and length the response went out with.
type statusWriter struct {
	http.ResponseWriter
	status int
	length int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.length += n
	return n, err
}

// ---- writing ----

// stageLogs ships access and execution logs for every stage.
type stageLogs struct {
	ship *logship.Shipper
	mu   sync.Mutex
	// streams are minted per (api, stage) per process, the way AWS's
	// hashed stream names group a stage's requests.
	streams map[string]string
}

func newStageLogs(s *Server) *stageLogs {
	return &stageLogs{ship: logship.New("apigateway", s.peers, s.logf), streams: map[string]string{}}
}

func (l *stageLogs) stream(apiID, stage string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := apiID + "/" + stage
	if s, ok := l.streams[k]; ok {
		return s
	}
	var b [8]byte
	rand.Read(b[:])
	s := fmt.Sprintf("%s/%s/%s/%s", apiID, stage, time.Now().UTC().Format("2006-01-02"), hex.EncodeToString(b[:]))
	l.streams[k] = s
	return s
}

// record writes what a finished request produced: an access-log line when
// the stage has a destination, and the execution narrative when its method
// setting asks for one.
func (l *stageLogs) record(st *Stage, rl *requestLog) {
	now := time.Now().UnixMilli()
	if st.AccessLog != nil && st.AccessLog.DestinationARN != "" {
		if group := logGroupFromARN(st.AccessLog.DestinationARN); group != "" {
			l.ship.Put(group, l.stream(rl.apiID, rl.stage), logship.Event{
				Timestamp: now, Message: rl.accessLine(st.AccessLog.Format), RequestID: rl.id,
			})
		}
	}
	setting := st.settingFor(rl.resource, rl.method)
	level, _ := setting["loggingLevel"].(string)
	if level == "OFF" || level == "" {
		return
	}
	if level == "ERROR" && rl.status < 400 && rl.errMessage == "" {
		return
	}
	dataTrace, _ := setting["dataTraceEnabled"].(bool)
	group := "API-Gateway-Execution-Logs_" + rl.apiID + "/" + rl.stage
	stream := l.stream(rl.apiID, rl.stage)
	var events []logship.Event
	for _, line := range rl.executionLines(dataTrace) {
		events = append(events, logship.Event{Timestamp: now, Message: "(" + rl.id + ") " + line, RequestID: rl.id})
	}
	l.ship.Put(group, stream, events...)
}

func (l *stageLogs) close() { l.ship.Close() }

// logGroupFromARN reads the group out of arn:aws:logs:…:log-group:NAME[:*].
func logGroupFromARN(arn string) string {
	_, rest, ok := strings.Cut(arn, ":log-group:")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(rest, ":*")
}

// accessLine fills a $context format. The variables API Gateway documents
// that a local request can answer are filled; anything else is "-", which
// is what AWS writes for an absent value.
func (rl *requestLog) accessLine(format string) string {
	if format == "" {
		format = `$context.identity.sourceIp - - [$context.requestTime] "$context.httpMethod $context.resourcePath $context.protocol" $context.status $context.responseLength $context.requestId`
	}
	elapsed := time.Since(rl.started).Milliseconds()
	integ := int64(0)
	if !rl.integEnd.IsZero() {
		integ = rl.integEnd.Sub(rl.integStart).Milliseconds()
	}
	vars := map[string]string{
		"requestId":          rl.id,
		"extendedRequestId":  rl.id,
		"apiId":              rl.apiID,
		"stage":              rl.stage,
		"httpMethod":         rl.method,
		"resourcePath":       rl.resource,
		"path":               "/" + rl.stage + rl.path,
		"protocol":           rl.protocol,
		"status":             strconv.Itoa(rl.status),
		"responseLength":     strconv.Itoa(rl.respLength),
		"responseLatency":    strconv.FormatInt(elapsed, 10),
		"integrationLatency": strconv.FormatInt(integ, 10),
		"requestTime":        rl.started.UTC().Format("02/Jan/2006:15:04:05 +0000"),
		"requestTimeEpoch":   strconv.FormatInt(rl.started.UnixMilli(), 10),
		"domainName":         rl.apiID + ".execute-api.localhost",
		"identity.sourceIp":  rl.sourceIP,
		"identity.userAgent": rl.userAgent,
		"error.message":      rl.errMessage,
		"error.messageString": func() string {
			if rl.errMessage == "" {
				return ""
			}
			return strconv.Quote(rl.errMessage)
		}(),
		"integration.status":            strconv.Itoa(rl.status),
		"integration.latency":           strconv.FormatInt(integ, 10),
		"integration.integrationStatus": strconv.Itoa(rl.status),
	}
	var out strings.Builder
	for i := 0; i < len(format); {
		if strings.HasPrefix(format[i:], "$context.") {
			j := i + len("$context.")
			for j < len(format) && (format[j] == '.' || format[j] == '_' || format[j] >= 'a' && format[j] <= 'z' || format[j] >= 'A' && format[j] <= 'Z' || format[j] >= '0' && format[j] <= '9') {
				j++
			}
			// A trailing dot belongs to the sentence, not the variable.
			name := strings.TrimRight(format[i+len("$context."):j], ".")
			j = i + len("$context.") + len(name)
			if v, ok := vars[name]; ok && v != "" {
				out.WriteString(v)
			} else {
				out.WriteString("-")
			}
			i = j
			continue
		}
		out.WriteByte(format[i])
		i++
	}
	return out.String()
}

// executionLines is the narrative API Gateway writes at INFO, in its order
// and its words; bodies only with data trace.
func (rl *requestLog) executionLines(dataTrace bool) []string {
	var lines []string
	lines = append(lines, "Extended Request Id: "+rl.id)
	lines = append(lines, "Verifying Usage Plan for request: "+rl.id+". API Key:  API Stage: "+rl.apiID+"/"+rl.stage)
	lines = append(lines, "API Key  authorized because method '"+rl.method+" "+rl.resource+"' does not require API Key. Request will not contribute to throttle or quota limits")
	lines = append(lines, "Usage Plan check succeeded for API Key  and API Stage "+rl.apiID+"/"+rl.stage)
	if rl.authorizer != "" {
		switch {
		case rl.authCached:
			lines = append(lines, "Using cached authorizer result for authorizer "+rl.authorizer)
		case !rl.authEnd.IsZero():
			lines = append(lines, fmt.Sprintf("Invoking authorizer %s (%d ms)", rl.authorizer, rl.authEnd.Sub(rl.authStart).Milliseconds()))
		}
		if rl.principal != "" {
			lines = append(lines, "Authorizer result: principalId "+rl.principal)
		}
	}
	lines = append(lines, "Starting execution for request: "+rl.id)
	lines = append(lines, "HTTP Method: "+rl.method+", Resource Path: "+rl.resource)
	lines = append(lines, "Method request path: "+jsonOf(pathParamsOf(rl)))
	lines = append(lines, "Method request query string: "+jsonOf(rl.queryMap()))
	lines = append(lines, "Method request headers: "+jsonOf(headerMap(rl.headers)))
	if dataTrace {
		lines = append(lines, "Method request body before transformations: "+string(rl.body))
	}
	if rl.integURI != "" {
		lines = append(lines, "Endpoint request URI: "+rl.integURI)
		if dataTrace && len(rl.integBody) > 0 {
			lines = append(lines, "Endpoint request body after transformations: "+truncateBytes(rl.integBody, 4096))
		}
		lines = append(lines, "Sending request to "+rl.integURI)
	}
	if !rl.integEnd.IsZero() {
		lines = append(lines, fmt.Sprintf("Received response. Status: %d, Integration latency: %d ms", rl.status, rl.integEnd.Sub(rl.integStart).Milliseconds()))
		if dataTrace && len(rl.integResp) > 0 {
			lines = append(lines, "Endpoint response body before transformations: "+truncateBytes(rl.integResp, 4096))
		}
	}
	if rl.errMessage != "" {
		lines = append(lines, "Execution failed due to configuration error: "+rl.errMessage)
	} else {
		lines = append(lines, "Successfully completed execution")
	}
	lines = append(lines, fmt.Sprintf("Method completed with status: %d", rl.status))
	return lines
}

func (rl *requestLog) queryMap() map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(rl.query, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func pathParamsOf(rl *requestLog) map[string]string {
	return map[string]string{"path": rl.path}
}

func headerMap(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = strings.Join(v, ",")
	}
	return out
}

func jsonOf(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
