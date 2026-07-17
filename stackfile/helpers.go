package stackfile

// Small helpers shared by the per-service apply/export files: tag rendering,
// JSON marshaling, schema-less XML picking, and ARN/number parsing.

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
)

// tagList renders a tag map as the sorted key/value pair list most AWS APIs
// take (key and value field names vary by service).
func tagList(tags map[string]string, keyField, valField string) []map[string]string {
	var out []map[string]string
	for _, k := range sortedNames(tags) {
		out = append(out, map[string]string{keyField: k, valField: tags[k]})
	}
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func arnLeaf(arn string) string {
	if i := strings.LastIndexAny(arn, ":/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func xmlValue(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	// XML-encoded payloads (SNS attribute values carry JSON) need their
	// entities restored — &#34;/&quot;/&amp; etc.
	return html.UnescapeString(rest[:j])
}

// xmlBlock returns the inner text of the first <tag>…</tag> block, or "".
func xmlBlock(s, tag string) string {
	blocks := xmlBlocks(s, tag)
	if len(blocks) == 0 {
		return ""
	}
	return blocks[0]
}

// xmlBlocks returns the inner text of every <tag>…</tag> block.
func xmlBlocks(s, tag string) []string {
	open, close := "<"+tag+">", "</"+tag+">"
	var out []string
	for {
		i := strings.Index(s, open)
		if i < 0 {
			break
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			break
		}
		out = append(out, rest[:j])
		s = rest[j+len(close):]
	}
	return out
}

// xmlValues returns every occurrence of <tag>…</tag>.
func xmlValues(s, tag string) []string {
	var out []string
	for {
		v := xmlValue(s, tag)
		if v == "" {
			break
		}
		out = append(out, v)
		i := strings.Index(s, "</"+tag+">")
		s = s[i+len(tag)+3:]
	}
	return out
}
