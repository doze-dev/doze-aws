package sqs

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const sqsXMLNS = "http://queue.amazonaws.com/doc/2012-11-05/"

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

func (p params) intDefault(name string, def int) int {
	if n, ok := p.intp(name); ok {
		return n
	}
	return def
}

// messageAttrs reads custom message attributes (SendMessage).
func (p params) messageAttrs() map[string]Attr {
	if p.form != nil {
		return queryMessageAttrs(p.form, "")
	}
	return jsonMessageAttrs(p.obj["MessageAttributes"])
}

func jsonMessageAttrs(raw json.RawMessage) map[string]Attr {
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
	out := map[string]Attr{}
	for k, v := range in {
		a := Attr{DataType: v.DataType, StringValue: v.StringValue}
		if v.BinaryValue != "" {
			a.BinaryValue, _ = base64.StdEncoding.DecodeString(v.BinaryValue)
		}
		out[k] = a
	}
	return out
}

func queryMessageAttrs(form url.Values, base string) map[string]Attr {
	out := map[string]Attr{}
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("%sMessageAttribute.%d.", base, i)
		name := form.Get(prefix + "Name")
		if name == "" {
			break
		}
		a := Attr{
			DataType:    form.Get(prefix + "Value.DataType"),
			StringValue: form.Get(prefix + "Value.StringValue"),
		}
		if bv := form.Get(prefix + "Value.BinaryValue"); bv != "" {
			a.BinaryValue, _ = base64.StdEncoding.DecodeString(bv)
		}
		out[name] = a
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// queueAttrs reads the CreateQueue/SetQueueAttributes attribute map.
func (p params) queueAttrs() map[string]string {
	if p.form != nil {
		out := map[string]string{}
		for i := 1; ; i++ {
			prefix := fmt.Sprintf("Attribute.%d.", i)
			name := p.form.Get(prefix + "Name")
			if name == "" {
				break
			}
			out[name] = p.form.Get(prefix + "Value")
		}
		return out
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
func (p params) stringList(name string) []string {
	if p.form != nil {
		var out []string
		// AWS query lists are name.1, name.2, …
		for i := 1; ; i++ {
			v := p.form.Get(fmt.Sprintf("%s.%d", name, i))
			if v == "" {
				break
			}
			out = append(out, v)
		}
		return out
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
	ID      string
	Body    string
	Delay   *int
	GroupID string
	DedupID string
	Attrs   map[string]Attr
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
			base := fmt.Sprintf("SendMessageBatchRequestEntry.%d.", i)
			id := p.form.Get(base + "Id")
			if id == "" {
				break
			}
			e := sendEntry{
				ID:      id,
				Body:    p.form.Get(base + "MessageBody"),
				GroupID: p.form.Get(base + "MessageGroupId"),
				DedupID: p.form.Get(base + "MessageDeduplicationId"),
				Attrs:   queryMessageAttrs(p.form, base),
			}
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
	}
	if raw, ok := p.obj["Entries"]; ok {
		_ = json.Unmarshal(raw, &entries)
	}
	for _, e := range entries {
		out = append(out, sendEntry{
			ID: e.ID, Body: e.Body, Delay: e.Delay, GroupID: e.GroupID, DedupID: e.DedupID,
			Attrs: jsonMessageAttrs(e.MessageAttrs),
		})
	}
	return out
}

func (p params) deleteBatchEntries() []delEntry {
	var out []delEntry
	if p.form != nil {
		for i := 1; ; i++ {
			base := fmt.Sprintf("DeleteMessageBatchRequestEntry.%d.", i)
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

// attributeNames reads the requested attribute names (AttributeNames in JSON,
// AttributeName.N in Query).
func (p params) attributeNames() []string {
	if p.form != nil {
		return p.stringList("AttributeName")
	}
	return p.stringList("AttributeNames")
}

func (p params) messageAttributeNames() []string {
	if p.form != nil {
		return p.stringList("MessageAttributeName")
	}
	return p.stringList("MessageAttributeNames")
}

func wants(names []string, name string) bool {
	for _, n := range names {
		if n == "All" || n == ".*" || n == name {
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

type respMeta struct {
	RequestID string `xml:"RequestId" json:"-"`
}

// writeResult encodes an action result in the request's protocol. result may be
// nil for actions with an empty body.
func writeResult(w http.ResponseWriter, req *request, action string, result any) {
	if req.json {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusOK)
		if result == nil {
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	resp := xml.StartElement{
		Name: xml.Name{Local: action + "Response"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: sqsXMLNS}},
	}
	_ = enc.EncodeToken(resp)
	if result != nil {
		_ = enc.EncodeElement(result, xml.StartElement{Name: xml.Name{Local: action + "Result"}})
	}
	_ = enc.EncodeElement(respMeta{RequestID: newID()}, xml.StartElement{Name: xml.Name{Local: "ResponseMetadata"}})
	_ = enc.EncodeToken(resp.End())
	_ = enc.Flush()
}

// writeError encodes an apiError in the request's protocol.
func writeError(w http.ResponseWriter, isJSON bool, err *apiError) {
	if isJSON {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(err.Status)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  err.Code,
			"message": err.Msg,
		})
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(err.Status)
	_, _ = w.Write([]byte(xml.Header))
	type xmlErr struct {
		XMLName   xml.Name `xml:"ErrorResponse"`
		Type      string   `xml:"Error>Type"`
		Code      string   `xml:"Error>Code"`
		Message   string   `xml:"Error>Message"`
		RequestID string   `xml:"RequestId"`
	}
	_ = xml.NewEncoder(w).Encode(xmlErr{
		Type: "Sender", Code: err.Code, Message: err.Msg, RequestID: newID(),
	})
}

// parseRequest decodes either protocol into a request. JSON is signalled by the
// X-Amz-Target header (AmazonSQS.<Action>); otherwise it is the Query protocol.
func parseRequest(r *http.Request) (*request, *apiError) {
	host := r.Host
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		action := target
		if i := strings.LastIndex(target, "."); i >= 0 {
			action = target[i+1:]
		}
		body, _ := io.ReadAll(r.Body)
		var obj map[string]json.RawMessage
		if len(body) > 0 {
			if err := json.Unmarshal(body, &obj); err != nil {
				return nil, &apiError{Code: "InvalidRequest", Status: 400, Msg: "invalid JSON body: " + err.Error()}
			}
		}
		return &request{action: action, json: true, host: host, p: params{obj: obj}}, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, &apiError{Code: "InvalidRequest", Status: 400, Msg: err.Error()}
	}
	action := r.Form.Get("Action")
	if action == "" {
		return nil, &apiError{Code: "MissingAction", Status: 400, Msg: "no Action specified"}
	}
	return &request{action: action, json: false, host: host, p: params{form: r.Form}}, nil
}

// tags reads the queue-tag map: CreateQueue/TagQueue "tags"/"Tags" in the JSON
// protocol, Tag.N.Key/Tag.N.Value in Query.
func (p params) tags() map[string]string {
	if p.form != nil {
		out := map[string]string{}
		for i := 1; ; i++ {
			prefix := fmt.Sprintf("Tag.%d.", i)
			k := p.form.Get(prefix + "Key")
			if k == "" {
				break
			}
			out[k] = p.form.Get(prefix + "Value")
		}
		if len(out) == 0 {
			return nil
		}
		return out
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
