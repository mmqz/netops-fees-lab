package api_test

// fees_period_test.go — issue #15's HTTP pins: the read-only period-probe
// endpoint answers the pre-import panel's data (kind/confidence/basis),
// the import channel enforces the three tiers (A mismatch → 422 with both
// values; override proceeds recorded), and the batch list carries the
// provenance fields.
import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// probeFeeFile posts the probe multipart to /api/fees/period-probe.
func probeFeeFile(t *testing.T, srv *httptest.Server, filename, content string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/fees/period-probe", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post probe: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return resp, body
}

// uploadFeeFileWithExtra posts the import multipart with one extra field
// (the #15 override channel).
func uploadFeeFileWithExtra(t *testing.T, srv *httptest.Server, filename, content, feeType, year, month, extraKey, extraVal string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for k, v := range map[string]string{"feeType": feeType, "year": year, "month": month} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if extraKey != "" {
		if err := mw.WriteField(extraKey, extraVal); err != nil {
			t.Fatalf("write %s: %v", extraKey, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/fees/imports", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post import: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return resp, body
}

func TestAPIFeesPeriodProbe(t *testing.T) {
	srv := newAPITestServer(t)
	// The 综维工资单 shape: the probe answers strong 2026-09 with the
	// evidence basis — nothing written, no declaration needed.
	payroll := "站址编码,员工工资上传批次,业务账期,1.8其他,金额\nHLJ-01,B-01,202609,5.00,10000\n"
	resp, body := probeFeeFile(t, srv, "payroll.csv", payroll)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var det struct {
		Kind       string `json:"kind"`
		Year       int    `json:"year"`
		Month      int    `json:"month"`
		Confidence string `json:"confidence"`
		Basis      string `json:"basis"`
	}
	if err := json.Unmarshal(body, &det); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, body)
	}
	if det.Kind != "payroll" || det.Confidence != "strong" || det.Year != 2026 || det.Month != 9 {
		t.Fatalf("probe detection = %+v", det)
	}
	if !strings.Contains(det.Basis, "业务账期") {
		t.Fatalf("probe basis = %q", det.Basis)
	}
	// A garbage file answers 422 (the reader's refusal), never a silent
	// anything.
	resp, body = probeFeeFile(t, srv, "bad.xls", "garbage")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad probe: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
}

func TestAPIFeesImportPeriodTiers(t *testing.T) {
	srv := newAPITestServer(t)
	payroll := "站址编码,员工工资上传批次,业务账期,1.8其他,金额\nHLJ-01,B-01,202608,5.00,10000\n"

	// A档 mismatch → 422 with BOTH values in the message.
	resp, body := uploadFeeFileWithExtra(t, srv, "payroll-08.csv", payroll, "maintenance", "2026", "9", "", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch import: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "2026-08") || !strings.Contains(string(body), "2026-09") {
		t.Fatalf("422 body must carry both values: %s", body)
	}
	// The override proceeds: 201 + unverified provenance.
	resp, body = uploadFeeFileWithExtra(t, srv, "payroll-08.csv", payroll, "maintenance", "2026", "8", "", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("corrected import: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var sum struct {
		PeriodSource string `json:"periodSource"`
		PeriodBasis  string `json:"periodBasis"`
	}
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, body)
	}
	if sum.PeriodSource != "auto" {
		t.Fatalf("A档 match periodSource = %q, want auto", sum.PeriodSource)
	}
	// The batch list exposes the provenance.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/batches?status=completed", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batches: %d", resp.StatusCode)
	}
	var list struct {
		Items []struct {
			PeriodSource string `json:"periodSource"`
			PeriodBasis  string `json:"periodBasis"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode batches: %v\n%s", err, body)
	}
	if len(list.Items) != 1 || list.Items[0].PeriodSource != "auto" || list.Items[0].PeriodBasis == "" {
		t.Fatalf("batch list must expose periodSource/periodBasis: %s", body)
	}
}
