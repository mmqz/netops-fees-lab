package importer

// Generic single-sheet seeding helpers shared by the importer tests
// (sourcerefversion, chain, importer). Extracted from the attribute-change
// test family: the device sheet shape and the analyze+execute one-two are
// test infrastructure, not business logic — the W005 proposal assertions
// themselves left with the governance channel.

import (
	"context"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
)

// attrChangeHeaders is the 7-column header of the synthetic device sheet.
var attrChangeHeaders = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}

// attrChangeMapping builds the confirmed device mapping for the sheet.
func attrChangeMapping(sheet string) mapping.Mapping {
	return mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet:        sheet,
		Action:       mapping.ActionImport,
		ResourceType: domain.ResourceTypeDevice,
		KeyColumns:   []string{"device_code"},
		ColumnMapping: map[string]string{
			"device_code": "设备编码",
			"device_name": "设备名称",
			"city":        "城市",
			"vendor":      "厂商",
			"model":       "型号",
			"mgmt_ip":     "管理IP",
			"status":      "状态",
		},
	}}}
}

// attrChangeRow builds one device data row.
func attrChangeRow(code, name, city, vendor, model, ip, status string) []string {
	return []string{code, name, city, vendor, model, ip, status}
}

// importSheet analyzes + executes one single-sheet workbook against the
// device mapping and returns the batch id and summary.
func importSheet(t *testing.T, svc *Service, rows [][]string) (string, Summary) {
	t.Helper()
	ctx := context.Background()
	path := writeWorkbookFile(t, "设备清单", rows)
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	sum, err := svc.Execute(ctx, a.BatchID, attrChangeMapping("设备清单"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return a.BatchID, sum
}
