package lambda

// WithoutConstraintTables removes the model-derived constraint tables and
// returns the function that puts them back.
//
// Exported for the rejection-parity suite (package lambda_test), which
// replays every case a second time with the tables gone to measure the figure
// the ledger publishes: how many of them the hand-written checks would let
// through on their own. That number is the only claim in the ledger that says
// the audit FOUND something rather than covered something, and until this
// existed there was no procedure behind it.
//
// Safe because this package runs no tests in parallel, so nothing can observe
// the gap. The caller must defer the returned function.
func WithoutConstraintTables() func() {
	saved := constraintTables
	constraintTables = nil
	return func() { constraintTables = saved }
}
