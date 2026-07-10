package awsquery

import (
	"net/url"
	"testing"
)

// FuzzMembers feeds arbitrary query strings + prefixes to the indexed-member
// parser (Attribute.N.* / entry.N.*), asserting it never panics on malformed
// or adversarial input.
func FuzzMembers(f *testing.F) {
	f.Add("Attribute.1.Name=a&Attribute.1.Value=b&Attribute.2.Name=c", "Attribute")
	f.Add("member.1=x&member.2=y", "member")
	f.Add("Attribute.999999999999.Name=a", "Attribute")
	f.Add("=&.=.&..", "")
	f.Add("Attribute..Name=x", "Attribute")
	f.Fuzz(func(t *testing.T, raw, prefix string) {
		vals, err := url.ParseQuery(raw)
		if err != nil {
			return
		}
		_ = Members(vals, prefix, false)
		_ = Members(vals, prefix, true)
	})
}
