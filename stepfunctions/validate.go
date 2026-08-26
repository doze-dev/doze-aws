package stepfunctions

import "github.com/doze-dev/doze-aws/internal/modelcheck"

// Step Functions' model-derived input validation: the constraint tables, walked
// by internal/modelcheck.
//
// Empty until the audit stage generates it with `dzaudit cases sfn` (172
// constrained inputs across 37 operations). Declared now so ServeHTTP's
// validation hook is wired from the first commit rather than retrofitted —
// modelcheck.ValidateMap over a nil table is a no-op, so this costs nothing and
// means the audit is a data change rather than a code change.
var constraintTables = map[string][]modelcheck.Constraint{}
