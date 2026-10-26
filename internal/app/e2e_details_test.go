package app

// e2e_details_test.go — #16's completion line item 6: the detail layer's
// CLI lifecycle on the REAL binary (default build — the layer is
// SQLite-side, no engine needed): add → duplicate refusal → list →
// status (reason discipline) → reconcile (the honest engine-absent
// shape).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFeesDetailsLifecycle(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// add: 201-equivalent exit 0, the pending row on stdout.
	stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp, "details", "add",
		"--link-key", "W8522883", "--period", "202609", "--type", "maintenance",
		"--category", "低值易耗品", "--amount-yuan", "150.00",
		"--item-name", "卡钉、胶带", "--actor", "cli-zhang")
	if code != 0 {
		t.Fatalf("add exit = %d:\n%s", code, stdout)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(stdout), &item); err != nil {
		t.Fatalf("add stdout is not JSON: %s", stdout)
	}
	if item["status"] != "pending" || item["createdBy"] != "cli-zhang" || item["amountCents"].(float64) != 15000 {
		t.Fatalf("added = %v", item)
	}
	id := item["id"].(string)

	// the duplicate fingerprint: non-zero exit + refusal envelope.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "add",
		"--link-key", "W8522883", "--period", "202609", "--type", "maintenance",
		"--category", "低值易耗品", "--amount-yuan", "150.00",
		"--item-name", "卡钉、胶带", "--actor", "cli-zhang")
	if code == 0 {
		t.Fatalf("duplicate add must refuse, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "疑似重复明细") {
		t.Fatalf("duplicate refusal text = %s", stdout)
	}

	// the mandates: --amount-yuan missing = usage exit 2.
	if _, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "add",
		"--link-key", "W", "--period", "202609", "--type", "maintenance",
		"--category", "x", "--actor", "cli"); code != 2 {
		t.Errorf("missing amount exit = %d, want 2", code)
	}

	// list: the filter narrows.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "list",
		"--link-key", "W8522883", "--period", "202609")
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, stdout)
	}
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil || list.Total != 1 || list.Items[0]["id"] != id {
		t.Fatalf("list = %s", stdout)
	}

	// status: rejected without a reason refuses (non-zero).
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "status",
		"--id", id, "--to", "rejected", "--actor", "cli-admin")
	if code == 0 {
		t.Fatalf("reject without reason must refuse:\n%s", stdout)
	}
	// with the reason: the trail lands.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "status",
		"--id", id, "--to", "rejected", "--reason", "重复填报", "--actor", "cli-admin")
	if code != 0 {
		t.Fatalf("reject exit = %d:\n%s", code, stdout)
	}
	item = map[string]any{}
	if err := json.Unmarshal([]byte(stdout), &item); err != nil {
		t.Fatalf("status stdout is not JSON: %s", stdout)
	}
	if item["status"] != "rejected" || item["verifiedBy"] != "cli-admin" || item["rejectReason"] != "重复填报" {
		t.Fatalf("rejected = %v", item)
	}

	// reconcile: the honest engine-absent shape on the default build.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "details", "reconcile",
		"--link-key", "W8522883", "--period", "202609", "--type", "maintenance")
	if code != 0 {
		t.Fatalf("reconcile exit = %d:\n%s", code, stdout)
	}
	rec := map[string]any{}
	if err := json.Unmarshal([]byte(stdout), &rec); err != nil {
		t.Fatalf("reconcile stdout is not JSON: %s", stdout)
	}
	if rec["detailSumCents"].(float64) != 15000 || rec["rows"].(float64) != 1 {
		t.Fatalf("reconcile detail side = %v", rec)
	}
	if rec["systemCents"] != nil {
		t.Fatalf("default build must answer the honest absent system side: %v", rec)
	}
}

func TestCLIFeesAliasesLifecycle(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// add: the audited human confirm channel.
	stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "add",
		"--canonical", "HLJ-0001", "--alias", "hlj001", "--source", "tower_lease",
		"--confidence", "rule", "--actor", "cli-zhang")
	if code != 0 {
		t.Fatalf("add exit = %d:\n%s", code, stdout)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(stdout), &item); err != nil {
		t.Fatalf("add stdout is not JSON: %s", stdout)
	}
	if item["createdBy"] != "cli-zhang" || item["confidence"] != "rule" {
		t.Fatalf("added = %v", item)
	}
	id := item["id"].(string)

	// duplicate: refusal exit + text.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "add",
		"--canonical", "HLJ-0001", "--alias", "hlj001", "--source", "tower_lease",
		"--confidence", "manual", "--actor", "cli")
	if code == 0 || !strings.Contains(stdout, "映射已存在") {
		t.Fatalf("duplicate = exit %d, %s", code, stdout)
	}

	// list.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "list")
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, stdout)
	}
	var list struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil || list.Total != 1 {
		t.Fatalf("list = %s", stdout)
	}

	// scan + cross-reconcile on the DEFAULT build: the honest engine
	// refusals (exit non-zero, JSON error envelope naming the tag).
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "scan")
	var errOut map[string]any
	if json.Unmarshal([]byte(stdout), &errOut) != nil {
		t.Fatalf("scan stdout is not JSON: %s", stdout)
	}
	if _, ok := errOut["error"]; !ok && code == 0 {
		t.Fatalf("default-build scan must refuse honestly: %s", stdout)
	}
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "cross-reconcile",
		"--year", "2026", "--month", "8")
	if json.Unmarshal([]byte(stdout), &errOut) != nil {
		t.Fatalf("cross-reconcile stdout is not JSON: %s", stdout)
	}
	if _, ok := errOut["error"]; !ok && code == 0 {
		t.Fatalf("default-build cross-reconcile must refuse honestly: %s", stdout)
	}

	// usage discipline: missing --year.
	if _, code = runCLI(t, "fees", "--db", db, "--data", tmp, "cross-reconcile"); code != 2 {
		t.Errorf("missing year exit = %d, want 2", code)
	}

	// remove.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "remove", "--id", id)
	if code != 0 {
		t.Fatalf("remove exit = %d:\n%s", code, stdout)
	}
	// remove again: 404 semantics.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "aliases", "remove", "--id", id)
	if code == 0 || !strings.Contains(stdout, "not_found") {
		t.Fatalf("re-remove = exit %d, %s", code, stdout)
	}
}
