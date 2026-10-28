// evidencetools_test.go — the #36 first-batch acceptance e2e: a real MCP
// client (initialize → tools/list → tools/call over pipe stdio) gets
// STRUCTURED evidence out of the two new tools.
//
//   - fee_evidence raw mode: a seeded real import answers the snapshot's
//     original row (header + cells + the #39 note) on ANY build — the
//     evidence chain's third tier needs no engine;
//   - fee_evidence clue mode: on the default build the honest
//     engine-not-built refusal (the contract answer); on a -tags duckdb
//     build the REAL preset run — one site's spike clue with its row
//     anchors (the acceptance's own (scene, site, year, month) shape);
//   - fee_links_list: seeded confirmed/pending links, the code and status
//     filters, the governance bound, the note that keeps the states from
//     reading as verdicts.
package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// seedFeeImport writes and imports one electricity CSV through the REAL
// service composition (the same WithFees wiring startSession uses),
// answering the completed batch id.
func seedFeeImport(t *testing.T, db *repo.DB, dir, name, body string, year, month int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := services.New(db, services.WithFees(dir))
	summary, err := svc.Fees.Import(context.Background(), fees.ImportOptions{
		Path: p, FeeType: fees.FeeTypeElectricity, Year: year, Month: month,
	})
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	return summary.BatchID
}

// seedLink inserts one fee-site link row directly through the repo (the
// link-scan path needs engine data; the repo row IS the state the tool
// reads — the human confirm channel's post-state). The linked resource is
// seeded too: fee_site_links.resource_id carries a real FK (migration
// v30, ON DELETE CASCADE) and resources.first_batch_id references
// import_batches — the seed walks the REAL chain (batch → resource →
// link), never a fabricated id.
func seedLink(t *testing.T, db *repo.DB, code, id, status, grade string, similarity float64, decided bool) {
	t.Helper()
	ctx := context.Background()
	batID := "bat_" + id
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Batches.Create(ctx, domain.ImportBatch{
			ID: batID, WorkbookName: "links.xlsx", WorkbookSHA256: "sha_" + id,
			Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
		})
	}); err != nil {
		t.Fatalf("seed batch %s: %v", batID, err)
	}
	res := domain.Resource{
		ID: "res_" + id, Type: domain.ResourceTypeSite,
		NaturalKey: "KEY-" + code + "-" + id, Name: "资源 " + code, FirstBatchID: batID,
		Attributes: map[string]string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Resources.Insert(ctx, res); err != nil {
		t.Fatalf("insert resource %s: %v", res.ID, err)
	}
	l := domain.FeeSiteLink{
		ID: id, FeeSiteCode: code, ResourceID: res.ID,
		ResourceType: domain.ResourceTypeSite, ResourceKey: res.NaturalKey,
		Grade: domain.LinkGrade(grade), Status: domain.LinkStatus(status),
		Source: domain.LinkSourceAuto, Similarity: similarity,
		CreatedAt: time.Now().UTC(),
	}
	if decided {
		now := time.Now().UTC()
		l.DecidedAt = &now
	}
	if err := db.FeeSiteLinks.InsertProposal(context.Background(), l); err != nil {
		t.Fatalf("insert link %s: %v", id, err)
	}
}

