package mcpserver

// analyticstools_test.go — the #36 second+third batch e2e: a real MCP
// client gets the four analysis faces over pipe stdio, each against the
// REAL service state of a seeded session:
//
//   - fee_batches_list: the seeded import is visible with its counts and
//     the closed status set refuses outsiders (the HTTP twin's own
//     refusal text);
//   - fee_verify_batches: the report answers with never-null problem
//     arrays on ANY build, and the default build's dataChecksSkipped
//     honesty rides BOTH the field and the note;
//   - fee_details_list: a seeded detail row answers through the same
//     filter the HTTP face accepts, and the closed status set refuses;
//   - fee_cross_reconcile: the default build passes the honest
//     engine-not-built refusal through verbatim (the fee_risks ruling);
//     the -tags duckdb build answers the REAL merged table with the
//     #38 attribution histogram and the governed row cap naming itself.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestMCPServerFeeBatchesList pins the batch face: visibility, counts,
// the closed status set, and the governed bound.
func TestMCPServerFeeBatchesList(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	batch := seedFeeImport(t, db, dir, "b1.csv",
		"站址编码,账期,电费(元)\nS-BL,2026-07,100.00\n", 2026, 7, fees.FeeTypeElectricity)
	session := startSessionIn(t, db, dir)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: feeBatchesListName, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(fee_batches_list): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_batches_list answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want the one seeded batch (%v)", len(items), items)
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != batch || item["status"] != "completed" {
		t.Errorf("batch item = %v, want id=%s status=completed", item, batch)
	}
	if item["rowsImported"].(float64) != 1 {
		t.Errorf("rowsImported = %v, want 1", item["rowsImported"])
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "幂等") {
		t.Errorf("note = %q, want the 数据来源面 doctrine (幂等键口径)", note)
	}

	// the closed status set + the governed bound.
	if got := callToolError(t, session, feeBatchesListName, map[string]any{"status": "bogus"}); !strings.Contains(got, "completed / failed / rolled_back") {
		t.Errorf("status bogus error = %q, want the closed-set refusal", got)
	}
	if got := callToolError(t, session, feeBatchesListName, map[string]any{"limit": 0}); !strings.Contains(got, "limit 必须是") {
		t.Errorf("limit 0 error = %q, want the governed refusal", got)
	}
	// the status filter answers the filtered truth.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: feeBatchesListName, Arguments: map[string]any{"status": "failed"}})
	if err != nil {
		t.Fatalf("CallTool(fee_batches_list failed): %v", err)
	}
	out = toolResultJSON(t, res)
	if items, _ := out["items"].([]any); len(items) != 0 {
		t.Errorf("failed-filter items = %d, want 0", len(items))
	} else if items == nil {
		t.Errorf("failed-filter items = null, want [] (arrays are never null)")
	}
}

// TestMCPServerFeeVerifyBatches pins the self-check face: works on ANY
// build, never-null problem arrays, and the default build's honest
// dataChecksSkipped naming itself in BOTH the field and the note.
func TestMCPServerFeeVerifyBatches(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	seedFeeImport(t, db, dir, "v1.csv", "站址编码,账期,电费(元)\nS-VB,2026-07,100.00\n", 2026, 7, fees.FeeTypeElectricity)
	session := startSessionIn(t, db, dir)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: feeVerifyBatchesName, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(fee_verify_batches): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_verify_batches answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	if got, _ := out["batchesChecked"].(float64); got != 1 {
		t.Errorf("batchesChecked = %v, want 1 (the seeded completed batch)", got)
	}
	for _, key := range []string{"problems", "periodProblems", "dataProblems"} {
		if arr, _ := out[key].([]any); arr == nil {
			t.Errorf("%s = null, want [] (arrays are never null)", key)
		}
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "自检") || !strings.Contains(note, "待核实线索") {
		t.Errorf("note = %q, want the self-check doctrine (自检 + 待核实线索)", note)
	}
	if skip, _ := out["dataChecksSkipped"].(bool); skip {
		// the default build's honesty must name itself in the note too —
		// a silently skipped face is the #27 anti-pattern.
		if !strings.Contains(note, "未运行") {
			t.Errorf("dataChecksSkipped=true but the note never says so: %q", note)
		}
	}
}

