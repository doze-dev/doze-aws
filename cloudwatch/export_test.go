package cloudwatch

// Dispatched is the set of operations with a real handler, exported so the
// rejection-parity suite (package cloudwatch_test) derives it from the
// dispatch table rather than keeping its own copy.
//
// It kept its own copy until D5, and the copy went stale the moment D3 added
// the eight alarm operations: 110 model-derived cases sat in the fixture and
// were silently filtered out, so the suite reported a pass over the four
// metric operations while proving nothing about alarms. A hand-maintained
// mirror of a table that grows is a test that stops testing without saying
// so, which is why this is derived now.
func Dispatched() map[string]bool {
	out := make(map[string]bool, len(handlers))
	for op := range handlers {
		out[op] = true
	}
	return out
}