// TestMCPServerFeeEvidenceRaw is the raw-mode acceptance: the MCP client
// resolves an anchor to the snapshot's original row — structured header +
// cells + the #39 note — on the default build too (pure file read).
func TestMCPServerFeeEvidenceRaw(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	batch := seedFeeImport(t, db, dir, "ev.csv",
		"站址编码,账期,电费(元)\nS-EV,2026-09,123.45\n", 2026, 9)
	session := startSessionIn(t, db, dir)

	// row 2 = the data row: header + cells + note, the structured contract.
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: feeEvidenceName,
		Arguments: map[string]any{
			"batch": batch, "row": 2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_evidence raw): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_evidence raw answered an error: %s", toolErrorText(t, res))
	}
	raw := toolResultJSON(t, res)
	if got, _ := raw["mode"].(string); got != "raw" {
		t.Errorf("mode = %v, want raw", got)
	}
	if got, _ := raw["batchId"].(string); got != batch {
		t.Errorf("batchId = %v, want %s", got, batch)
	}
	header, _ := raw["header"].([]any)
	if len(header) != 3 || header[0] != "站址编码" {
		t.Errorf("header = %v, want the 3-column CSV header", header)
	}
	cells, _ := raw["cells"].([]any)
	if len(cells) != 3 || cells[0] != "S-EV" || cells[2] != "123.45" {
		t.Errorf("cells = %v, want [S-EV 2026-09 123.45]", cells)
	}
	note, _ := raw["note"].(string)
	if !strings.Contains(note, "未经系统解释") {
		t.Errorf("note = %q, want the #39 raw-row doctrine (未经系统解释)", note)
	}

	// row 1 answers the header line itself (same 口径 as the anchors).
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: feeEvidenceName, Arguments: map[string]any{"batch": batch, "row": 1},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_evidence row 1): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_evidence row 1 answered an error: %s", toolErrorText(t, res))
	}
	raw = toolResultJSON(t, res)
	if cells, _ := raw["cells"].([]any); len(cells) == 0 || cells[0] != "站址编码" {
		t.Errorf("row 1 cells = %v, want the header line back", cells)
	}

	// a row past the end: the typed not-found is a CONTRACT answer.
	got := callToolError(t, session, feeEvidenceName, map[string]any{"batch": batch, "row": 99})
	if !strings.Contains(got, "不存在") {
		t.Errorf("row 99 error = %q, want the 行不存在 not-found", got)
	}

	// mode-shape validation, explicit over silent (#91).
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"batch": batch}, "row 必填"},
		{map[string]any{"row": 2}, "site 必填"},
		{map[string]any{"scenario": "spike", "feeType": "electricity", "year": 2026}, "site 必填"},
		{map[string]any{"scenario": "bogus", "feeType": "electricity", "year": 2026, "site": "S-EV"}, "场景"},
		{map[string]any{"scenario": "spike", "feeType": "bogus", "year": 2026, "site": "S-EV"}, "feeType"},
		{map[string]any{"scenario": "spike", "feeType": "electricity", "site": "S-EV"}, "year"},
		{map[string]any{"scenario": "spike", "feeType": "electricity", "year": 2026, "site": "S-EV", "month": 13}, "1–12"},
		{map[string]any{"scenario": "spike", "feeType": "electricity", "year": 2026, "site": "S-EV", "limit": 0}, "limit 必须是"},
	} {
		if got := callToolError(t, session, feeEvidenceName, tc.args); !strings.Contains(got, tc.want) {
			t.Errorf("fee_evidence(%v) error = %q, want containing %q", tc.args, got, tc.want)
		}
	}
}

// TestMCPServerFeeEvidenceClue runs the clue mode against the REAL preset:
// on a -tags duckdb build one site's spike clue comes back with its row
// anchors (the AI's step 2 of the analysis loop); on the default build the
// honest engine-not-built refusal passes through verbatim (the same
// contract fee_risks answers — mirrored from TestMCPFeeRisks).
func TestMCPServerFeeEvidenceClue(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := testsupport.TempDir(t)
	// a real spike: 100.00 -> 240.00 (+140%, over the 0.30 governed bar).
	seedFeeImport(t, db, dir, "m1.csv", "站址编码,账期,电费(元)\nS-SP,2026-01,100.00\n", 2026, 1)
	seedFeeImport(t, db, dir, "m2.csv", "站址编码,账期,电费(元)\nS-SP,2026-02,240.00\n", 2026, 2)
	session := startSessionIn(t, db, dir)
	ctx := context.Background()

	if !fees.Query(ctx).Available() {
		// default build: the refusal, every scenario — the engine behavior
		// is owned by the duckdb-tagged fees tests (the TestMCPFeeRisks
		// precedent).
		got := callToolError(t, session, feeEvidenceName, map[string]any{
			"scenario": "spike", "feeType": "electricity", "year": 2026, "site": "S-SP",
		})
		if !strings.Contains(got, "分析查询引擎未编译") {
			t.Errorf("clue mode on default build error = %q, want the engine-not-built refusal", got)
		}
		return
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: feeEvidenceName,
		Arguments: map[string]any{
			"scenario": "spike", "feeType": "electricity", "year": 2026, "site": "S-SP",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_evidence clue): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_evidence clue answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	if got, _ := out["mode"].(string); got != "clue" {
		t.Errorf("mode = %v, want clue", out["mode"])
	}
	if got, _ := out["site"].(string); got != "S-SP" {
		t.Errorf("site = %v, want S-SP", out["site"])
	}
	clues, _ := out["clues"].([]any)
	if len(clues) != 1 {
		t.Fatalf("clues = %d items, want the one S-SP spike clue (%v)", len(clues), clues)
	}
	clue, _ := clues[0].(map[string]any)
	if got, _ := clue["siteCode"].(string); got != "S-SP" {
		t.Errorf("clue siteCode = %v, want S-SP", clue["siteCode"])
	}
	anchors, _ := clue["evidence"].([]any)
	if len(anchors) == 0 {
		t.Fatalf("clue carries no evidence anchors — the tool's whole point")
	}
	anchor, _ := anchors[0].(map[string]any)
	for _, key := range []string{"sourceFile", "sourceRow", "importBatch", "amountCents"} {
		if _, ok := anchor[key]; !ok {
			t.Errorf("anchor missing %q: %v", key, anchor)
		}
	}
	if v, _ := anchor["amountCents"].(float64); v != 24000 {
		t.Errorf("anchor amountCents = %v, want 24000 (240.00 元)", anchor["amountCents"])
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "人工核实") {
		t.Errorf("note = %q, want the 待核实 doctrine", note)
	}

	// a site with no clue in the scenario answers an HONEST empty array
	// (never null — #93), never a fabricated row.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: feeEvidenceName,
		Arguments: map[string]any{
			"scenario": "spike", "feeType": "electricity", "year": 2026, "site": "S-NONE",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_evidence clue, no-clue site): %v", err)
	}
	if res.IsError {
		t.Fatalf("no-clue site answered an error: %s", toolErrorText(t, res))
	}
	out = toolResultJSON(t, res)
	if clues, _ := out["clues"].([]any); clues == nil {
		t.Errorf("clues = null for a no-clue site, want [] (arrays are never null)")
	} else if len(clues) != 0 {
		t.Errorf("clues = %v, want the empty array for S-NONE", clues)
	}
}

