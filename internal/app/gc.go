package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// GCResult reports one uploads garbage-collection run.
type GCResult struct {
	// Scanned is the number of entries found under <DataDir>/uploads.
	Scanned int `json:"scanned"`
	// Deleted is the number of orphan files successfully removed.
	Deleted int `json:"deleted"`
	// Referenced is the number of entries kept because some batch (any
	// status) references their workbook_path.
	Referenced int `json:"referenced"`
	// Errors is the number of removals that failed (logged, not fatal).
	Errors int `json:"errors"`
}

// GCOrphanUploads deletes the files under <DataDir>/uploads that NO import
// batch references (W5-D, v0.4.0). A batch's workbook_path is the reference
// set — for EVERY batch status: a completed batch re-reads the workbook for
// issue evidence, and even failed/rolled_back batches keep their reference
// (rollback deleted the rows, but the path is the audit record of what was
// imported; the operator may re-import from it). Only never-referenced
// uploads are orphans: crash debris of interrupted uploads (the analyzing
// crash window writes the file before the batch becomes durable) and stale
// re-uploads of the same content under different original names.
//
// Where it runs (and where it must NOT):
//   - serve startup, serially BEFORE the listener starts — no concurrent
//     upload can be in flight, so a file being written now cannot be
//     misjudged (the RecoverInterrupted precedent: cmd layer, own process);
//   - the explicit `netopslab gc --db --data` CLI subcommand (manual run).
//
// The MCP process never triggers it (mcp owns no serve loop); the HTTP API
// layer never triggers it either (no GC route — GC is a maintenance action
// of the process that owns the data directory).
//
// A missing uploads directory is a normal fresh install: 0/0, no error.
// Removal failures are logged (slog Warn) and counted, never fatal — GC
// must not take the server down over one sticky file.
func (a *App) GCOrphanUploads(ctx context.Context) (GCResult, error) {
	if a.Config.DataDir == "" {
		// Same fail-fast semantics as the upload route: a GC without a
		// data root has nothing to sweep (and a relative "uploads" would
		// be wrong anyway).
		return GCResult{}, fmt.Errorf("app: gc: dataDir is not configured")
	}

	refs, err := a.DB.Batches.ListWorkbookPaths(ctx)
	if err != nil {
		return GCResult{}, fmt.Errorf("app: gc: list workbook paths: %w", err)
	}
	// Every recorded path, in all comparison shapes: the cleaned path as
	// recorded (may be relative to the recording process's CWD), its
	// absolute form, and its base name. Uploads are content-addressed
	// (<sha256>_<name>.xlsx — globally unique by hash), so base-name
	// matching is the path-shape-independent identity here and errs on the
	// KEEP side (a kept false positive is a stale file; a deleted false
	// negative is data loss).
	refPaths := make(map[string]struct{}, 2*len(refs))
	refBases := make(map[string]struct{}, len(refs))
	for _, p := range refs {
		clean := filepath.Clean(p)
		refPaths[clean] = struct{}{}
		if abs, err := filepath.Abs(clean); err == nil {
			refPaths[abs] = struct{}{}
		}
		refBases[filepath.Base(clean)] = struct{}{}
	}

	uploadsDir := filepath.Join(a.Config.DataDir, "uploads")
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return GCResult{}, nil // fresh install, nothing uploaded yet
		}
		return GCResult{}, fmt.Errorf("app: gc: read uploads dir %s: %w", uploadsDir, err)
	}

	res := GCResult{Scanned: len(entries)}
	for _, entry := range entries {
		name := entry.Name()
		// Directories are not uploads; leave them alone (conservative).
		if entry.IsDir() {
			res.Referenced++
			continue
		}
		path := filepath.Join(uploadsDir, name)
		clean := filepath.Clean(path)
		abs := clean
		if absPath, err := filepath.Abs(clean); err == nil {
			abs = absPath
		}
		if _, ok := refBases[name]; ok {
			res.Referenced++
			continue
		}
		if _, ok := refPaths[clean]; ok {
			res.Referenced++
			continue
		}
		if _, ok := refPaths[abs]; ok {
			res.Referenced++
			continue
		}
		if err := os.Remove(path); err != nil {
			res.Errors++
			a.Log.Warn("gc: remove orphan upload failed", "path", path, "error", err.Error())
			continue
		}
		res.Deleted++
	}
	return res, nil
}

// gcResponse is the single JSON object the `netopslab gc` subcommand prints
// (the §10 stdout contract: exactly one JSON object).
type gcResponse struct {
	Scanned    int `json:"scanned"`
	Deleted    int `json:"deleted"`
	Referenced int `json:"referenced"`
	Errors     int `json:"errors"`
}

// cmdGC implements `netopslab gc --db --data`: manual uploads garbage
// collection (the serve-startup sweep, on demand). Success prints the GC
// counts as one JSON object on stdout; the n>0 count is ALSO slog-logged to
// stderr (info) so serve-style logs carry it.
func cmdGC(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("gc")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	data := fs.String("data", "./data", "上传文件存储目录（uploads 所在根目录）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "gc 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := New(Config{DBPath: *db, DataDir: *data})
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	res, err := a.GCOrphanUploads(context.Background())
	if err != nil {
		return err
	}
	if res.Deleted > 0 {
		a.Log.Info("gc: removed orphan uploads", "deleted", res.Deleted, "scanned", res.Scanned)
	}
	return writeJSON(stdout, gcResponse{
		Scanned:    res.Scanned,
		Deleted:    res.Deleted,
		Referenced: res.Referenced,
		Errors:     res.Errors,
	})
}
