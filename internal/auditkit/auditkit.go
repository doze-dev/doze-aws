// Package auditkit builds the request one audit case describes: a baseline the
// service is known to accept, with a single constrained input replaced by a
// value that violates it.
//
// # Why this is not in modelcheck
//
// modelcheck walks a path to decide whether a request is valid. This walks a
// path to construct an invalid one. They are deliberately separate packages
// with separate segment parsers, because a test that shares the parser it is
// checking cannot catch a parser that is wrong in the same way twice — and a
// path grammar with three container kinds is exactly the sort of thing to get
// consistently wrong.
//
// # Exemplars
//
// Two thirds of a service's constraints sit inside structures. Mutating one
// means first sending a valid enclosing structure, which reopens the
// wrong-reason-refusal problem a level down: if the stand-in is itself invalid,
// every case under it is refused for the stand-in and the whole group reads as
// enforced when nothing was tested. Callers are expected to PROBE their
// exemplars — build the case with mutate=false and assert the service still
// accepts it — before trusting any result underneath.
package auditkit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// segRE splits a path segment into its member name and container markers:
// "[]" a list, "{}" a map, and both may appear on one segment.
var segRE = regexp.MustCompile(`^([A-Za-z0-9]+)((?:\[\]|\{\})*)$`)

func splitSegment(seg string) (string, []string) {
	m := segRE.FindStringSubmatch(seg)
	if m == nil {
		return seg, nil
	}
	var markers []string
	for i := 0; i+1 < len(m[2]); i += 2 {
		markers = append(markers, m[2][i:i+2])
	}
	return m[1], markers
}

// Apply builds a case's request in place.
//
// exemplars supplies a known-good value for any container the path descends
// through that the baseline does not already carry, keyed by the container's
// own path ("ShardFilter", "Records[]"). A value of nil means the case is about
// the member being absent, so the leaf is deleted.
//
// mutate=false stops before the leaf, which is how a caller probes that an
// exemplar leaves the baseline acceptable.
//
// It returns an error rather than failing the test, because a path it cannot
// build is a hole in the harness, not a finding about the service, and
// reporting them as the same thing is how an audit lies.
func Apply(body map[string]any, exemplars map[string]any, path string, value any, mutate bool) error {
	segs := strings.Split(path, ".")
	cur := body

	for i, seg := range segs[:len(segs)-1] {
		name, markers := splitSegment(seg)
		key := strings.Join(segs[:i+1], ".")
		if _, present := cur[name]; !present {
			ex, ok := exemplars[key]
			if !ok {
				return fmt.Errorf("no exemplar for container %q (needed by %q)", key, path)
			}
			cur[name] = DeepCopy(ex)
		}

		v := cur[name]
		for _, mk := range markers {
			switch mk {
			case "[]":
				lst, ok := v.([]any)
				if !ok || len(lst) == 0 {
					return fmt.Errorf("container %q is not a non-empty list", key)
				}
				v = lst[0]
			case "{}":
				m, ok := v.(map[string]any)
				if !ok || len(m) == 0 {
					return fmt.Errorf("container %q is not a non-empty map", key)
				}
				ks := make([]string, 0, len(m))
				for k := range m {
					ks = append(ks, k)
				}
				sort.Strings(ks)
				v = m[ks[0]]
			}
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("container %q is not a structure", key)
		}
		cur = m
	}

	if !mutate {
		return nil
	}

	leaf, markers := splitSegment(segs[len(segs)-1])
	if value == nil {
		delete(cur, leaf) // a @required case is the member's absence
		return nil
	}
	// A constrained leaf under markers bounds the ELEMENTS, not the collection
	// — TagKeys[] bounds each key — so the violating value is wrapped back up
	// through the markers, innermost first.
	wrapped := value
	for i := len(markers) - 1; i >= 0; i-- {
		switch markers[i] {
		case "[]":
			wrapped = []any{wrapped}
		case "{}":
			wrapped = map[string]any{"k": wrapped}
		}
	}
	cur[leaf] = wrapped
	return nil
}

// Containers lists the distinct container paths a set of case paths descends
// through — what a caller has to probe before trusting the cases under them.
func Containers(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		segs := strings.Split(p, ".")
		if len(segs) == 1 {
			continue
		}
		prefix := strings.Join(segs[:len(segs)-1], ".")
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	sort.Strings(out)
	return out
}

// DeepCopy keeps one case's mutation from leaking into the next through a
// shared baseline or exemplar — the cross-talk that makes a suite pass in
// isolation and fail in order.
func DeepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = DeepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = DeepCopy(val)
		}
		return out
	default:
		return v
	}
}
