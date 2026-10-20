package console

import (
	"html/template"
	"strings"
)

// narrowWords is a header sentence's clause as the boards draw it at both
// widths. Board Narrow (1100 px and below) says "hidden 60 s" and "dead letters
// after 3" where the wide boards say "hidden for 60 s on receive" and "dead
// letters to <queue> after 3". The long sentence is the one written; the words
// the short one drops are written {in braces}, and drawn as their own spans
// (class s-wide) that the stylesheet hides at 1100 and below. So the text reads
// whole wherever the stylesheet is not applied, and a service writes one
// sentence, not two.
func narrowWords(s string) template.HTML {
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(s[open:], '}')
		if end < 0 {
			break
		}
		b.WriteString(template.HTMLEscapeString(s[:open]))
		b.WriteString(`<span class="s-wide">`)
		b.WriteString(template.HTMLEscapeString(s[open+1 : open+end]))
		b.WriteString(`</span>`)
		s = s[open+end+1:]
	}
	b.WriteString(template.HTMLEscapeString(s))
	return template.HTML(b.String())
}
