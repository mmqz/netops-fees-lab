package domain

// SeverityForCode maps a rule code of the validation catalog (PROJECT_BRIEF
// §7) to its severity. The second return value reports whether code is a
// known rule of the catalog. Row-phase E codes fail a row, W codes record a
// warning on an imported row, I codes are set-level info findings.
func SeverityForCode(code string) (Severity, bool) {
	switch code {
	case RuleDuplicateKey, RuleMissingRequiredField, RuleInvalidReference,
		RuleInvalidFormat, RuleSubnetOverlap, RuleDuplicateAddress:
		return SeverityError, true
	case RuleOrphanRecord, RuleUnknownStatusValue, RuleCircuitSelfLoop:
		return SeverityWarning, true
	case RuleUnlinkedDevice:
		return SeverityInfo, true
	default:
		return "", false
	}
}
