package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Fingerprint computes the header fingerprint of one sheet (W24, §12-B7
// 方案 A — issue #28): the sha256 hex digest over the CANONICAL sheet
// signature
//
//	sheet + "\x1f" + join(normalizedHeaders, "\x1e")
//
// where normalizedHeaders are the sheet's headers IN SHEET ORDER, each
// trimmed + fullwidth-folded (foldFullwidth: U+FF01..FF5E and the
// ideographic space) + lower-cased. The function is PURE (no I/O, no
// clock): the same (sheet, headers) pair always yields the same digest —
// the property the unattended import's determinism tests pin.
//
// Why the SEPARATORS are control characters: \x1f (unit) and \x1e (record)
// cannot appear in an xlsx cell's trimmed text in practice, so no header
// combination can forge another sheet's signature by concatenation.
//
// Why column ORDER matters: a column permutation is a DIFFERENT fingerprint
// by design. Excel column order is what the mapping's columnMapping and the
// row Values are keyed against conceptually; a workbook whose columns moved
// is a structurally different workbook (someone re-exported it through a
// different tool), and silently auto-importing it with a stale mapping is
// exactly the mismatched-import hazard B7 exists to prevent. A permuted
// workbook falls back to the human confirmation flow — honest re-confirmation
// beats a lucky guess.
//
// Why fold trim/case/fullwidth but keep everything else: cosmetic drift
// (a trailing space, "管理IP" vs "管理ip", fullwidth ASCII) does not change
// what a column IS, so it must not invalidate a confirmed template; but any
// real content change (rename, add, remove, reorder) changes the signature.
func Fingerprint(sheet string, headers []string) string {
	normalized := make([]string, len(headers))
	for i, h := range headers {
		normalized[i] = NormalizeHeader(h)
	}
	var b strings.Builder
	b.Grow(len(sheet) + len(headers)*8)
	b.WriteString(sheet)
	b.WriteString("\x1f")
	b.WriteString(strings.Join(normalized, "\x1e"))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// NormalizeHeader canonicalizes ONE header for fingerprinting: trim, fold
// fullwidth ASCII-range runes to halfwidth, lower-case. The ladder mirrors
// NormalizeNaturalKeyComponent minus the upper-casing (fingerprinting is
// case-INsensitive so "管理IP" and "管理ip" are the same column). Exported
// because the unattended import's header re-anchoring (importer) must
// compare a stored template's headers to a current sheet's headers under
// the SAME canonical form the fingerprint used — a private helper would
// risk a second, drifting normalization ladder.
func NormalizeHeader(h string) string {
	return strings.ToLower(foldFullwidth(strings.TrimSpace(h)))
}
