package api

// docs_gate_test.go — the #33 doc gates: the verify-batches DQ face and
// the nightly workflow lines are CONTRACTS now (the close comment cites
// them, operators read them), so the docs that carry them are gated the
// same way facts.md is (#116's doctrine: the prose contract travels in
// the same change as the code it describes).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readDoc loads a docs file, skipping when the package is tested outside
// the repo tree (the facts_gate pattern).
func readDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Skipf("docs/%s not readable (package tested outside the repo tree): %v", name, err)
	}
	return string(raw)
}

// TestAPIDocCarriesDQFace gates api.md's verify-batches section: every DQ
// id the code emits (DQ1..DQ5) and both DQ5 severity bands must be
// documented — a new check added without its contract line fails here.
func TestAPIDocCarriesDQFace(t *testing.T) {
	doc := readDoc(t, "api.md")
	for _, id := range []string{"DQ1", "DQ2", "DQ3", "DQ4", "DQ5", "DQ6"} {
		if !strings.Contains(doc, id+"（") && !strings.Contains(doc, id+"/") {
			t.Errorf("api.md's verify-batches section no longer documents %s — the DQ map travels with the code", id)
		}
	}
	for _, kind := range []string{"single-site-code", "row-count-cliff", "majority-negative-amount"} {
		if !strings.Contains(doc, kind) {
			t.Errorf("api.md is missing the %q kind — the #33 checks must stay documented", kind)
		}
	}
}

// TestFeesLayerDocCarriesNightlyFaces gates fees-layer.md's CI prose: the
// nightly fuzz/bench workflows and the W3 staticcheck line are part of
// the build-form contract an operator reads before trusting the gates.
func TestFeesLayerDocCarriesNightlyFaces(t *testing.T) {
	doc := readDoc(t, "fees-layer.md")
	for _, needle := range []string{"staticcheck", "fuzz", "bench-gate", "GOOS=windows"} {
		if !strings.Contains(doc, needle) {
			t.Errorf("fees-layer.md's build-form section lost the %q line — the CI/nightly contract travels with the code", needle)
		}
	}
}
