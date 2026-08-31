// Package docs embeds the api-support ledger so the console can render each
// service's fidelity table at runtime. The ledger stays the single source of
// truth, maintained as markdown next to the services it describes; embedding
// it is read-only access, not a second copy.
package docs

import "embed"

//go:embed api-support/*.md
var FS embed.FS
