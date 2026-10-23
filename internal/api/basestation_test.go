package api_test

// Base-station seeding helper for the fee link-scan tests (fees_test):
// the 无线基站表 sheet shape + its confirmed mapping. Extracted from the
// renames test family when that channel left the fees repo — the sheet
// itself is resource-universe infrastructure.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// baseStationMappingJSON is the confirmed mapping of the 无线基站表 sheet.
const baseStationMappingJSON = `{"sheets":[
        {"sheet":"无线基站表","action":"import","resourceType":"base_station","keyColumns":["base_station_code"],
         "columnMapping":{"base_station_type":"基站类型","base_station_name":"无线基站名称","base_station_code":"基站编码","sharing":"电联共享","uplink_a_ip":"归属A IP","ring":"环","chain":"链","uplink_a_name":"归属A名称","uplink_a_port":"归属A端口","gateway_ip":"网关IP","base_station_ip":"基站IP","description":"描述","work_order":"工单号","remark":"备注"}}
]}`

func buildBaseStationWorkbook(t *testing.T, name, stationIP string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "无线基站表"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"基站类型", "无线基站名称", "基站编码", "电联共享", "归属A IP", "环", "链", "归属A名称", "归属A端口", "网关IP", "基站IP", "描述", "工单号", "备注"},
		{"宏基站", name, "", "是", "4.28.2.3", "环1", "链1", "枢纽楼A4", "gei_1/11", "12.17.10.126", stationIP, "BBU接入", "202366", ""},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("无线基站表", cell, v); err != nil {
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

// importBaseStationWorkbook uploads the workbook and confirms the
// base_station mapping.
func importBaseStationWorkbook(t *testing.T, srv *httptest.Server, name, stationIP string) {
	t.Helper()
	resp, body := uploadFile(t, srv, "stations.xlsx", buildBaseStationWorkbook(t, name, stationIP))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload", body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping",
		[]byte(baseStationMappingJSON), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping", body, &sum)
	if sum.Summary.RowsImported != 1 {
		t.Fatalf("import of %q = %+v, want 1 imported row", name, sum)
	}
}
