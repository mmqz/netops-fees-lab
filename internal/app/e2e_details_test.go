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
