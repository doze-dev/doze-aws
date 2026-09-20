package console

import (
	"encoding/json"
	"html/template"
	"strings"
)

// maskSentinel stands in for a masked string value during rendering; it is a
// private-use rune so it never collides with real content.
const maskSentinel = "MASK"

// maskedValue renders a value with string values masked but keys visible — the
// View mode of the value workspace. For JSON, object/array string values become
// dots; for plain text the whole thing masks. Returns safe HTML.
func maskedValue(raw string, reveal bool) template.HTML {
	if reveal {
		return template.HTML(highlightJSON(raw))
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			masked := maskJSON(v)
			if out, err := json.MarshalIndent(masked, "", "  "); err == nil {
				return template.HTML(highlightJSON(string(out)))
			}
		}
	}
	// plain string: mask entirely
	return template.HTML(`<span class="mk">` + strings.Repeat("•", minInt(len(raw), 24)) + `</span>`)
}

func maskJSON(v any) any {
	switch t := v.(type) {
	case string:
		return maskSentinel
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = maskJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = maskJSON(e)
		}
		return out
	}
	return v
}

// highlightJSON produces lightweight syntax-colored, escaped HTML, turning the
// mask sentinel into a dotted span.
func highlightJSON(s string) string {
	s = template.HTMLEscapeString(s)
	s = strings.ReplaceAll(s, "&#34;"+maskSentinel+"&#34;", `<span class="mk">••••••••</span>`)
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '&' && strings.HasPrefix(s[i:], "&#34;") {
			end := strings.Index(s[i+5:], "&#34;")
			if end >= 0 {
				token := s[i : i+5+end+5]
				rest := s[i+5+end+5:]
				if strings.HasPrefix(strings.TrimLeft(rest, " "), ":") {
					out.WriteString(`<span class="jk">` + token + `</span>`)
				} else {
					out.WriteString(`<span class="js">` + token + `</span>`)
				}
				i += 5 + end + 5
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// diffLine is one line of a naive line-based diff.
type diffLine struct {
	Kind string // ctx | add | del
	Text string
}

func lineDiff(oldText, newText string) []diffLine {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	oldSet := map[string]int{}
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := map[string]int{}
	for _, l := range newLines {
		newSet[l]++
	}
	var out []diffLine
	for _, l := range oldLines {
		if newSet[l] == 0 {
			out = append(out, diffLine{"del", l})
		}
	}
	for _, l := range newLines {
		if oldSet[l] == 0 {
			out = append(out, diffLine{"add", l})
		} else {
			out = append(out, diffLine{"ctx", l})
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
