package api_test

// Issue #7 and #8 regression tests:
//
//   - #7: negative / non-numeric limit and offset values must answer 400
//     bad_request with the accepted range (they used to be unknown query
//     names, silently ignored); limit/offset is now a real alternative
//     pagination interface (limit 1..100 clamped, offset 0-based) and mixing
//     it with page/pageSize is a 400.
//   - #8: GET /api/health carries dbPath (the server's absolute database
//     binding) and workspace 404 envelopes hint at the cross-database
//     mismatch — the one-look check that separates 连错库 from 数据丢失.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
)

// seededAPIServer is a newAPITestServer with the three canonical workbooks
// imported (64 resources — the §10 counts), the fixture the pagination
// semantics assertions page through.
func seededAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newAPITestServer(t)
	uploadAndConfirm(t, srv, "devices.xlsx",
		sheetExpectation{name: testfixtures.DeviceSheetName, rowCount: 30, columns: len(testfixtures.DeviceHeaders),
			header: "设备编码", field: "device_code", confidence: 0.75, firstSample: "DEV-001"},
		[4]int{30, 27, 0, 3}, map[string]int{"error": 4, "warning": 1, "info": 27})
	uploadAndConfirm(t, srv, "ippools.xlsx",
		sheetExpectation{name: testfixtures.IPPoolSheetName, rowCount: 20, columns: len(testfixtures.IPPoolHeaders),
			header: "CIDR", field: "cidr", confidence: 0.9, firstSample: "10.40.1.0/24"},
		[4]int{20, 16, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 12})
	uploadAndConfirm(t, srv, "circuits.xlsx",
		sheetExpectation{name: testfixtures.CircuitSheetName, rowCount: 25, columns: len(testfixtures.CircuitHeaders),
			header: "带宽Mbps", field: "bandwidth_mbps", confidence: 0.75, firstSample: "1000"},
		[4]int{25, 21, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 1})
	return srv
}

func TestPaginationLimitOffsetValidation(t *testing.T) {
	srv := newAPITestServer(t)
	cases := []struct {
		query       string
		wantStatus  int
		wantMessage string
	}{
		{"?limit=-1", http.StatusBadRequest, "limit"},
		{"?offset=-5", http.StatusBadRequest, "offset"},
		{"?limit=abc", http.StatusBadRequest, "limit"},
		{"?offset=abc", http.StatusBadRequest, "offset"},
		{"?limit=0", http.StatusBadRequest, "limit"},
		{"?page=1&offset=0", http.StatusBadRequest, "不能同时使用"},
		{"?pageSize=20&limit=5", http.StatusBadRequest, "不能同时使用"},
	}
	for _, tc := range cases {
		resp, body := doRequest(t, srv, http.MethodGet, "/api/resources"+tc.query, nil, "")
		if resp.StatusCode != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d (body %s)", tc.query, resp.StatusCode, tc.wantStatus, body)
			continue
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeJSON(t, tc.query, body, &env)
		if env.Error.Code != "bad_request" {
			t.Errorf("%s: code = %q, want bad_request", tc.query, env.Error.Code)
		}
		if !strings.Contains(env.Error.Message, tc.wantMessage) {
			t.Errorf("%s: message = %q, want it to mention %q", tc.query, env.Error.Message, tc.wantMessage)
		}
	}
}

// TestPaginationLimitOffsetSemantics pins the happy path: limit/offset pages
// exactly like page/pageSize (offset is 0-based, page = offset/limit + 1)
// and limit above MaxPageSize clamps to 100 like pageSize does.
func TestPaginationLimitOffsetSemantics(t *testing.T) {
	srv := seededAPIServer(t)

	// limit=5&offset=10 must be the third 5-row page.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?limit=5&offset=10", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("limit=5&offset=10: status = %d (body %s)", resp.StatusCode, body)
	}
	var page struct {
		Items    []map[string]any `json:"items"`
		Total    int              `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"pageSize"`
	}
	decodeJSON(t, "limit/offset page", body, &page)
	if page.PageSize != 5 || page.Page != 3 {
		t.Errorf("limit=5&offset=10 → (page %d, pageSize %d), want (3, 5)", page.Page, page.PageSize)
	}
	if len(page.Items) != 5 {
		t.Errorf("limit=5&offset=10 → %d items, want 5", len(page.Items))
	}
	if page.Total != 64 {
		t.Errorf("limit=5&offset=10 → total %d, want 64", page.Total)
	}

	// The same rows as the page interface: ?page=3&pageSize=5.
	resp, body2 := doRequest(t, srv, http.MethodGet, "/api/resources?page=3&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page=3&pageSize=5: status = %d", resp.StatusCode)
	}
	var pageRef struct {
		Items []map[string]any `json:"items"`
	}
	decodeJSON(t, "page reference", body2, &pageRef)
	if len(pageRef.Items) != len(page.Items) {
		t.Fatalf("limit/offset and page/pageSize disagree: %d vs %d items", len(page.Items), len(pageRef.Items))
	}
	for i := range page.Items {
		if page.Items[i]["id"] != pageRef.Items[i]["id"] {
			t.Errorf("row %d differs between interfaces: %v vs %v", i, page.Items[i]["id"], pageRef.Items[i]["id"])
		}
	}

	// Oversized limit clamps to MaxPageSize (100), mirroring pageSize.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?limit=99999", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("limit=99999: status = %d", resp.StatusCode)
	}
	decodeJSON(t, "clamped limit", body, &page)
	if page.PageSize != 100 {
		t.Errorf("limit=99999 → pageSize %d, want clamp 100", page.PageSize)
	}
}

// TestHealthCarriesDBPath pins issue #8's runtime visibility: /api/health
// answers the ABSOLUTE database path the server is bound to.
func TestHealthCarriesDBPath(t *testing.T) {
	srv := newAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: status = %d", resp.StatusCode)
	}
	var health struct {
		Status      string `json:"status"`
		Version     string `json:"version"`
		RuleVersion string `json:"ruleVersion"`
		DBPath      string `json:"dbPath"`
	}
	decodeJSON(t, "health", body, &health)
	if health.Status != "ok" || health.Version != domain.AppVersion {
		t.Errorf("health: status/version = %q/%q, want ok/%s", health.Status, health.Version, domain.AppVersion)
	}
	if health.DBPath == "" || !filepath.IsAbs(health.DBPath) {
		t.Errorf("health: dbPath = %q, want the absolute path of the bound database", health.DBPath)
	}
	if !strings.HasSuffix(health.DBPath, "netops.db") {
		t.Errorf("health: dbPath = %q, want it to end with the test database name", health.DBPath)
	}
}
