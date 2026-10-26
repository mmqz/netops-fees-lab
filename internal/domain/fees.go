package domain

// The fee-analysis domain types (#58/#60/#61, migration v29): the state
// layer's record shapes of the fee import pipeline. The fee DATA never
// lives in SQLite — only these batch bookkeeping rows; snapshots stay on
// the filesystem (content-addressed) and the normalized rows live in the
// hive-partitioned Parquet analysis table queried by DuckDB. Types live
// here (not in internal/fees) for the same reason RenameProposal does:
// repo needs them, and repo must never import a service package.

import (
	"fmt"
	"strings"
	"time"
)

// errUnknownFeeType refuses a fee type token outside the closed enum.
func errUnknownFeeType(raw string) error {
	return fmt.Errorf("domain: 未知费用类型 %q（可用：%s）", raw, strings.Join(FeeTypes(), "、"))
}

// FeeType is the closed enum of the fee domains the analysis layer
// accepts. Values are directory-safe lower-case tokens (hive partition
// values).
type FeeType string

const (
	// FeeTypeElectricity is 电费 (electricity bills of sites).
	FeeTypeElectricity FeeType = "electricity"
	// FeeTypeTowerLease is 铁塔租费 (tower lease fees).
	FeeTypeTowerLease FeeType = "tower_lease"
	// FeeTypeRepair is 修理费 (repair costs).
	FeeTypeRepair FeeType = "repair"
	// FeeTypeMaintenance is 综维费用 (maintenance / ops-efficiency data).
	FeeTypeMaintenance FeeType = "maintenance"
)

// ParseFeeType validates a raw token (CLI flag / mapping JSON) against the
// closed enum.
func ParseFeeType(raw string) (FeeType, error) {
	switch FeeType(raw) {
	case FeeTypeElectricity, FeeTypeTowerLease, FeeTypeRepair, FeeTypeMaintenance:
		return FeeType(raw), nil
	default:
		return "", errUnknownFeeType(raw)
	}
}

// FeeTypes lists every fee type value (CLI usage text).
func FeeTypes() []string {
	return []string{
		string(FeeTypeElectricity), string(FeeTypeTowerLease),
		string(FeeTypeRepair), string(FeeTypeMaintenance),
	}
}

// FeeTypeLabel is the Chinese display name of a fee type.
func FeeTypeLabel(t FeeType) string {
	switch t {
	case FeeTypeElectricity:
		return "电费"
	case FeeTypeTowerLease:
		return "铁塔租费"
	case FeeTypeRepair:
		return "修理费"
	case FeeTypeMaintenance:
		return "综维费用"
	default:
		return string(t)
	}
}

// FeeStatus is the fee-batch state machine: completed is the only state
// whose Parquet files are authoritative; failed/rolled_back batches' files
// (if any survived a crash) are analysis-layer GC orphans by definition.
type FeeStatus string

const (
	// FeeStatusCompleted: snapshot + Parquet + batch row all landed.
	FeeStatusCompleted FeeStatus = "completed"
	// FeeStatusFailed: the conversion failed before the commit point.
	FeeStatusFailed FeeStatus = "failed"
	// FeeStatusRolledBack: a completed batch deliberately reversed
	// (Parquet files deleted, snapshot kept as the audit trail).
	FeeStatusRolledBack FeeStatus = "rolled_back"
)

