package domain

// AllRuleCodes lists every rule code of the validation catalog in canonical
// order (E001..E006, W001..W006, I001 — the declaration order of the
// constants in domain.go). The schema-discovery endpoint (GET /api/schema,
// W5-D) projects this list together with SeverityForCode and
// RuleDescriptionFor so rule metadata has a single runtime source. A new rule
// constant must be appended here (and given a description) —
// TestRuleCodes pins the constant set and the catalog order.
func AllRuleCodes() []string {
	return []string{
		RuleDuplicateKey, RuleMissingRequiredField, RuleInvalidReference,
		RuleInvalidFormat, RuleSubnetOverlap, RuleDuplicateAddress,
		RuleOrphanRecord, RuleUnknownStatusValue, RuleCircuitSelfLoop,
		RuleKeyFallback, RuleAttributeChange, RuleSuspectedRename, RuleUnlinkedDevice,
	}
}

// RuleDescriptionFor returns the one-line English description of a rule code
// (the same text as the constant's doc comment in domain.go — the catalog's
// human-readable gloss). The second return reports whether code is known.
func RuleDescriptionFor(code string) (string, bool) {
	switch code {
	case RuleDuplicateKey:
		return "natural key duplicated (row)", true
	case RuleMissingRequiredField:
		return "required field empty or unmapped (row)", true
	case RuleInvalidReference:
		return "reference target missing (row)", true
	case RuleInvalidFormat:
		return "ipv4/cidr/int/enum parse failure (row)", true
	case RuleSubnetOverlap:
		return "ip_pool CIDRs overlap (set)", true
	case RuleDuplicateAddress:
		return "same mgmt_ip/gateway on >1 resource (set)", true
	case RuleOrphanRecord:
		return "optional relation field left empty (row)", true
	case RuleUnknownStatusValue:
		return "status not in the known enum (row)", true
	case RuleCircuitSelfLoop:
		return "circuit a_end == z_end (row)", true
	case RuleKeyFallback:
		return "natural key degraded to fallback fields (row)", true
	case RuleAttributeChange:
		return "attribute diff on a duplicate-key row awaits confirmation (row)", true
	case RuleSuspectedRename:
		return "name-keyed resource likely renamed (same attrs, new key) awaits confirmation (row)", true
	case RuleUnlinkedDevice:
		return "device with zero relations (set)", true
	default:
		return "", false
	}
}

// SeverityForCode maps a rule code of the validation catalog (PROJECT_BRIEF
// §7) to its severity. The second return value reports whether code is a
// known rule of the catalog. Row-phase E codes fail a row, W codes record a
// warning on an imported row, I codes are set-level info findings.
func SeverityForCode(code string) (Severity, bool) {
	switch code {
	case RuleDuplicateKey, RuleMissingRequiredField, RuleInvalidReference,
		RuleInvalidFormat, RuleSubnetOverlap, RuleDuplicateAddress:
		return SeverityError, true
	case RuleOrphanRecord, RuleUnknownStatusValue, RuleCircuitSelfLoop, RuleKeyFallback,
		RuleAttributeChange, RuleSuspectedRename:
		return SeverityWarning, true
	case RuleUnlinkedDevice:
		return SeverityInfo, true
	default:
		return "", false
	}
}
