package sqs

// The SQS wire codec: one decoded request shape over the two protocols SQS
// speaks — AWS JSON 1.0 (modern SDKs) and the legacy Query protocol (aws-sdk-go
// v1-era clients). The protocol envelopes themselves come from the shared
// layers: internal/awsjson for JSON, internal/awsquery for Query/XML.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/doze-dev/doze-aws/internal/modelcheck"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/awsquery"
)

const sqsXMLNS = "http://queue.amazonaws.com/doc/2012-11-05/"

// qapi and japi render the two protocols' response envelopes.
var (
	qapi = awsquery.API{XMLNS: sqsXMLNS}
	japi = awsjson.API{TargetPrefix: "AmazonSQS", JSONVersion: "1.0"}
)

// request is one decoded SQS API call, protocol-agnostic from the handlers'
// view. params reads scalars and the nested shapes (message attributes, queue
// attributes, batch entries) from whichever wire format was used.
type request struct {
	action string
	json   bool
	host   string // client-facing Host header, for building queue URLs
	p      params
}

// params abstracts the two wire formats: AWS JSON 1.0 (a parsed object) and the
// legacy Query protocol (form values).
type params struct {
	obj  map[string]json.RawMessage // JSON protocol
	form url.Values                 // Query protocol
}

func (p params) str(name string) string {
	if p.form != nil {
		return p.form.Get(name)
	}
	raw, ok := p.obj[name]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Numbers/bools arrive un-quoted in JSON; return their literal text.
	return strings.Trim(string(raw), `"`)
}

func (p params) intp(name string) (int, bool) {
	s := p.str(name)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// intParam reads an integer request parameter that has a stated range.
//
// A parameter that is not there is not present, and the caller applies its
// default. One that is there is held to the range, and one that is not a whole
// number is refused rather than read as absent: intp reads "banana" as
// "not given", which let a wrong value stand in for the default behind a 200.
//
// doze: the ranges are the API reference's, parameter by parameter (SQS's
// service model states none).
func (p params) intParam(name string, lo, hi int) (n int, present bool, err *apiError) {
	s := p.str(name)
	if s == "" {
		return 0, false, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil {
		return 0, true, errInvalid("Value " + s + " for parameter " + name + " is invalid. Reason: Must be a whole number.")
	}
	if n < lo || n > hi {
		return n, true, errInvalid("Value " + s + " for parameter " + name + " is invalid. Reason: Must be between " +
			strconv.Itoa(lo) + " and " + strconv.Itoa(hi) + ", if provided.")
	}
	return n, true, nil
}

func (p params) intDefault(name string, def int) int {
	if n, ok := p.intp(name); ok {
		return n
	}
	return def
}

// messageAttrs reads custom message attributes (SendMessage).
func (p params) messageAttrs() map[string]attr {
	if p.form != nil {
		return fromQueryAttrs(awsquery.MessageAttrs(p.form, "MessageAttribute"))
	}
	return jsonMessageAttrs(p.obj["MessageAttributes"])
}

// fromQueryAttrs converts the shared Query codec's attribute shape into SQS's
// persisted Attr.
func fromQueryAttrs(in map[string]awsquery.MessageAttr) map[string]attr {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]attr, len(in))
	for k, a := range in {
		out[k] = attr{DataType: a.DataType, StringValue: a.StringValue, BinaryValue: a.BinaryValue}
	}
	return out
}

