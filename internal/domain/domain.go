// Package domain defines the pure domain model of NetOps Lab: entities,
// value enums, validation rule codes, rule version and sentinel errors.
// Phase 2 adds the simulation workbench model (workspaces, virtual devices,
// interfaces, links, changesets, check runs and checkpoints, plus the op and
// check code catalogs of PROJECT_BRIEF_P2 §4/§5).
//
// The package has no dependency on storage, transport or parsing packages
// (only the standard library plus github.com/google/uuid for ID generation),
// so every other layer may import it freely. All rule codes, statuses and
// relation types used across the application must be defined here as
// constants — no magic strings elsewhere (see PROJECT_BRIEF §14).
package domain

import (
	"errors"

	"github.com/google/uuid"
)

// RuleVersion is the version of the validation rule catalog (PROJECT_BRIEF §7).
// Identical inputs plus this version must produce identical business results.
const RuleVersion = "1.0.0"

// AppVersion is the version of the NetOps Lab application itself, reported by
// the health endpoint, the MCP server implementation and the CLI. v0.7.0 is
// the data-model v1.0 milestone (issues #16-#29, rounds W19-W25): the
// resource graph expands from 8 types / 4 relations to 33 types / 29
// relations across the seven-layer model (L0 space .. L6 business), with
// the layer/domain registry as executable code, metrics storage + ingest
// + synthdata + anomaly scan, run-state history, the layered BFS traverse
// / impact / context endpoints, configurable detection rules + the alert
// state machine, and the header-fingerprint unattended import (mapping
// templates — the「每天导入即可」enabler). TestAppVersionP2 pins this
// constant and TestAppVersionMatchesChangelogHead enforces the CHANGELOG
// single fact source, so the three-way hand-copied versions cannot drift
// again.
const AppVersion = "v0.7.0"

// Validation rule codes (PROJECT_BRIEF §7). Row-phase codes run during import
// on each data row; set-phase codes run after import on the whole database.
const (
	RuleDuplicateKey         = "E001" // natural key duplicated (row)
	RuleMissingRequiredField = "E002" // required field empty or unmapped (row)
	RuleInvalidReference     = "E003" // reference target missing (row)
	RuleInvalidFormat        = "E004" // ipv4/cidr/int/enum parse failure (row)
	RuleSubnetOverlap        = "E005" // ip_pool CIDRs overlap (set)
	RuleDuplicateAddress     = "E006" // same mgmt_ip/gateway on >1 resource (set)
	RuleOrphanRecord         = "W001" // optional relation field left empty (row)
	RuleUnknownStatusValue   = "W002" // status not in the known enum (row)
	RuleCircuitSelfLoop      = "W003" // circuit a_end == z_end (row)
	RuleKeyFallback          = "W004" // natural key degraded to fallback fields (row)
	RuleAttributeChange      = "W005" // attribute diff on a duplicate-key row awaits confirmation (row)
	RuleSuspectedRename      = "W006" // suspected rename of a name-keyed resource awaits confirmation (row)
	RuleUnlinkedDevice       = "I001" // device with zero relations (set)
)

// SetLevelRuleCodes lists the codes of all set-phase rules. Set-level issues
// are stored with a NULL batch_id and are deterministically deleted and
// re-inserted by the validation engine. The repo layer uses this list to
// purge set-level issues.
var SetLevelRuleCodes = []string{
	RuleSubnetOverlap,
	RuleDuplicateAddress,
	RuleUnlinkedDevice,
}

// Sentinel errors shared by all layers.
var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("domain: entity not found")
	// ErrConflict is returned when an operation conflicts with existing
	// state (for example a duplicate natural key or a batch in use).
	ErrConflict = errors.New("domain: entity conflict")
)

// NewID returns a new entity identifier of the form "<prefix>_<uuid>",
// for example "res_0cf362e6-9ef3-4a2e-b1d2-1e37f8f3a6c0". Supported
// prefixes: res, rel, src, bat, iss.
func NewID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}
