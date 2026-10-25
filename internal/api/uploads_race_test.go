package api_test

// The #112 pins: the resource workbook channel (POST /api/imports) and the
// metrics channel (POST /api/metrics/imports) are the same upload race
// family #106 fixed for the fee channel — both stored user bytes in place
// with O_TRUNC under a content-addressed name
// (uploads/<sha256>_<name>), so two concurrent submissions of the SAME
// content truncated and rewrote one file while the other request's
// read-back was re-hashing it: the batch row could record a SHA256 that
// does not match the bytes on disk (the evidence hash lied) and the GC's
// base-name keep-set could then vouch for a file the recorded hash never
// described. Both channels now stage through storeUpload
// (internal/api/uploads.go): per-request ".tmp-*" temp + atomic rename —
// concurrent racers replace each other with whole identical files and no
// reader ever observes a partial one. These tests hold the fix in place
// with the TestAPIFeesConcurrentSameUpload harness (#106's own
// discriminative pattern — several rounds, each a fresh simultaneous
// double-submission, because the pre-fix interleaving is probabilistic).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/metrics"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// raceResult is one request's outcome of the concurrent-upload harness.
type raceResult struct {
	status int
	body   []byte
}

// raceUpload fires n identical multipart bodies at endpoint simultaneously
// and returns the per-request outcomes in firing order. Only Errorf fires
// inside the goroutines (goroutine-safe); FailNow stays with the test
// goroutine — the TestAPIFeesConcurrentSameUpload harness, shared by the
// #112 pins so all three channels of the family test the same way.
func raceUpload(t *testing.T, srv *httptest.Server, endpoint, contentType string, body []byte, n int) []raceResult {
	t.Helper()
	start := make(chan struct{})
	results := make([]raceResult, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req, rerr := http.NewRequest(http.MethodPost, srv.URL+endpoint, bytes.NewReader(body))
			if rerr != nil {
				t.Errorf("build request: %v", rerr)
				return
			}
			req.Header.Set("Content-Type", contentType)
			resp, rerr := http.DefaultClient.Do(req)
			if rerr != nil {
				t.Errorf("post: %v", rerr)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, rerr := io.ReadAll(resp.Body)
			if rerr != nil {
				t.Errorf("read body: %v", rerr)
				return
			}
			results[i].status = resp.StatusCode
			results[i].body = b
		}(i)
	}
	close(start)
	wg.Wait()
	return results
}

// buildUploadMultipart serializes one multipart body (form field "file"
// plus optional extra fields) and returns its content type — a body built
// once can be posted by several goroutines, each with its own reader.
func buildUploadMultipart(t *testing.T, filename string, content []byte, fields map[string]string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return mw.FormDataContentType(), buf.Bytes()
}

// assertNoStagingTempLeak fails the test if any ".tmp-*" staging temp
// remains under <dir>/uploads — every storeUpload rename must consume its
// temp file, success or failure.
func assertNoStagingTempLeak(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatalf("read uploads dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("staging temp %s leaked after the concurrent uploads", e.Name())
		}
	}
}

