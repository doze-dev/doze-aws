package dozeaws

// Moving an existing data directory into the per-region layout.
//
// Before regions, a service's store lived at <data-dir>/<service>. Now it lives
// at <data-dir>/<region>/<service>, or <data-dir>/_global/<service> for the
// region-less ones. An installation that predates the change has to be moved,
// and the move has to be safe enough to run automatically at startup:
//
//   - It is a directory RENAME per service. No file is read, rewritten, or
//     re-encoded, so there is no format risk and nothing to get wrong halfway.
//   - It refuses to overwrite. If a destination already exists the migration
//     stops with both paths named, rather than merging two stores.
//   - It says what it will do before doing it, because a data directory moving
//     under you unannounced is indistinguishable from data loss.
//
// The one thing it deliberately does NOT do is delete anything.

import (
	"fmt"
	"os"
	"path/filepath"
)

// NeedsMigration reports whether dataDir is in the pre-region layout: at least
// one service store sitting directly under it.
//
// The check is by known service name rather than "any directory", so an
// unrelated folder someone left in the data directory is never moved.
func NeedsMigration(dataDir string) bool {
	return len(oldLayoutServices(dataDir)) > 0
}

// oldLayoutServices lists the services with a store directly under dataDir.
func oldLayoutServices(dataDir string) []string {
	if dataDir == "" {
		return nil
	}
	var found []string
	for _, name := range Implemented {
		if fi, err := os.Stat(filepath.Join(dataDir, name)); err == nil && fi.IsDir() {
			found = append(found, name)
		}
	}
	return found
}

// MigrationPlan is what a migration would do, so it can be printed before it
// runs — and so a caller can offer a dry run.
type MigrationPlan struct {
	// Moves holds the renames, in the order they would be applied.
	Moves []MigrationMove
}

// MigrationMove is one service's directory rename.
type MigrationMove struct {
	Service string
	From    string
	To      string
}

// PlanMigration works out what moving dataDir into the per-region layout would
// do. region names the region the existing data is treated as belonging to:
// there was only one before this change, so its identity is the instance's.
func PlanMigration(dataDir, region string) MigrationPlan {
	var p MigrationPlan
	for _, name := range oldLayoutServices(dataDir) {
		p.Moves = append(p.Moves, MigrationMove{
			Service: name,
			From:    filepath.Join(dataDir, name),
			To:      filepath.Join(dataDir, ServiceDir(region, name)),
		})
	}
	return p
}

// Describe renders the plan as lines to print. Empty when there is nothing to do.
func (p MigrationPlan) Describe() []string {
	if len(p.Moves) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.Moves)+1)
	out = append(out, fmt.Sprintf("moving %d service directories into the per-region layout:", len(p.Moves)))
	for _, m := range p.Moves {
		out = append(out, fmt.Sprintf("  %s  ->  %s", m.From, m.To))
	}
	return out
}

// Migrate applies the plan. It is idempotent in the sense that running it on an
// already-migrated directory finds nothing to move and returns nil.
//
// It stops at the first problem rather than continuing, and never overwrites:
// a destination that already exists means two stores for one service, which is
// a question for a person rather than something to resolve by guessing.
func Migrate(dataDir, region string) (MigrationPlan, error) {
	p := PlanMigration(dataDir, region)
	for _, m := range p.Moves {
		if _, err := os.Stat(m.To); err == nil {
			return p, fmt.Errorf("dozeaws: cannot migrate %s: %s already exists; "+
				"move or remove one of them and start again", m.Service, m.To)
		}
		if err := os.MkdirAll(filepath.Dir(m.To), 0o755); err != nil {
			return p, fmt.Errorf("dozeaws: migrate %s: %w", m.Service, err)
		}
		if err := os.Rename(m.From, m.To); err != nil {
			return p, fmt.Errorf("dozeaws: migrate %s: %w", m.Service, err)
		}
	}
	return p, nil
}
