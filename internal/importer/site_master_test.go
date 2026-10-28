package importer

// site_master_test.go — the #38 import channel pin: the three master-data
// columns of a site-like sheet (基站/局站/机房) project onto the structured
// migration-v40 columns as English tokens, the attributes JSON keeps the
// raw sheet text (lossless), and an out-of-set value warns and leaves the
// column NULL — the row still imports (the RuleUnknownStatusValue
// precedent: never a row failure, never a guess).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"github.com/xuri/excelize/v2"
)

// importBaseStationSheet imports one synthesized 基站 sheet with the given
// master-data columns and answers the imported resource + the batch's
// row issues.
func importBaseStationSheet(t *testing.T, header []string, dataRows [][]string) (*repo.DB, domain.Resource, Summary) {
	t.Helper()
	dir := testsupport.TempDir(t)
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	// The 基站 SheetHint: a bare "Sheet1" would not map to the base_station
	// spec at all (the analyzer is hint-driven, never guess-driven).
	if err := f.SetSheetName(sheet, "基站表"); err != nil {
		t.Fatalf("SetSheetName: %v", err)
	}
	sheet = "基站表"
	row := make([]any, len(header))
	for i, h := range header {
		row[i] = h
	}
	_ = f.SetSheetRow(sheet, "A1", &row)
	for ri, r := range dataRows {
		cells := make([]any, len(r))
		for i, c := range r {
			cells[i] = c
		}
		axis, err := excelize.CoordinatesToCellName(1, ri+2)
		if err != nil {
			t.Fatalf("axis: %v", err)
		}
		_ = f.SetSheetRow(sheet, axis, &cells)
	}
	p := filepath.Join(dir, "stations.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}

	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := New(db)
	ctx := context.Background()
	a, err := svc.Analyze(ctx, p)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	summary, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, dataRows[0][0])
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	return db, res, summary
}

func TestSiteMasterDataProjection(t *testing.T) {
	_, res, sum := importBaseStationSheet(t,
		[]string{"基站编码", "无线基站名称", "站址性质", "供电方式", "生命周期"},
		[][]string{{"BS-MASTER-1", "归因一号站", "共享", "无", "在网"}})

	if res.SiteNature != "shared" {
		t.Fatalf("SiteNature = %q, want shared (共享 → shared token)", res.SiteNature)
	}
	if res.PowerMode != "none" {
		t.Fatalf("PowerMode = %q, want none (无 → none token)", res.PowerMode)
	}
	if res.Lifecycle != "active" {
		t.Fatalf("Lifecycle = %q, want active (在网 → active token)", res.Lifecycle)
	}
	// The status-field doctrine applies to the trio too: an enum value
	// lives ONLY in the structured column — the attributes JSON never
	// carries it (st.attributes skips KindEnum; the pre-#38 status field
	// behaves the same way, and consistency with it is the point).
	if _, ok := res.Attributes["site_nature"]; ok {
		t.Fatalf("enum values must not land in attributes (the status precedent): %v", res.Attributes)
	}
	// All three recognized values: no warnings at all.
	if sum.IssuesBySeverity[domain.SeverityWarning] != 0 {
		t.Fatalf("recognized values answered warnings: %v", sum.IssuesBySeverity)
	}
}

func TestSiteMasterDataOutOfSetValueWarnsAndStaysNull(t *testing.T) {
	_, res, sum := importBaseStationSheet(t,
		[]string{"基站编码", "无线基站名称", "站址性质", "供电方式", "生命周期"},
		[][]string{{"BS-MASTER-2", "口径外二号站", "铁塔站", "市电", "停用中"}})

	if res.SiteNature != "" || res.PowerMode != "" || res.Lifecycle != "" {
		t.Fatalf("out-of-set values must leave the columns NULL, got %q/%q/%q",
			res.SiteNature, res.PowerMode, res.Lifecycle)
	}
	// The out-of-set text lands NOWHERE (warned + not stored — the same
	// RuleUnknownStatusValue semantics as an unknown 状态): neither column
	// nor attributes. The warning IS the trace.
	if _, ok := res.Attributes["site_nature"]; ok {
		t.Fatalf("out-of-set values must not land in attributes: %v", res.Attributes)
	}
	if sum.RowsFailed != 0 || sum.RowsImported != 1 {
		t.Fatalf("out-of-set values must not fail the row: %+v", sum)
	}
	if sum.IssuesBySeverity[domain.SeverityWarning] < 3 {
		t.Fatalf("the three unknown values must warn (>=3 warning issues), got %v", sum.IssuesBySeverity)
	}
}