// TestMCPServerFeeDetailsList pins the detail journal: a seeded row
// answers through the same filters the HTTP face accepts, the closed
// status set refuses, and the linkKey filter narrows.
func TestMCPServerFeeDetailsList(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	svc := newFeeServiceForTest(t, db, dir)
	if _, err := svc.AddDetail(context.Background(), fees.DetailInput{
		LinkKey: "S-DL", Period: "202607", FeeType: string(fees.FeeTypeElectricity),
		Category: "油费", AmountYuan: float64Ptr(88.5), ItemName: "巡检油费",
	}, "seed-operator"); err != nil {
		t.Fatalf("AddDetail: %v", err)
	}
	session := startSessionIn(t, db, dir)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: feeDetailsListName, Arguments: map[string]any{"linkKey": "S-DL"}})
	if err != nil {
		t.Fatalf("CallTool(fee_details_list): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_details_list answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want the seeded detail (%v)", len(items), items)
	}
	item, _ := items[0].(map[string]any)
	if item["linkKey"] != "S-DL" || item["status"] != domain.FeeDetailStatusPending {
		t.Errorf("detail item = %v, want linkKey=S-DL status=pending", item)
	}
	if got, _ := item["amountCents"].(float64); got != 8850 {
		t.Errorf("amountCents = %v, want 8850 (88.50 元, integer-cents contract)", got)
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "佐证而非权威") {
		t.Errorf("note = %q, want the #16 doctrine (佐证而非权威)", note)
	}

	// a linkKey nobody owns answers the honest empty array; the closed
	// status set and the governed bound refuse.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: feeDetailsListName, Arguments: map[string]any{"linkKey": "S-NONE"}})
	if err != nil {
		t.Fatalf("CallTool(fee_details_list, none): %v", err)
	}
	out = toolResultJSON(t, res)
	if items, _ := out["items"].([]any); items == nil || len(items) != 0 {
		t.Errorf("no-owner items = %v, want [] (never null)", items)
	}
	if got := callToolError(t, session, feeDetailsListName, map[string]any{"status": "bogus"}); !strings.Contains(got, "pending / confirmed / rejected / unmatched") {
		t.Errorf("status bogus error = %q, want the closed-set refusal", got)
	}
	if got := callToolError(t, session, feeDetailsListName, map[string]any{"feeType": "bogus"}); !strings.Contains(got, "feeType") {
		t.Errorf("feeType bogus error = %q, want the feeType refusal", got)
	}
	if got := callToolError(t, session, feeDetailsListName, map[string]any{"limit": 0}); !strings.Contains(got, "limit 必须是") {
		t.Errorf("limit 0 error = %q, want the governed refusal", got)
	}
}

// TestMCPServerFeeCrossReconcile pins the reconcile face: the default
// build passes the engine-not-built refusal through verbatim (the
// fee_risks/#103 ruling); the -tags duckdb build answers the REAL merged
// table with the #38 attribution histogram and the governed cap naming
// itself in the note.
func TestMCPServerFeeCrossReconcile(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	seedFeeImport(t, db, dir, "x-e.csv", "站址编码,账期,电费(元)\nS-XR,2026-08,120.00\n", 2026, 8, fees.FeeTypeElectricity)
	seedFeeImport(t, db, dir, "x-t.csv", "站址编码,账期,费用(元)\nS-XT,2026-08,300.00\n", 2026, 8, fees.FeeTypeTowerLease)
	session := startSessionIn(t, db, dir)
	ctx := context.Background()

	if !fees.Query(ctx).Available() {
		// default build: the honest refusal, verbatim (#103).
		got := callToolError(t, session, feeCrossReconcileName, map[string]any{"year": 2026, "month": 8})
		if !strings.Contains(got, "分析查询引擎未编译") {
			t.Errorf("default build error = %q, want the engine-not-built refusal", got)
		}
		// parameter discipline fires BEFORE the engine check (400-before-501).
		got = callToolError(t, session, feeCrossReconcileName, map[string]any{"year": 1999})
		if !strings.Contains(got, "2000–2100") {
			t.Errorf("year 1999 error = %q, want the range refusal", got)
		}
		return
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: feeCrossReconcileName,
		Arguments: map[string]any{"year": 2026, "month": 8}})
	if err != nil {
		t.Fatalf("CallTool(fee_cross_reconcile): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_cross_reconcile answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	if got, _ := out["onlyElectricity"].(float64); got != 1 {
		t.Errorf("onlyElectricity = %v, want 1 (S-XR)", got)
	}
	if got, _ := out["onlyTower"].(float64); got != 1 {
		t.Errorf("onlyTower = %v, want 1 (S-XT)", got)
	}
	// both rows stay unknown without master data — and the histogram SAYS so.
	hist, _ := out["attributionCounts"].(map[string]any)
	if got, _ := hist["unknown"].(float64); got != 2 {
		t.Errorf("attributionCounts[unknown] = %v, want 2 (no master data seeded)", got)
	}
	warnings, _ := out["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); strings.Contains(s, "2 行差异 attribution=unknown") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the unknown-count honesty notice", warnings)
	}
	rows, _ := out["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	row0, _ := rows[0].(map[string]any)
	if row0["class"] != "only_electricity" {
		t.Errorf("rows[0].class = %v, want only_electricity first (anomaly-first order)", row0["class"])
	}
	if row0["attribution"] != "unknown" {
		t.Errorf("rows[0].attribution = %v, want unknown", row0["attribution"])
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "归因口径（#38）") {
		t.Errorf("note = %q, want the attribution vocabulary", note)
	}

	// the governed cap: limit 1 keeps one row, names the trim, keeps the
	// FULL counts.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: feeCrossReconcileName,
		Arguments: map[string]any{"year": 2026, "month": 8, "limit": 1}})
	if err != nil {
		t.Fatalf("CallTool(fee_cross_reconcile, limit 1): %v", err)
	}
	out = toolResultJSON(t, res)
	if rows, _ := out["rows"].([]any); len(rows) != 1 {
		t.Errorf("capped rows = %d, want 1", len(rows))
	}
	if got, _ := out["rowsTotal"].(float64); got != 2 {
		t.Errorf("rowsTotal = %v, want 2 (the cap trims rows, never the counts)", got)
	}
	note, _ = out["note"].(string)
	if !strings.Contains(note, "截取前 1 行") {
		t.Errorf("capped note = %q, want the trim named", note)
	}
}

// newFeeServiceForTest composes the fee service over the SAME db+dir the
// session will use (the AddDetail seeding channel of the HTTP twin).
func newFeeServiceForTest(t *testing.T, db *repo.DB, dir string) *fees.Service {
	t.Helper()
	return services.New(db, services.WithFees(dir)).Fees
}

// float64Ptr is the test-side pointer helper the detail input wants.
func float64Ptr(v float64) *float64 { return &v }
