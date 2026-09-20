package console

import (
	"encoding/json"
	"html/template"
	"strings"
)

// MsgSummary is the display form of a message body. The console generates the
// AWS event shapes it delivers (S3 notifications, SNS envelopes, EventBridge
// events), so it can recognize them and lead with what happened instead of a
// wall of wire JSON.
type MsgSummary struct {
	Kind     string        // chip label: "s3 event", "sns · orders", "eb event", "json", "text"
	Svc      string        // service color for the chip; "" = neutral
	Line     string        // the one-line summary
	Note     string        // envelope/meta context shown in the expanded view
	BodyHTML template.HTML // pretty, syntax-lit body for the expanded view
	Raw      string        // the display body as plain text (copy target)
}

// summarize recognizes a message body and returns its display form.
func summarize(body string) MsgSummary {
	trimmed := strings.TrimSpace(body)
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		// Not a JSON object — an array still pretty-prints; anything else is text.
		var arr []any
		if json.Unmarshal([]byte(trimmed), &arr) == nil {
			return MsgSummary{Kind: "json", Line: firstLine(trimmed, 140), BodyHTML: litJSON(trimmed), Raw: pretty(trimmed)}
		}
		return MsgSummary{Kind: "text", Line: firstLine(trimmed, 140), BodyHTML: template.HTML(template.HTMLEscapeString(trimmed)), Raw: trimmed}
	}

	str := func(k string) string { s, _ := m[k].(string); return s }

	// S3 event notification: {"Records":[{"eventSource":"aws:s3",…}]}
	if recs, ok := m["Records"].([]any); ok && len(recs) > 0 {
		if r0, ok := recs[0].(map[string]any); ok {
			if src, _ := r0["eventSource"].(string); src == "aws:s3" {
				name, _ := r0["eventName"].(string)
				bucket, key, size := "", "", float64(-1)
				if s3, ok := r0["s3"].(map[string]any); ok {
					if b, ok := s3["bucket"].(map[string]any); ok {
						bucket, _ = b["name"].(string)
					}
					if o, ok := s3["object"].(map[string]any); ok {
						key, _ = o["key"].(string)
						key = unescapeKeyDisplay(key)
						size, _ = o["size"].(float64)
					}
				}
				line := name + " — " + bucket + " / " + key
				if size >= 0 {
					line += " · " + humanBytes(int64(size))
				}
				if n := len(recs); n > 1 {
					line += " (+" + plural(n-1, "more record") + ")"
				}
				return MsgSummary{Kind: "s3 event", Svc: "s3", Line: line, BodyHTML: litJSON(trimmed), Raw: pretty(trimmed)}
			}
		}
	}

	// SNS envelope: {"Type":"Notification","TopicArn":…,"Message":…}
	if str("Type") == "Notification" && str("TopicArn") != "" {
		topic := arnLeaf(str("TopicArn"))
		inner := str("Message")
		note := "via topic " + topic
		if s := str("Subject"); s != "" {
			note += " · subject “" + s + "”"
		}
		if ts := str("Timestamp"); ts != "" {
			note += " · " + ts
		}
		return MsgSummary{
			Kind: "sns · " + topic, Svc: "sns",
			Line: firstLine(inner, 140), Note: note,
			BodyHTML: litJSON(inner), Raw: pretty(inner),
		}
	}

	// EventBridge event: {"source":…,"detail-type":…,"detail":…}
	if str("source") != "" && str("detail-type") != "" {
		return MsgSummary{
			Kind: "eb event", Svc: "eb",
			Line:     str("source") + " · " + str("detail-type"),
			BodyHTML: litJSON(trimmed), Raw: pretty(trimmed),
		}
	}

	return MsgSummary{Kind: "json", Line: firstLine(trimmed, 140), BodyHTML: litJSON(trimmed), Raw: pretty(trimmed)}
}

// litJSON pretty-prints and syntax-lights a JSON value for display; non-JSON
// input falls back to escaped plain text.
func litJSON(s string) template.HTML {
	p := pretty(s)
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(highlightJSON(p))
}

func pretty(s string) string {
	if p := prettyJSON(s); p != "" {
		return p
	}
	return s
}

// firstLine compacts a value to a single line and clips it for the summary row.
func firstLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		// clip on a rune boundary
		for max > 0 && s[max]&0xC0 == 0x80 {
			max--
		}
		s = s[:max] + "…"
	}
	if s == "" {
		return "(empty message)"
	}
	return s
}

// unescapeKeyDisplay undoes the URL-encoding S3 applies to keys inside event
// notifications ("invoices%2Freceipt.json" → "invoices/receipt.json").
func unescapeKeyDisplay(k string) string {
	return strings.NewReplacer("%2F", "/", "%2f", "/", "+", " ").Replace(k)
}
