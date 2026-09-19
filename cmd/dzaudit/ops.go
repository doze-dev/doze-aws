package main

// `dzaudit ops <model>` — the complete operation list AWS publishes.
//
// # What this closes
//
// README and CONTRIBUTING both say operation coverage is "enforced per service
// by a frozen model list (*/coverage_test.go)". It was true for five services.
// In nine others that file is an SDK smoke suite with no operation list in it
// at all, so the filename means two different things in the tree and the claim
// was a third.
//
// The five lists that do exist are hand-transcribed — about three hundred
// operation names typed out of `dzaudit list` — and pinned by a magic count
// that only fails once somebody runs the tool. model-drift.yml's own header
// admits it: "The per-service modelOperations lists are pinned by an exact
// count and would fail — but only once someone ran the tool."
//
// Committing the list instead fixes both halves. The five stop being
// transcriptions and become reads of a fixture, and the nine get the same
// assertion for ten lines each. And because model-drift.yml regenerates these
// weekly and fails on a diff, AWS adding an operation becomes a red build with
// nobody in the loop rather than a discovery.
//
// # Why not reuse shapes_*.json
//
// It looks like the same information and is not: emitShapes skips an operation
// with no output shape or no assertable members, so shapes_sfn.json holds 27
// entries for a 37-operation model. A list that is quietly short is worse than
// no list, because it makes the ratchet pass while missing ten operations.

import (
	"encoding/json"
	"io"
	"sort"
)

// emitOps writes every operation the service model documents, sorted.
//
// Short names, not Smithy IDs: the dispatch tables these are checked against
// are keyed by the name on the wire, and a fixture that needed translating at
// every use would invite the translation to differ between uses.
func emitOps(w io.Writer, m *model) error {
	_, _, ids := m.service()
	ops := make([]string, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, shortName(id))
	}
	sort.Strings(ops)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ops)
}
