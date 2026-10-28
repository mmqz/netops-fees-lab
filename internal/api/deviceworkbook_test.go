package api_test

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

// deviceImportMapping is the confirmed mapping JSON of the device sheet.
const deviceImportMapping = `{"sheets":[
        {"sheet":"设备清单","action":"import","resourceType":"device","keyColumns":["device_code"],
         "columnMapping":{"device_code":"设备编码","device_name":"设备名称","city":"城市","vendor":"厂商","model":"型号","mgmt_ip":"管理IP","status":"状态"}}
]}`

// buildDeviceWorkbook synthesizes the one-row 设备清单 workbook the sourceref
// tests import (extracted from the attribute-change test family with that
// channel — the sheet shape is universe-seeding infrastructure).

func buildDeviceWorkbook(t *testing.T, ip string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
		{"DEV-001", "核心路由1", "杭州", "华为", "NE40E", ip, "在网"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("设备清单", cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return buf.Bytes()
}
