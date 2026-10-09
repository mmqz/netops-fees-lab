// Package domain defines the pure domain model of NetOps Lab: entities,
// value enums, validation rule codes, rule version and sentinel errors.
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
