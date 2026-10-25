package api_test

// auth_audit_test.go — the privileged-op audit log of issue #9 方案 C
// (v0.18): every Admin-route SUCCESS emits one slog line ("audit: admin
// op") carrying the method, the path, the source address and the response
// status — the traceable record of a destructive operation in a
// shared-token world. Refusals (403) are NOT audited as privileged ops
// (nothing happened); they stay ordinary request-log lines.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/api"
	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestAdminOpAuditLog pins the audit line of a successful admin-route
// operation: a real batch delete (the issue's canonical destructive op)
// with the admin token must (a) succeed and (b) leave exactly one
// "audit: admin op" line in the server log naming the method, the path
// and the source address — while the declarer's 403 attempt on the same
// route leaves NO audit line.
func TestAdminOpAuditLog(t *testing.T) {
	dir := testsupport.TempDir(t)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// One real completed batch to delete.
	const batchID = "bat_i9_audit"
	ctx := context.Background()
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: batchID, WorkbookName: "audit.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RuleVersion: domain.RuleVersion,
		StartedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	var buf bytes.Buffer
	srv := api.New(slog.New(slog.NewTextHandler(&buf, nil)), services.New(db), dir)
	srv.AuthToken = "i9-audit-declarer"
	srv.AdminToken = "i9-audit-admin"
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	// The declarer's refused attempt: 403, and NO audit line afterwards.
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/batches/"+batchID, nil)
	if err != nil {
		t.Fatalf("build declarer request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer i9-audit-declarer")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("declarer delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("declarer delete: status = %d, want 403", resp.StatusCode)
	}
	if strings.Contains(buf.String(), "audit: admin op") {
		t.Fatalf("refused attempt produced an audit line:\n%s", buf.String())
	}

	// The admin's successful delete: 200 + the audit line.
	req, err = http.NewRequest(http.MethodDelete, ts.URL+"/api/batches/"+batchID, nil)
	if err != nil {
		t.Fatalf("build admin request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer i9-audit-admin")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin delete: status = %d, want 200", resp.StatusCode)
	}
	log := buf.String()
	if !strings.Contains(log, "audit: admin op") {
		t.Fatalf("successful admin op left no audit line:\n%s", log)
	}
	for _, want := range []string{"method=DELETE", "path=/api/batches/" + batchID, "status=200"} {
		if !strings.Contains(log, want) {
			t.Errorf("audit line missing %q:\n%s", want, log)
		}
	}
}