// buildRaceWorkbook generates one ~400KB multi-thousand-row device
// workbook (8000 rows — the pre-#112 torn-file interleaving happened at
// write-chunk granularity and the catchable window is the store write
// overlapping another request's read-back, so the surface must both span
// many 32KB chunks and keep the write slow enough to actually overlap).
// round is embedded in every row so each round stores a distinct
// content-addressed file.
func buildRaceWorkbook(t *testing.T, round int) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "网元设备"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	headers := []string{"设备编码", "设备名称", "厂商", "型号", "管理IP", "状态", "地市", "描述"}
	if err := f.SetSheetRow("网元设备", "A1", &headers); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	for r := 0; r < 8000; r++ {
		cell, err := excelize.CoordinatesToCellName(1, r+2)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		row := []string{
			fmt.Sprintf("DEV-R%02d-%05d", round, r),
			fmt.Sprintf("竞态设备%02d-%05d", round, r),
			"华为", "CX600",
			fmt.Sprintf("10.%d.%d.%d", round, r/250%250, r%250+1),
			"在用",
			fmt.Sprintf("地市%03d", r%20),
			fmt.Sprintf("并发双提交竞态测试行 %02d-%05d", round, r),
		}
		if err := f.SetSheetRow("网元设备", cell, &row); err != nil {
			t.Fatalf("write row: %v", err)
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

// TestAPIImportConcurrentSameUpload pins #112 for the resource workbook
// channel (POST /api/imports): four CONCURRENT submissions of the same
// content (and name) must leave the stored upload byte-honest — the file
// at uploads/<sha256>_<name> hashes to exactly the SHA256 all analysis
// responses record, and each batch row's workbook_path (the evidence
// chain AND the GC keep-set's base-name identity) points at those bytes.
// The analyze step re-reads the stored path to record that hash, so
// under the pre-#112 in-place O_TRUNC write the loser of the race could
// record a hash the disk bytes never matched. Each round is a fresh race
// with fresh content; the resource channel has no same-content dedup, so
// every request creates its own batch and all answer 202. Four
// simultaneous posts (denser than the fees pair) because this channel's
// read-back window is one bulk read at analyze start — the overlap odds
// scale with the pairwise schedule spread, and 6 rounds × 4 posts
// multiply them.
func TestAPIImportConcurrentSameUpload(t *testing.T) {
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(func() {
		srv.Close()
		_ = a.Close()
	})

	name := "race-devices.xlsx"
	for round := 0; round < 6; round++ {
		content := buildRaceWorkbook(t, round)
		sum := sha256.Sum256(content)
		contentSHA := hex.EncodeToString(sum[:])
		contentType, body := buildUploadMultipart(t, name, content, nil)

		results := raceUpload(t, srv, "/api/imports", contentType, body, 4)
		for i, r := range results {
			if r.status != http.StatusAccepted {
				t.Fatalf("round %d: concurrent upload %d status = %d (want 202 — a torn store must fail loudly, never pass)\nbody: %s",
					round, i, r.status, r.body)
			}
			var an importAnalysis
			decodeJSON(t, fmt.Sprintf("round %d upload %d", round, i), r.body, &an)
			if an.SHA256 != contentSHA {
				t.Errorf("round %d: upload %d recorded sha256 = %s, want the content hash %s — the evidence hash must never lie (#112)",
					round, i, an.SHA256, contentSHA)
			}
			// The evidence chain: workbook_path must point at bytes that
			// hash to the recorded SHA256 — the GC's base-name matching
			// vouches for exactly this identity.
			wp, err := a.DB.Batches.WorkbookPath(context.Background(), an.BatchID)
			if err != nil {
				t.Fatalf("round %d: workbook path of batch %s: %v", round, an.BatchID, err)
			}
			if base := filepath.Base(wp); base != contentSHA+"_"+name {
				t.Errorf("round %d: upload %d workbook_path base = %q, want the content-addressed %q", round, i, base, contentSHA+"_"+name)
			}
			if got := hashFile(t, wp); got != contentSHA {
				t.Errorf("round %d: upload %d workbook at %s hashes to %s, want %s — the path the batch records must carry the bytes the hash claims",
					round, i, wp, got, contentSHA)
			}
		}
		// The file both requests targeted (same content → one
		// content-addressed name): whole identical bytes.
		stored := filepath.Join(dir, "uploads", contentSHA+"_"+name)
		if got := hashFile(t, stored); got != contentSHA {
			t.Errorf("round %d: stored upload hashes to %s, want %s", round, got, contentSHA)
		}
	}

	assertNoStagingTempLeak(t, dir)
}

// TestAPIMetricsConcurrentSameUpload pins #112 for the metrics channel
// (POST /api/metrics/imports): two CONCURRENT submissions of the same
// content (and name) must leave the stored upload byte-honest — the file
// at uploads/<sha256>_<name> hashes to exactly the SHA256 both batch rows
// record. The ingest re-reads the stored path to record the batch's
// WorkbookSHA256 BEFORE any row is processed, so the store race is pinned
// exactly where it lives; the workbook's rows deliberately resolve against
// an EMPTY resource table (every row fails resolution, the ingest
// completes with rowsFailed — the metrics.go doctrine: the stored upload
// stays content-addressed and the next `netopsfees gc` collects it), which
// keeps this pin light while exercising the full store → read-back →
// hash-record path. Same content every round is deliberate: the metrics
// channel has no same-content dedup, every POST is a fresh batch, and all
// rounds target the one content-addressed file. Four simultaneous posts
// (the imports pin's density — the ingest's read-back window is one bulk
// read at ingest start, and the overlap odds scale with the pairwise
// schedule spread).
func TestAPIMetricsConcurrentSameUpload(t *testing.T) {
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(func() {
		srv.Close()
		_ = a.Close()
	})

	// One big synthetic perf workbook (~140KB, 1600 rows × the whole
	// metric catalog): the store write spans many 32KB chunks — the
	// surface the pre-#112 in-place O_TRUNC write interleaved at.
	synth := testsupport.TempDir(t)
	if _, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out:      synth,
		Devices:  800,
		Ports:    2,
		Days:     1,
		Scenario: metrics.ScenarioNormal,
		Seed:     42,
		Metrics:  0, // the whole closed catalog
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(synth, "perf-day-01.xlsx"))
	if err != nil {
		t.Fatalf("read perf workbook: %v", err)
	}
	sum := sha256.Sum256(content)
	contentSHA := hex.EncodeToString(sum[:])

	// The suggested mapping comes from the analyze endpoint, which rides
	// the same fixed store and then removes its scratch copy (the analyze
	// channel is stateless). A DIFFERENT display name keeps the scratch
	// copy's content-addressed path disjoint from the import rounds'
	// target, so the analyze channel's deferred remove can never race a
	// round's rename.
	analyzeType, analyzeBody := buildUploadMultipart(t, "perf-suggest.xlsx", content, nil)
	ar := raceUpload(t, srv, "/api/metrics/analyze", analyzeType, analyzeBody, 1)
	if ar[0].status != http.StatusOK {
		t.Fatalf("analyze: status = %d, want 200\nbody: %s", ar[0].status, ar[0].body)
	}
	var analysis metrics.IngestAnalysis
	decodeJSON(t, "analyze", ar[0].body, &analysis)
	if analysis.SuggestedMapping.TSField == "" || len(analysis.SuggestedMapping.Metrics) == 0 {
		t.Fatalf("analyze: suggested mapping incomplete: %+v", analysis.SuggestedMapping)
	}
	mappingJSON, err := json.Marshal(analysis.SuggestedMapping)
	if err != nil {
		t.Fatalf("marshal mapping: %v", err)
	}

	name := "perf-day-01.xlsx"
	var batchIDs []string
	for round := 0; round < 6; round++ {
		contentType, mbody := buildUploadMultipart(t, name, content, map[string]string{"mapping": string(mappingJSON)})
		results := raceUpload(t, srv, "/api/metrics/imports", contentType, mbody, 4)
		for i, r := range results {
			if r.status != http.StatusAccepted {
				t.Fatalf("round %d: concurrent metric import %d status = %d (want 202 — a torn store must fail loudly, never pass)\nbody: %s",
					round, i, r.status, r.body)
			}
			var imp struct {
				BatchID string `json:"batchId"`
			}
			decodeJSON(t, fmt.Sprintf("round %d import %d", round, i), r.body, &imp)
			if imp.BatchID == "" {
				t.Fatalf("round %d: import %d returned no batchId\nbody: %s", round, i, r.body)
			}
			batchIDs = append(batchIDs, imp.BatchID)
		}
		// The file both requests targeted: whole identical bytes.
		stored := filepath.Join(dir, "uploads", contentSHA+"_"+name)
		if got := hashFile(t, stored); got != contentSHA {
			t.Errorf("round %d: stored metric upload hashes to %s, want %s", round, got, contentSHA)
		}
	}

	// Every batch's recorded evidence SHA256 == the content hash: the
	// ingest re-read the stored path, and with the pre-#112 in-place write
	// a torn read would have recorded a lying hash.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/metrics/batches?pageSize=100", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metric batches list: status = %d\n%s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"items"`
	}
	decodeJSON(t, "metric batches", body, &list)
	shas := make(map[string]string, len(list.Items))
	for _, it := range list.Items {
		shas[it.ID] = it.SHA256
	}
	for _, id := range batchIDs {
		if shas[id] != contentSHA {
			t.Errorf("metric batch %s recorded sha256 = %s, want the content hash %s — the evidence hash must never lie (#112)", id, shas[id], contentSHA)
		}
	}

	// No staging temp leaks, and the analyze channel's scratch copy is
	// gone: only the one content-addressed import file remains.
	entries, err := os.ReadDir(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatalf("read uploads dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("staging temp %s leaked after the concurrent uploads", e.Name())
			continue
		}
		if e.Name() != contentSHA+"_"+name {
			t.Errorf("unexpected uploads entry %s — the analyze channel's scratch copy must be removed and every import must share the one content-addressed name", e.Name())
		}
	}
}

