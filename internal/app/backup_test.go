package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// TestCLIBackup pins `netopsfees backup --db --out`: the snapshot is a
// byte-consistent, OPENABLE database carrying the source's data; the
// stdout contract is one JSON object {out, bytes}; --out in a
// not-yet-existing directory is created; a missing --out is a usage
// error (exit 2 path).
func TestCLIBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "netops.db")

	// Seed the source database with one resource (data the snapshot must
	// carry).
	a, err := New(Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	seedTS := time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC)
	if err := a.DB.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_bak", StartedAt: seedTS, Status: domain.BatchStatusCompleted,
	}); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := a.DB.Do(ctx, func(tx *repo.Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_bak", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-BAK",
			Name: "备份设备", Attributes: map[string]string{}, FirstBatchID: "bat_bak",
			CreatedAt: seedTS, UpdatedAt: seedTS,
		})
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	// backup into a not-yet-existing subdirectory (created on demand).
	out := filepath.Join(dir, "snapshots", "netops-snapshot.db")
	var stdout, stderr bytes.Buffer
	code := CLI([]string{"backup", "--db", dbPath, "--out", out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("backup exit = %d, stderr: %s", code, stderr.String())
	}
	var resp struct {
		Out   string `json:"out"`
		Bytes int64  `json:"bytes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if resp.Out != out || resp.Bytes <= 0 {
		t.Errorf("backup response = {out: %q, bytes: %d}, want the path and a positive size", resp.Out, resp.Bytes)
	}
	if info, err := os.Stat(out); err != nil || info.Size() != resp.Bytes {
		t.Errorf("stat snapshot: err=%v size=%d, want %d", err, infoSize(info), resp.Bytes)
	}

	// The snapshot opens and carries the source's rows (a backup that
	// cannot be restored is a tarball, not a backup).
	ba, err := New(Config{DBPath: out})
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = ba.Close() }()
	got, err := ba.Query.ResourceDetail(context.Background(), "res_bak")
	if err != nil {
		t.Fatalf("read snapshot resource: %v", err)
	}
	if got.NaturalKey != "DEV-BAK" || got.Name != "备份设备" {
		t.Errorf("snapshot resource = %+v, want the seeded DEV-BAK row", got)
	}

	// Missing --out: usage error (exit 2).
	stdout.Reset()
	stderr.Reset()
	code = CLI([]string{"backup", "--db", dbPath}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("backup without --out exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stdout.String(), "usage_error") {
		t.Errorf("usage error stdout = %q, want usage_error envelope", stdout.String())
	}
}

func infoSize(info os.FileInfo) int64 {
	if info == nil {
		return -1
	}
	return info.Size()
}