func jsonMessageAttrs(raw json.RawMessage) map[string]attr {
	if len(raw) == 0 {
		return nil
	}
	var in map[string]struct {
		DataType    string `json:"DataType"`
		StringValue string `json:"StringValue"`
		BinaryValue string `json:"BinaryValue"` // base64 in the JSON protocol
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	out := map[string]attr{}
	for k, v := range in {
		a := attr{DataType: v.DataType, StringValue: v.StringValue}
		if v.BinaryValue != "" {
			a.BinaryValue, _ = base64.StdEncoding.DecodeString(v.BinaryValue)
		}
		out[k] = a
	}
	return out
}

// queueAttrs reads the CreateQueue/SetQueueAttributes attribute map.
func (p params) queueAttrs() map[string]string {
	if p.form != nil {
		return awsquery.PairMap(p.form, "Attribute", "Name", "Value")
	}
	if raw, ok := p.obj["Attributes"]; ok {
		var m map[string]string
		if json.Unmarshal(raw, &m) == nil {
			return m
		}
	}
	return nil
}

// stringList reads a repeated string parameter (e.g. AttributeNames).
// queryListNames maps the Query protocol's wire name of a list member to
// the member's model name.
var queryListNames = map[string]string{"AWSAccountId": "AWSAccountIds", "ActionName": "Actions"}

// queryMemberNames is every other member the Query protocol spells under a
// different name than the model gives it, which is what a flattened list or
// map's xmlName is (doc: the model's xmlName traits). The model's constraints
// are written against the model's names, so a request is aliased to them before
// they are walked; otherwise a valid Query request reads as missing its
// required member.
var queryMemberNames = map[string][]string{
	"Tag":                            {"Tags", "tags"},
	"TagKey":                         {"TagKeys"},
	"Attribute":                      {"Attributes"},
	"AttributeName":                  {"AttributeNames", "MessageSystemAttributeNames"},
	"MessageAttributeName":           {"MessageAttributeNames"},
	"MessageAttribute":               {"MessageAttributes"},
	"MessageSystemAttribute":         {"MessageSystemAttributes"},
	"SendMessageBatchRequestEntry":   {"Entries"},
	"DeleteMessageBatchRequestEntry": {"Entries"},
	"ChangeMessageVisibilityBatchRequestEntry": {"Entries"},
}

func (p params) stringList(name string) []string {
	if p.form != nil {
		// AWS query lists are name.1, name.2, … — under the wire name, where
		// the model name differs.
		for wire, member := range queryListNames {
			if member == name {
				if vals := awsquery.Members(p.form, wire, true); len(vals) > 0 {
					return vals
				}
			}
		}
		return awsquery.Members(p.form, name, true)
	}
	if raw, ok := p.obj[name]; ok {
		var out []string
		if json.Unmarshal(raw, &out) == nil {
			return out
		}
	}
	return nil
}

// sendEntry is one SendMessageBatch entry. Delay is nil when unset (use default).
type sendEntry struct {
	ID       string
	Body     string
	Delay    *int
	GroupID  string
	DedupID  string
	Attrs    map[string]attr
	SysAttrs map[string]string
	// Invalid is a refusal of this entry alone, decided while reading it.
	Invalid *apiError
}

// delEntry is one DeleteMessageBatch entry.
type delEntry struct {
	ID            string
	ReceiptHandle string
}

func (p params) sendBatchEntries() []sendEntry {
	var out []sendEntry
	if p.form != nil {
		for i := 1; ; i++ {
			base := "SendMessageBatchRequestEntry." + strconv.Itoa(i) + "."
			id := p.form.Get(base + "Id")
			if id == "" {
				break
			}
			e := sendEntry{
				ID:      id,
				Body:    p.form.Get(base + "MessageBody"),
				GroupID: p.form.Get(base + "MessageGroupId"),
				DedupID: p.form.Get(base + "MessageDeduplicationId"),
				Attrs:   fromQueryAttrs(awsquery.MessageAttrs(p.form, base+"MessageAttribute")),
			}
			e.SysAttrs, e.Invalid = querySystemAttrs(p.form, base+"MessageSystemAttribute")
			if d := p.form.Get(base + "DelaySeconds"); d != "" {
				if n, err := strconv.Atoi(d); err == nil {
					e.Delay = &n
				}
			}
			out = append(out, e)
		}
		return out
	}
	var entries []struct {
		ID           string          `json:"Id"`
		Body         string          `json:"MessageBody"`
		Delay        *int            `json:"DelaySeconds"`
		GroupID      string          `json:"MessageGroupId"`
		DedupID      string          `json:"MessageDeduplicationId"`
		MessageAttrs json.RawMessage `json:"MessageAttributes"`
		SysAttrs     json.RawMessage `json:"MessageSystemAttributes"`
	}
	if raw, ok := p.obj["Entries"]; ok {
		_ = json.Unmarshal(raw, &entries)
	}
	for _, e := range entries {
		sys, bad := jsonSystemAttrs(e.SysAttrs)
		out = append(out, sendEntry{
			ID: e.ID, Body: e.Body, Delay: e.Delay, GroupID: e.GroupID, DedupID: e.DedupID,
			Attrs: jsonMessageAttrs(e.MessageAttrs), SysAttrs: sys, Invalid: bad,
		})
	}
	return out
}

func (p params) deleteBatchEntries() []delEntry {
	var out []delEntry
	if p.form != nil {
		for i := 1; ; i++ {
			base := "DeleteMessageBatchRequestEntry." + strconv.Itoa(i) + "."
			id := p.form.Get(base + "Id")
			if id == "" {
				break
			}
			out = append(out, delEntry{ID: id, ReceiptHandle: p.form.Get(base + "ReceiptHandle")})
		}
		return out
	}
	var entries []struct {
		ID            string `json:"Id"`
		ReceiptHandle string `json:"ReceiptHandle"`
	}
	if raw, ok := p.obj["Entries"]; ok {
		_ = json.Unmarshal(raw, &entries)
	}
	for _, e := range entries {
		out = append(out, delEntry{ID: e.ID, ReceiptHandle: e.ReceiptHandle})
	}
	return out
}

// visEntry is one ChangeMessageVisibilityBatch entry.
type visEntry struct {
	ID            string
	ReceiptHandle string
	Timeout       int
}

func (p params) visibilityBatchEntries() []visEntry {
	var out []visEntry
	if p.form != nil {
		for i := 1; ; i++ {
			base := "ChangeMessageVisibilityBatchRequestEntry." + strconv.Itoa(i) + "."
			id := p.form.Get(base + "Id")
			if id == "" {
				break
			}
			timeout, _ := strconv.Atoi(p.form.Get(base + "VisibilityTimeout"))
			out = append(out, visEntry{
				ID: id, ReceiptHandle: p.form.Get(base + "ReceiptHandle"), Timeout: timeout,
			})
		}
		return out
	}
	var entries []struct {
		ID                string `json:"Id"`
		ReceiptHandle     string `json:"ReceiptHandle"`
		VisibilityTimeout int    `json:"VisibilityTimeout"`
	}
	if raw, ok := p.obj["Entries"]; ok {
		_ = json.Unmarshal(raw, &entries)
	}
	for _, e := range entries {
		out = append(out, visEntry{ID: e.ID, ReceiptHandle: e.ReceiptHandle, Timeout: e.VisibilityTimeout})
	}
	return out
}

// attributeNames reads the requested message system attribute names.
//
// Both spellings are accepted because AWS renamed this field: AttributeNames
// is deprecated in favour of MessageSystemAttributeNames, and current SDKs send
// the new one. Reading only the old name meant a modern aws-sdk-go-v2 client
// asking for "All" got nothing back — no ApproximateReceiveCount, no
// SentTimestamp — while an older client got everything, which is the kind of
// divergence that looks like a client bug for a long time before anyone
// suspects the emulator.
func (p params) attributeNames() []string {
	if p.form != nil {
		if n := p.stringList("MessageSystemAttributeName"); len(n) > 0 {
			return n
		}
		return p.stringList("AttributeName")
	}
	if n := p.stringList("MessageSystemAttributeNames"); len(n) > 0 {
		return n
	}
	return p.stringList("AttributeNames")
}

func (p params) messageAttributeNames() []string {
	if p.form != nil {
		return p.stringList("MessageAttributeName")
	}
	return p.stringList("MessageAttributeNames")
}

// wants reports whether a ReceiveMessage name selection takes an attribute:
// the name itself, "All" or ".*" for everything, or "prefix.*" for every name
// that starts with the prefix (doc: the model's MessageAttributeNames).
func wants(names []string, name string) bool {
	for _, n := range names {
		if n == "All" || n == ".*" || n == name {
			return true
		}
		if prefix, ok := strings.CutSuffix(n, ".*"); ok && prefix != "" && strings.HasPrefix(name, prefix+".") {
			return true
		}
	}
	return false
}

// queueNameFromURL returns the queue name (last path segment) of a queue URL.
func queueNameFromURL(qurl string) string {
	if i := strings.LastIndex(qurl, "/"); i >= 0 {
		return qurl[i+1:]
	}
	return qurl
}

// ---- response encoding ----

// writeResult encodes an action result in the request's protocol. result may be
// nil for actions with an empty body.
func writeResult(w http.ResponseWriter, req *request, action string, result any) {
	if req.json {
		japi.Write(w, result)
		return
	}
	qapi.WriteResult(w, action, result)
}

// jsonErrorType maps an internal error code to the modern awsjson error shape
// name aws-sdk-go-v2 matches on. The Query protocol keeps the legacy code; only
// the JSON protocol needs the shape name so errors.As(err, &types.XxX{}) works.
func jsonErrorType(code string) string {
	switch code {
	case "AWS.SimpleQueueService.NonExistentQueue":
		return "QueueDoesNotExist"
	case "AWS.SimpleQueueService.QueueDeletedRecently":
		return "QueueDeletedRecently"
	case "AWS.SimpleQueueService.QueueNameExists", "QueueAlreadyExists":
		return "QueueNameExists"
	case "AWS.SimpleQueueService.PurgeQueueInProgress":
		return "PurgeQueueInProgress"
	case "AWS.SimpleQueueService.EmptyBatchRequest",
		"AWS.SimpleQueueService.TooManyEntriesInBatchRequest",
		"AWS.SimpleQueueService.BatchEntryIdsNotDistinct",
		"AWS.SimpleQueueService.InvalidBatchEntryId":
		return strings.TrimPrefix(code, "AWS.SimpleQueueService.")
	default:
		return code
	}
}

// errorSenderClass classifies a fault as Sender (4xx) or Receiver (5xx), the way
// real AWS does — not a hardcoded "Sender".
func errorSenderClass(status int) string {
	if status >= 500 {
		return "Receiver"
	}
	return "Sender"
}

// writeError encodes an apiError in the request's protocol.
func writeError(w http.ResponseWriter, isJSON bool, err *apiError) {
	e := *err
	e.SenderFault = err.Status < 500
	// The status is the error shape's own, on both wires: the model gives
	// these shapes httpError 404 and 403, and awsQueryError says the same for
	// the Query protocol. The JSON wire answered 400 for all of them on the
	// strength of a recollection; the model is what is on disk.
	switch e.Code {
	case "ReceiptHandleIsInvalid", "ResourceNotFoundException", "InvalidAddress":
		e.Status = 404
	case "AWS.SimpleQueueService.PurgeQueueInProgress", "OverLimit":
		e.Status = 403
	}
	if isJSON {
		// Query-compatible services carry the legacy code in this header while
		// __type holds the modern shape name.
		w.Header().Set("x-amzn-query-error", err.Code+";"+errorSenderClass(e.Status))
		e.Code = jsonErrorType(err.Code)
		japi.WriteError(w, &e)
		return
	}
	qapi.WriteError(w, &e)
}

// parseRequest decodes either protocol into a request. JSON is signalled by the
// X-Amz-Target header (AmazonSQS.<Action>); otherwise it is the Query protocol.
func parseRequest(r *http.Request) (*request, *apiError) {
	host := r.Host
	if r.Header.Get("X-Amz-Target") != "" {
		action, aerr := japi.Action(r)
		if aerr != nil {
			return nil, aerr
		}
		var obj map[string]json.RawMessage
		if aerr := awsjson.DecodeBody(r, &obj); aerr != nil {
			return nil, aerr
		}
		return &request{action: action, json: true, host: host, p: params{obj: obj}}, nil
	}
	vals, err := awsquery.Params(r)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	action := vals.Get("Action")
	if action == "" {
		return nil, &apiError{Code: "MissingAction", Status: 400, Message: "no Action specified", SenderFault: true}
	}
	return &request{action: action, json: false, host: host, p: params{form: vals}}, nil
}

// tags reads the queue-tag map: CreateQueue/TagQueue "tags"/"Tags" in the JSON
// protocol, Tag.N.Key/Tag.N.Value in Query.
func (p params) tags() map[string]string {
	if p.form != nil {
		return awsquery.PairMap(p.form, "Tag", "Key", "Value")
	}
	// CreateQueue uses "tags" (lowercase), TagQueue uses "Tags".
	for _, key := range []string{"tags", "Tags"} {
		if raw, ok := p.obj[key]; ok {
			var m map[string]string
			if json.Unmarshal(raw, &m) == nil && len(m) > 0 {
				return m
			}
		}
	}
	return nil
}

// tagKeys reads UntagQueue's key list (TagKeys in JSON, TagKey.N in Query).
func (p params) tagKeys() []string {
	if p.form != nil {
		return p.stringList("TagKey")
	}
	return p.stringList("TagKeys")
}

// systemAttrs reads SendMessage's MessageSystemAttribute entries.
//
// AWS defines exactly one today, AWSTraceHeader, and doze-aws accepts only
// that one for the same reason it validates everything else: taking a name AWS
// would refuse means code that works here fails on deploy. The values live
// apart from user attributes because SQS excludes them from
// MD5OfMessageAttributes and returns them with SentTimestamp and friends.
func (p params) systemAttrs() (map[string]string, *apiError) {
	// Query protocol, in either spelling of a map: MessageSystemAttribute.N.Name
	// (flattened, which the model's xmlFlattened asks for) and
	// MessageSystemAttribute.entry.N.Name.
	if p.form != nil {
		return querySystemAttrs(p.form, "MessageSystemAttribute")
	}
	// JSON protocol: {"MessageSystemAttributes":{"AWSTraceHeader":{"StringValue":..}}}
	return jsonSystemAttrs(p.obj["MessageSystemAttributes"])
}

// checkSystemAttr holds a message system attribute to what the model allows:
// "the only supported message system attribute is AWSTraceHeader. Its type must
// be String". Any other name, and any other type, used to be dropped without a
// word, so a typo sent a message with no trace on it and answered 200.
func checkSystemAttr(name, dataType string) *apiError {
	if name != sysAttrTraceHeader {
		return errInvalid("Message system attribute name '" + name + "' is invalid. The only supported message system attribute is " + sysAttrTraceHeader + ".")
	}
	if dataType != "String" {
		return errInvalid("Message system attribute " + sysAttrTraceHeader + " must have the data type String, not '" + dataType + "'.")
	}
	return nil
}

// querySystemAttrs reads the AWSTraceHeader out of a Query request's system
// attribute list under prefix, in either spelling of a map.
func querySystemAttrs(form url.Values, prefix string) (map[string]string, *apiError) {
	var out map[string]string
	for _, spelling := range []string{prefix + ".%d.", prefix + ".entry.%d."} {
		for i := 1; ; i++ {
			base := fmt.Sprintf(spelling, i)
			name := form.Get(base + "Name")
			if name == "" {
				break
			}
			if aerr := checkSystemAttr(name, form.Get(base+"Value.DataType")); aerr != nil {
				return nil, aerr
			}
			if v := form.Get(base + "Value.StringValue"); v != "" {
				if out == nil {
					out = map[string]string{}
				}
				out[name] = v
			}
		}
	}
	return out, nil
}

// jsonSystemAttrs reads the AWSTraceHeader out of a JSON MessageSystemAttributes
// member.
func jsonSystemAttrs(raw json.RawMessage) (map[string]string, *apiError) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]struct {
		DataType    string `json:"DataType"`
		StringValue string `json:"StringValue"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return nil, nil
	}
	var out map[string]string
	for k, v := range m {
		if aerr := checkSystemAttr(k, v.DataType); aerr != nil {
			return nil, aerr
		}
		if v.StringValue != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v.StringValue
		}
	}
	return out, nil
}

// hasFormPrefix reports whether a Query request carries any key under prefix.
func (p params) hasFormPrefix(prefix string) bool {
	for k := range p.form {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// sysAttrTraceHeader is the one message system attribute AWS defines.
const sysAttrTraceHeader = "AWSTraceHeader"

// asMap renders the request's parameters as the nested shape the model's
// constraint paths describe, whichever protocol carried them.
//
// SQS is the only service here that speaks both, so this is where the two
// converge: the JSON body is already that shape, and the Query form is
// un-flattened into it. Validating one and not the other would leave half the
// clients unchecked — and the Query half is the older, likelier-to-be-wrong one.
func (p params) asMap() map[string]any {
	if p.form != nil {
		m := modelcheck.FromQuery(p.form)
		// The Query protocol spells AddPermission's lists by their wire
		// names, AWSAccountId.N and ActionName.N; the model, and the JSON
		// protocol, say AWSAccountIds and Actions.
		for wire, member := range queryListNames {
			if v, ok := m[wire]; ok {
				if _, present := m[member]; !present {
					m[member] = v
				}
			}
		}
		for wire, members := range queryMemberNames {
			if v, ok := m[wire]; ok {
				for _, member := range members {
					if _, present := m[member]; !present {
						m[member] = v
					}
				}
			}
		}
		// The generic walk leaves a flattened map as a list of pairs, because
		// SQS spells its maps without the `.entry.` marker (Tag.N, Attribute.N),
		// which would check none of the attributes. Render the members whole,
		// in the shape the JSON protocol's body already has, so one table
		// judges both wires. (The walk kept one element of each list when this
		// was written; it keeps them all since foundation B13, so the list half
		// below now only applies the wire names.)
		p.queryShapes(m)
		return m
	}
	out := make(map[string]any, len(p.obj))
	for k, raw := range p.obj {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out[k] = v
		}
	}
	return out
}

// queryShapes overwrites, in m, every list and map member a Query request
// spells in flattened form with its whole JSON-shaped equivalent.
func (p params) queryShapes(m map[string]any) {
	lists := map[string][]string{
		"AWSAccountId":         {"AWSAccountIds"},
		"ActionName":           {"Actions"},
		"AttributeName":        {"AttributeNames", "MessageSystemAttributeNames"},
		"MessageAttributeName": {"MessageAttributeNames"},
		"TagKey":               {"TagKeys"},
	}
	for wire, members := range lists {
		vals := awsquery.Members(p.form, wire, true)
		if len(vals) == 0 {
			continue
		}
		list := make([]any, len(vals))
		for i, v := range vals {
			list[i] = v
		}
		for _, member := range members {
			m[member] = list
		}
	}
	maps := []struct {
		wire, key string
		members   []string
	}{
		{"Tag", "Key", []string{"Tags", "tags"}},
		{"Attribute", "Name", []string{"Attributes"}},
		{"MessageAttribute", "Name", []string{"MessageAttributes"}},
		{"MessageSystemAttribute", "Name", []string{"MessageSystemAttributes"}},
	}
	for _, mp := range maps {
		if out := queryFlatMap(p.form, mp.wire, mp.key); out != nil {
			for _, member := range mp.members {
				m[member] = out
			}
		}
	}
	for _, wire := range []string{"SendMessageBatchRequestEntry", "DeleteMessageBatchRequestEntry", "ChangeMessageVisibilityBatchRequestEntry"} {
		if entries := queryEntries(p.form, wire); entries != nil {
			m["Entries"] = entries
		}
	}
}

// queryFlatMap reads a flattened map: prefix.N.<keyField> is the key and
// prefix.N.Value, or prefix.N.Value.<member>, is its value.
func queryFlatMap(form url.Values, prefix, keyField string) map[string]any {
	var out map[string]any
	for i := 1; ; i++ {
		base := prefix + "." + strconv.Itoa(i) + "."
		key := form.Get(base + keyField)
		if key == "" {
			return out
		}
		sub := url.Values{}
		for k, v := range form {
			if strings.HasPrefix(k, base+"Value") {
				sub[strings.TrimPrefix(k, base)] = v
			}
		}
		if out == nil {
			out = map[string]any{}
		}
		out[key] = modelcheck.FromQuery(sub)["Value"]
	}
}

// queryEntries reads a batch's flattened entries whole: every element, each
// with its own attribute maps in the JSON spelling.
func queryEntries(form url.Values, prefix string) []any {
	var out []any
	for i := 1; ; i++ {
		base := prefix + "." + strconv.Itoa(i) + "."
		sub := url.Values{}
		for k, v := range form {
			if strings.HasPrefix(k, base) {
				sub[strings.TrimPrefix(k, base)] = v
			}
		}
		if len(sub) == 0 {
			return out
		}
		elem := modelcheck.FromQuery(sub)
		delete(elem, "MessageAttribute")
		delete(elem, "MessageSystemAttribute")
		if attrs := queryFlatMap(sub, "MessageAttribute", "Name"); attrs != nil {
			elem["MessageAttributes"] = attrs
		}
		if attrs := queryFlatMap(sub, "MessageSystemAttribute", "Name"); attrs != nil {
			elem["MessageSystemAttributes"] = attrs
		}
		out = append(out, elem)
	}
}
