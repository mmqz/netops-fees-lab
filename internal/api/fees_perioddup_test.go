package api_test

// fees_perioddup_test.go — issue #20's HTTP pins: the cross-period duplicate
// refusal is a 422 naming both periods + the batch id, the
// allowPeriodDuplicate form field is the explicit override, and (#24) the
// duplicate answer carries the COMPLETE field set — periodSource non-empty
// and equal to the batch row's value.
import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAPIFeesCrossPeriodDuplicate(t *testing.T) {
	srv := newAPITestServer(t)
	body := "站址编码,电费(元)\nHLJ-01,100.00\nHLJ-02,200.00\n"

	// First import: clean 201.
	resp, raw := uploadFeeFileWithExtra(t, srv, "dup.csv", body, "electricity", "2026", "1", "", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first import: status = %d\nbody: %s", resp.StatusCode, raw)
	}

	// Same bytes, other month: 422, both periods + the override hint.
	resp, raw = uploadFeeFileWithExtra(t, srv, "dup.csv", body, "electricity", "2026", "5", "", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("cross-period import: status = %d, want 422\nbody: %s", resp.StatusCode, raw)
	}
	for _, want := range []string{"2026-01", "2026-05", "allowPeriodDuplicate"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("422 body must contain %q: %s", want, raw)
		}
	}

	// The override proceeds: 201 + the audit warning + complete fields (#24).
	resp, raw = uploadFeeFileWithExtra(t, srv, "dup.csv", body, "electricity", "2026", "5",
		"allowPeriodDuplicate", "true")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("override import: status = %d\nbody: %s", resp.StatusCode, raw)
	}
	var sum struct {
		PeriodSource     string   `json:"periodSource"`
		WorkbookName     string   `json:"workbookName"`
		SkippedDuplicate bool     `json:"skippedDuplicate"`
		Warnings         []string `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, raw)
	}
	var audited bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "跨账期重复覆盖") {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("warnings = %v, want the cross-period override audit note", sum.Warnings)
	}

	// #24 duplicate answer: re-import under the SAME (overridden) period —
	// the idempotent duplicate is INFORMATIONAL (200 carries the existing
	// batch as the answer) and must carry the batch row's complete
	// provenance, not a hand-picked subset.
	resp, raw = uploadFeeFileWithExtra(t, srv, "dup.csv", body, "electricity", "2026", "5", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate import: status = %d, want 200 (informational)\nbody: %s", resp.StatusCode, raw)
	}
	var dup struct {
		PeriodSource     string `json:"periodSource"`
		WorkbookName     string `json:"workbookName"`
		Existing         string `json:"existingBatch"`
		SkippedDuplicate bool   `json:"skippedDuplicate"`
	}
	if err := json.Unmarshal(raw, &dup); err != nil {
		t.Fatalf("decode duplicate answer: %v\n%s", err, raw)
	}
	if dup.PeriodSource == "" || dup.WorkbookName == "" || dup.Existing == "" || !dup.SkippedDuplicate {
		t.Fatalf("duplicate answer = %+v — #24: the field set must be complete", dup)
	}
	if dup.PeriodSource != "manual" {
		t.Errorf("periodSource = %q, want the DB row's manual (no content evidence in this CSV)", dup.PeriodSource)
	}
}

// TestAPIFeesRiskMap pins #18's HTTP contract: parameter validation
// precedes the engine check, the envelope shape is stable, and the
// coordinate join answers honest nulls for geoless links.
func TestAPIFeesRiskMap(t *testing.T) {
	srv := newAPITestServer(t)

	// bad feeType -> 400 before any engine consideration.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/risk-map?feeType=bogus&year=2026", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad feeType: status = %d\nbody: %s", resp.StatusCode, body)
	}
	// bad year -> 400.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risk-map?feeType=electricity&year=1999", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad year: status = %d\nbody: %s", resp.StatusCode, body)
	}
	// default build (std engine) -> 501; duckdb build -> 200 envelope.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risk-map?feeType=electricity&year=2026&month=2", nil, "")
	if resp.StatusCode == http.StatusNotImplemented {
		t.Skip("default build: the 501 contract holds")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("risk-map: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var rep struct {
		FeeType string `json:"feeType"`
		Points  []struct {
			SiteCode  string   `json:"siteCode"`
			Lat       *float64 `json:"lat"`
			Scenarios []string `json:"scenarios"`
		} `json:"points"`
		LinkedSites int `json:"linkedSites"`
		GeoSites    int `json:"geoSites"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if rep.FeeType != "electricity" {
		t.Fatalf("feeType = %q", rep.FeeType)
	}
	// no geo resources seeded in this server: points may be empty or
	// geoless, but the counts must stay consistent.
	if rep.GeoSites > rep.LinkedSites {
		t.Fatalf("geoSites %d > linkedSites %d — impossible", rep.GeoSites, rep.LinkedSites)
	}
}
