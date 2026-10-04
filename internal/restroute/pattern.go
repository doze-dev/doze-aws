package restroute

import "strings"

// Pattern turns a model path template into a chi pattern. segs has an empty
// string where a label goes and labels names it (the layout dzaudit emits);
// greedy marks a trailing label that takes the rest of the path, which chi
// spells `*`.
//
//	Pattern([]string{"2015-03-31","functions","","aliases",""},
//	        []string{"","","FunctionName","","Name"}, false)
//	  = "/2015-03-31/functions/{FunctionName}/aliases/{Name}"
func Pattern(segs, labels []string, greedy bool) string {
	if len(segs) == 0 {
		return "/"
	}
	var b strings.Builder
	for i, s := range segs {
		b.WriteByte('/')
		switch {
		case s != "":
			b.WriteString(s)
		case greedy && i == len(segs)-1:
			b.WriteByte('*')
		default:
			b.WriteString("{" + labels[i] + "}")
		}
	}
	return b.String()
}
