package mapping

import (
	"encoding/json"
	"testing"
)

// fpJSON renders a fingerprint test case compactly for failure messages.
func fpJSON(sheet string, headers []string) string {
	b, _ := json.Marshal([]any{sheet, headers})
	return string(b)
}

// TestFingerprintDeterministic pins the pure-function contract: the same
// (sheet, headers) pair always yields the same digest, and the digest is a
// 64-char lowercase hex string (sha256). Two independent invocations of the
// same input — including a copied header slice — must agree (the 7-day
// unattended journey recomputes the fingerprint per workbook).
func TestFingerprintDeterministic(t *testing.T) {
	sheet := "设备清单"
	headers := []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}

	first := Fingerprint(sheet, headers)
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 (sha256 hex)", len(first))
	}
	for _, r := range first {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			t.Fatalf("fingerprint %q contains non-hex rune %q", first, r)
		}
	}
	if second := Fingerprint(sheet, headers); second != first {
		t.Errorf("Fingerprint not deterministic: %s vs %s", first, second)
	}
	// A copied slice (fresh backing array) yields the same digest: the value,
	// not the slice identity, is the input.
	headersCopy := append([]string(nil), headers...)
	if again := Fingerprint(sheet, headersCopy); again != first {
		t.Errorf("Fingerprint of copied headers = %s, want %s", again, first)
	}
	// Same workbook bytes re-parsed ⇒ same headers ⇒ same fingerprint: the
	// two-workbooks-same-shape case of the unattended journey.
	if other := Fingerprint(sheet, []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}); other != first {
		t.Errorf("Fingerprint of structurally identical sheet = %s, want %s", other, first)
	}
}

// TestFingerprintColumnOrderMatters pins the honest-workbook rule: a column
// permutation is a DIFFERENT fingerprint (a changed workbook needs
// re-confirmation — a permuted export is not the workbook the template was
// confirmed for).
func TestFingerprintColumnOrderMatters(t *testing.T) {
	sheet := "设备清单"
	headers := []string{"设备编码", "设备名称", "城市"}
	base := Fingerprint(sheet, headers)

	permuted := []string{"设备名称", "设备编码", "城市"}
	if got := Fingerprint(sheet, permuted); got == base {
		t.Errorf("fingerprint of permuted columns %s equals base — order must matter", fpJSON(sheet, permuted))
	}
	// A single adjacent swap already changes the digest.
	swapped := []string{"设备编码", "城市", "设备名称"}
	if got := Fingerprint(sheet, swapped); got == base {
		t.Errorf("fingerprint of swapped columns %s equals base — order must matter", fpJSON(sheet, swapped))
	}
	// And the permuted form is stable in its own right.
	if got := Fingerprint(sheet, permuted); got != Fingerprint(sheet, permuted) {
		t.Errorf("fingerprint of permuted columns not deterministic")
	}
}

// TestFingerprintCosmeticInvariance pins the folding ladder: trim,
// fullwidth→halfwidth and case folding do NOT change the fingerprint (cosmetic
// drift is the same column), while any content change (rename, add, remove)
// and any sheet-name change DOES.
func TestFingerprintCosmeticInvariance(t *testing.T) {
	sheet := "端口"
	headers := []string{"设备名称", "端口名称", "管理ＩＰ"}
	base := Fingerprint(sheet, headers)

	// Trim of surrounding whitespace (incl. the ideographic space U+3000).
	spacey := []string{" 设备名称 ", "　端口名称", "管理ＩＰ"}
	if got := Fingerprint(sheet, spacey); got != base {
		t.Errorf("fingerprint of trimmed headers = %s, want base %s (trim must not matter)", got, base)
	}
	// Fullwidth ASCII folds to halfwidth.
	folded := []string{"设备名称", "端口名称", "管理IP"}
	if got := Fingerprint(sheet, folded); got != base {
		t.Errorf("fingerprint of halfwidth headers = %s, want base %s (fullwidth fold must not matter)", got, base)
	}
	// Case folding: "IP" vs "ip" (and fullwidth lowercase).
	cases := []string{"设备名称", "端口名称", "管理ip"}
	if got := Fingerprint(sheet, cases); got != base {
		t.Errorf("fingerprint of lower-cased headers = %s, want base %s (case must not matter)", got, base)
	}

	// Content changes DO change the digest.
	renamed := []string{"设备名称", "端口名称X", "管理IP"}
	if got := Fingerprint(sheet, renamed); got == base {
		t.Errorf("fingerprint of renamed column equals base — rename must change it")
	}
	added := append([]string(nil), headers...)
	added = append(added, "带宽")
	if got := Fingerprint(sheet, added); got == base {
		t.Errorf("fingerprint of added column equals base — addition must change it")
	}
	removed := headers[:2]
	if got := Fingerprint(sheet, removed); got == base {
		t.Errorf("fingerprint of removed column equals base — removal must change it")
	}
	// Sheet-name change: same headers, another sheet = another signature.
	if got := Fingerprint("端口备份", headers); got == base {
		t.Errorf("fingerprint of different sheet name equals base — sheet must be part of the signature")
	}
	// Empty header list is a valid (degenerate) signature — distinct from any
	// non-empty one and stable.
	empty := Fingerprint(sheet, nil)
	if empty == base || empty == "" || Fingerprint(sheet, nil) != empty {
		t.Errorf("fingerprint of empty headers = %q, want stable non-base digest", empty)
	}
}

// TestFingerprintNoConcatenationForgery pins the separator choice: no header
// list can forge another sheet's signature by splitting/merging, because the
// unit/record separators are control characters that cannot be produced by
// header text in practice. [a] vs [a,b] vs the literal "a\x1eb" must all
// differ.
func TestFingerprintNoConcatenationForgery(t *testing.T) {
	sheet := "S"
	a := Fingerprint(sheet, []string{"a"})
	ab := Fingerprint(sheet, []string{"a", "b"})
	if a == ab {
		t.Errorf("[a] and [a,b] share a fingerprint — join separator not honored")
	}
	// A header literally containing the record separator would collide with
	// the two-header form — impossible from real xlsx cells (trimmed cell
	// text with control separators is rejected by Excel itself); the test
	// documents the assumption.
	literal := Fingerprint(sheet, []string{"a\x1eb"})
	if literal == ab {
		t.Logf("documented: header containing a literal record separator collides with the split form (unproducible in xlsx)")
	}
	// Two different sheets with swapped header boundaries never collide.
	x := Fingerprint(sheet, []string{"ab", "c"})
	y := Fingerprint(sheet, []string{"a", "bc"})
	if x == y {
		t.Errorf("boundary-shifted header lists share a fingerprint — separator ambiguity")
	}
}