// TestMCPServerFeeLinksList is the links acceptance: the confirmed/pending
// states of one code answer with their lifecycle vocabulary, the status
// filter and the governed bound hold, and the note keeps the states from
// reading as verdicts (确认/驳回是人类通道).
func TestMCPServerFeeLinksList(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedLink(t, db, "S-LK", "lnk_conf", "confirmed", "exact", 0, true)
	seedLink(t, db, "S-LK", "lnk_pend", "pending", "fuzzy", 0.91, false)
	session := startSession(t, db)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: feeLinksListName, Arguments: map[string]any{"code": "S-LK"},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_links_list): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_links_list answered an error: %s", toolErrorText(t, res))
	}
	out := toolResultJSON(t, res)
	items, _ := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want the confirmed + pending pair (%v)", len(items), items)
	}
	statuses := map[string]bool{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		status, _ := item["status"].(string)
		statuses[status] = true
		if _, ok := item["grade"]; !ok {
			t.Errorf("item missing grade: %v", item)
		}
	}
	if !statuses["confirmed"] || !statuses["pending"] {
		t.Errorf("statuses = %v, want confirmed + pending", statuses)
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "待人工核实") || !strings.Contains(note, "非费用认定结论") {
		t.Errorf("note = %q, want the acceptance's own wording (待人工核实 + 非费用认定结论)", note)
	}

	// the status filter: the closed set passes, the outside refuses, and
	// the filtered answer is the filtered truth.
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: feeLinksListName, Arguments: map[string]any{"code": "S-LK", "status": "confirmed"},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_links_list confirmed): %v", err)
	}
	out = toolResultJSON(t, res)
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Errorf("confirmed filter items = %d, want 1", len(items))
	} else if item, _ := items[0].(map[string]any); item["status"] != "confirmed" {
		t.Errorf("filtered item status = %v, want confirmed", item["status"])
	}
	if got := callToolError(t, session, feeLinksListName, map[string]any{"code": "S-LK", "status": "bogus"}); !strings.Contains(got, "confirmed / pending / dismissed") {
		t.Errorf("status bogus error = %q, want the closed-set refusal", got)
	}

	// governance: limit < 1 refuses; no code answers the recent links.
	if got := callToolError(t, session, feeLinksListName, map[string]any{"limit": 0}); !strings.Contains(got, "limit 必须是") {
		t.Errorf("limit 0 error = %q, want the governed refusal", got)
	}
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: feeLinksListName, Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(fee_links_list, no code): %v", err)
	}
	if res.IsError {
		t.Fatalf("fee_links_list without code answered an error: %s", toolErrorText(t, res))
	}
	out = toolResultJSON(t, res)
	if items, _ := out["items"].([]any); len(items) < 2 {
		t.Errorf("no-code recent items = %d, want the seeded links visible", len(items))
	}
}