// FeeBatch is one fee import: the state-layer record of a snapshot +
// Parquet conversion. Idempotency key = (sha256, sheet, fee_type,
// fee_year, fee_month, mapping), enforced by a partial UNIQUE index over
// completed batches only — failed/rolled_back attempts never block a
// clean retry. Sheet is the workbook sheet the rows came from (#125,
// migration v34): ” for CSV files and for every batch recorded before
// the multi-sheet channel — a real monthly ledger carries ONE fee type
// per sheet (电费/铁塔租费/修理费), and two same-type sheets of one file
// are different data, not an idempotency duplicate.
type FeeBatch struct {
	ID           string
	WorkbookName string
	SHA256       string
	Sheet        string // workbook sheet of origin ('' = CSV / pre-v34)
	FeeType      FeeType
	FeeYear      int
	FeeMonth     int
	SnapshotPath string // relative to the data dir (content-addressed)
	SourcePath   string // absolute path the import actually read (issue #6; '' = unknown/pre-v35)
	Mapping      string // canonical JSON of the column mapping
	// PeriodSource is how the batch's period was established (#15,
	// migration v36): "auto" (A档 — a dedicated period field in the
	// content agrees with the declaration), "unverified" (B档 — only
	// indirect evidence, or an operator override of a disagreement),
	// "manual" (C档 — no period evidence in the content; the human
	// declaration is the only source). Empty = pre-v36 legacy row.
	PeriodSource string
	// PeriodBasis is the 识别依据 (#15 acceptance 4): the probe fields,
	// agreement counts and ambiguity notes. Empty on legacy rows.
	PeriodBasis  string
	Status       FeeStatus
	RowsTotal    int
	RowsImported int
	RowsFailed   int
	Error        string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// LinkGrade is the association grade of a fee-site link (#63, #58 stage 3).
type LinkGrade string

const (
	// LinkGradeExact: fee code == the resource's natural key, byte-for-byte.
	LinkGradeExact LinkGrade = "exact"
	// LinkGradeRule: equality under a named normalization rule (recorded).
	LinkGradeRule LinkGrade = "rule"
	// LinkGradeFuzzy: a similarity candidate — pending until a human
	// confirms (never auto-merged).
	LinkGradeFuzzy LinkGrade = "fuzzy"
)

// LinkStatus is the link lifecycle: exact/rule create confirmed (auto);
// fuzzy creates pending; a human confirms or dismisses. Confirmed and
// dismissed are terminal (re-confirm/re-dismiss refuse).
type LinkStatus string

const (
	// LinkStatusConfirmed: the live association (at most one per code).
	LinkStatusConfirmed LinkStatus = "confirmed"
	// LinkStatusPending: a fuzzy proposal awaiting a human decision.
	LinkStatusPending LinkStatus = "pending"
	// LinkStatusDismissed: a fuzzy proposal a human rejected (audit kept).
	LinkStatusDismissed LinkStatus = "dismissed"
)

// LinkSource tells who created the association: auto (deterministic
// exact/rule matching) or human (a confirmed fuzzy proposal).
type LinkSource string

const (
	// LinkSourceAuto: deterministic matcher output.
	LinkSourceAuto LinkSource = "auto"
	// LinkSourceHuman: a human-confirmed fuzzy candidate.
	LinkSourceHuman LinkSource = "human"
)

// FeeSiteLink is one fee-site ↔ resource association row (migration v30).
type FeeSiteLink struct {
	ID           string
	FeeSiteCode  string
	ResourceID   string
	ResourceType ResourceType
	ResourceKey  string
	Grade        LinkGrade
	Rule         string
	Similarity   float64 // fuzzy grade only; 0 otherwise
	Status       LinkStatus
	Source       LinkSource
	BatchID      string // the fee batch that produced a fuzzy proposal; "" otherwise
	CreatedAt    time.Time
	DecidedAt    *time.Time
}

// ClueDecisionStatus is the closed enum of the risk-clue decision states
// (#119, migration v33). verified: the human CONFIRMED the clue is a real
// anomaly — it stays visible (marked) as the known-issues ledger.
// dismissed: the human ruled the clue out — it never re-appears in the
// default risk output (the #69 PairDecided doctrine: a decided pair stays
// decided; the count is still reported, never silently dropped).
type ClueDecisionStatus string

const (
	// ClueDecisionVerified: human-confirmed real anomaly (visible, marked).
	ClueDecisionVerified ClueDecisionStatus = "verified"
	// ClueDecisionDismissed: human-ruled-out clue (suppressed, counted).
	ClueDecisionDismissed ClueDecisionStatus = "dismissed"
)

// ParseClueDecisionStatus validates a raw token against the closed enum.
func ParseClueDecisionStatus(raw string) (ClueDecisionStatus, error) {
	switch ClueDecisionStatus(raw) {
	case ClueDecisionVerified, ClueDecisionDismissed:
		return ClueDecisionStatus(raw), nil
	default:
		return "", fmt.Errorf("domain: 未知线索裁定 %q（可用：verified、dismissed）", raw)
	}
}

// IsValid reports whether s is a known clue decision status (the values
// must stay in sync with risk_clue_decisions' CHECK constraint, v33).
func (s ClueDecisionStatus) IsValid() bool {
	switch s {
	case ClueDecisionVerified, ClueDecisionDismissed:
		return true
	default:
		return false
	}
}

// RiskClueDecision is one human decision on a computed risk clue
// (migration v33). The clue has no row of its own — it is recomputed from
// the analysis layer on every run — so the decision IS the record, keyed
// by the clue's identity (scenario + fee type + site + period). No
// foreign keys by design: site codes come from fee files (not the
// resources table), and the row is pure audit — batch rollback never
// touches it (the W005/W006 doctrine: human decisions survive as the
// audit trail). Re-deciding flips the status and refreshes DecidedAt
// (the human may change their mind as evidence evolves; the ledger keeps
// the LATEST decision, the optional Note carries the reason).
type RiskClueDecision struct {
	ID        string
	Scenario  string
	FeeType   FeeType
	SiteCode  string
	Year      int
	Month     int // 0 = the whole-year-scope shape (unlinked clues may carry it)
	Status    ClueDecisionStatus
	Note      string
	DecidedAt time.Time
}
