package shapecheck

// The path grammar has three implementations in this tree. Nothing checked
// that they agree.
//
// "Table.Items[].Attributes{}.Name" is written in constraint tables, in case
// files and in shape files, and three separate pieces of code turn it into a
// walk over a JSON document:
//
//	auditkit.SplitSegment    a regex, per segment
//	modelcheck.SplitSegment  a regex, per segment, behind a parse cache
//	shapecheck.segments      hand-rolled, over the whole path
//
// The duplication is deliberate and auditkit's own note argues for it: a test
// that shares the parser it is checking cannot catch a parser that is wrong in
// the same way twice. But "deliberately duplicated" is only a virtue while the
// copies actually agree, and until now the agreement was an assumption. A
// disagreement would not announce itself — a path one parser resolves and
// another does not reads as "member absent", and an absent member is a PASS in
// every one of these checkers. The whole audit pipeline would go quiet rather
// than go red.
//
// # The honest shape of what this covers
//
// auditkit's and modelcheck's copies are not independent. They are the same
// regex and the same loop, one of them behind a sync.Map; comparing them to
// each other proves only that the copy-paste was faithful, which is worth
// knowing and is not worth much. THAT is asserted separately and exactly,
// because it is an equality rather than a property.
//
// The real check is the regex pair against shapecheck's hand-rolled parser,
// which shares no code with them and was written from the grammar rather than
// from the regex. Those two disagreeing is a finding.

import (
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/doze-dev/doze-aws/internal/auditkit"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
)

// pathGen generates paths in the grammar, and a fair number that are not in it.
//
// The malformed ones matter as much as the well-formed: a path with an unclosed
// bracket or a marker in the middle of a name is what a typo in a constraint
// table looks like, and the three parsers reaching different conclusions about
// one is precisely the silent failure this exists to catch.
func pathGen() *rapid.Generator[string] {
	name := rapid.SampledFrom([]string{"A", "Name", "Items", "x9", "QueueUrl"})
	marker := rapid.SampledFrom([]string{"", "[]", "{}", "[][]", "{}{}", "[]{}", "{}[]", "[", "]", "{", "[]["})
	segment := rapid.Custom(func(t *rapid.T) string {
		return name.Draw(t, "name") + marker.Draw(t, "marker")
	})
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(1, 4).Draw(t, "depth")
		parts := make([]string, n)
		for i := range parts {
			parts[i] = segment.Draw(t, "segment")
		}
		return strings.Join(parts, ".")
	})
}

// TestTheTwoRegexParsersAreTheSameCode asserts the equality the comment above
// claims, rather than leaving it as a remark that could quietly stop being
// true. If these two ever diverge, the cross-check below is comparing three
// things and this test says so first.
func TestTheTwoRegexParsersAreTheSameCode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		seg := rapid.Custom(func(t *rapid.T) string {
			return rapid.SampledFrom([]string{"A", "Items", "x9"}).Draw(t, "name") +
				rapid.SampledFrom([]string{"", "[]", "{}", "[]{}", "[", "]["}).Draw(t, "marker")
		}).Draw(rt, "segment")

		aName, aMarks := auditkit.SplitSegment(seg)
		mName, mMarks := modelcheck.SplitSegment(seg)
		if aName != mName || !sameStrings(aMarks, mMarks) {
			rt.Fatalf("%q: auditkit says (%q, %v), modelcheck says (%q, %v)",
				seg, aName, aMarks, mName, mMarks)
		}
	})
}

// TestTheThreePathParsersAgree is the one that can find something.
func TestTheThreePathParsersAgree(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		path := pathGen().Draw(rt, "path")
		want := viaRegex(path)
		got := viaSegments(path)
		if !sameStrings(want, got) {
			rt.Fatalf("%q: the regex parsers read %v, shapecheck reads %v",
				path, want, got)
		}
	})
}

// viaRegex decomposes a path with auditkit's parser, normalised to the flat
// form shapecheck produces: one entry per member and one per container step,
// in the order a walk would take them.
func viaRegex(path string) []string {
	var out []string
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		name, markers := auditkit.SplitSegment(part)
		if name != "" {
			out = append(out, "member:"+name)
		}
		for _, m := range markers {
			out = append(out, m)
		}
	}
	return out
}

// viaSegments is the same decomposition from shapecheck's own parser. It
// reaches segments directly because this file is IN the package — which is why
// the cross-check lives here rather than in a package of its own: only the two
// regex copies had to be exported for it, and a third export would have been
// one more piece of API existing solely to be tested.
func viaSegments(path string) []string {
	var out []string
	for _, s := range segments(path) {
		switch s.kind {
		case segMember:
			out = append(out, "member:"+s.name)
		case segElem:
			out = append(out, "[]")
		case segValue:
			out = append(out, "{}")
		}
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