// TestAPIImportConcurrentSameUploadSustained pins #126's acceptance bar:
// "连跑 20 次并发上传，必须 20/20 全绿——单次通过不代表修好". The v0.16.1
// Windows evidence is the point: the first #124 fix turned 5/5 failures into
// 4/5 — the fifth run passed by luck, which is exactly why one lucky green
// round proves nothing. Twenty consecutive FRESH races (distinct content per
// round, so every round stages a target that does not exist yet — the
// rename-collision window at its widest), each with the full 202 +
// evidence-hash + content-addressed-path + no-temp-leak assertion set of
// the 6-round pin above. The in-process stripe serialization (#126) makes
// every loser take the idempotent fast path, so a green round here is
// deterministic rather than probabilistic — but the test keeps its guard:
// any regression back to probabilistic tolerance (a retry loop quietly
// removed, a stripe narrowed) resurfaces here first.
func TestAPIImportConcurrentSameUploadSustained(t *testing.T) {
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(func() {
		srv.Close()
		_ = a.Close()
	})

	name := "race-sustained.xlsx"
	for round := 0; round < 20; round++ {
		content := buildRaceWorkbook(t, 100+round) // fresh target every round
		sum := sha256.Sum256(content)
		contentSHA := hex.EncodeToString(sum[:])
		contentType, body := buildUploadMultipart(t, name, content, nil)

		results := raceUpload(t, srv, "/api/imports", contentType, body, 4)
		for i, r := range results {
			if r.status != http.StatusAccepted {
				t.Fatalf("round %d: concurrent upload %d status = %d (want 202 — %d/%d rounds green)\nbody: %s",
					round, i, r.status, round, 20, r.body)
			}
			var an importAnalysis
			decodeJSON(t, fmt.Sprintf("round %d upload %d", round, i), r.body, &an)
			if an.SHA256 != contentSHA {
				t.Errorf("round %d: upload %d recorded sha256 = %s, want the content hash %s — the evidence hash must never lie (#112)",
					round, i, an.SHA256, contentSHA)
			}
			wp, err := a.DB.Batches.WorkbookPath(context.Background(), an.BatchID)
			if err != nil {
				t.Fatalf("round %d: workbook path of batch %s: %v", round, an.BatchID, err)
			}
			if base := filepath.Base(wp); base != contentSHA+"_"+name {
				t.Errorf("round %d: upload %d workbook_path base = %q, want the content-addressed %q", round, i, base, contentSHA+"_"+name)
			}
			if got := hashFile(t, wp); got != contentSHA {
				t.Errorf("round %d: upload %d workbook at %s hashes to %s, want %s", round, i, wp, got, contentSHA)
			}
		}
		stored := filepath.Join(dir, "uploads", contentSHA+"_"+name)
		if got := hashFile(t, stored); got != contentSHA {
			t.Errorf("round %d: stored upload hashes to %s, want %s", round, got, contentSHA)
		}
	}

	assertNoStagingTempLeak(t, dir)
}
