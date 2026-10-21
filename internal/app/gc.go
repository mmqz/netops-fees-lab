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
	// Scanned is the number of entries judged under <DataDir>/uploads plus
	// the fee staging subdir <DataDir>/uploads/fees (the fees directory
	// itself is a container, descended into, not judged).
	Scanned int `json:"scanned"`
	// Deleted is the number of orphan files successfully removed.
	Deleted int `json:"deleted"`
	// Referenced is the number of entries kept: files some batch (any
	// status) references via its workbook_path, fee staging files some
	// fee batch derives, plus directories other than uploads/fees
	// (conservative skip).
	//
	// Interrupted staging temps (".tmp-*", the per-request temp names of
	// storeUpload — internal/api/uploads.go) are orphans in BOTH areas
	// (#112 for the flat dir, #106-B for uploads/fees): a temp name is
	// never recorded in workbook_path nor derivable from a fee batch, so
	// it is never referenced. Serve startup (the primary trigger) is
	// unambiguous — the sweep runs before the listener, no request of THIS
	// process can be mid-write, so any temp found is crash debris. A
	// manual `netopslab gc` overlapping a live serve MAY collect an
	// in-flight request's fresh temp — the upload then fails loudly
	// (rename ENOENT → 500, the operator retries); it can never produce a
	// torn file or a lying hash: the same accepted window the fee staging
	// sweep already has (its test seeds a FRESH temp and pins deletion).
	// Fresh temps are therefore NOT grace-protected here, unlike the
	// Parquet tree (internal/fees GCAnalysis), where deleting a fresh
	// orphan would SILENTLY lose rows of a completed batch — loud failure
	// vs silent loss is the doctrinal line.
	Referenced int `json:"referenced"`
	// Errors is the number of removals that failed (logged, not fatal).
	Errors int `json:"errors"`
}

// GCOrphanUploads deletes the files under <DataDir>/uploads that NO import
// batch references (W5-D, v0.4.0), plus — #106-B — the fee channel's staging
// files under <DataDir>/uploads/fees. A resource batch's workbook_path is
// the reference set of the flat uploads dir — for EVERY batch status: a
// completed batch re-reads the workbook for issue evidence, and even
// failed/rolled_back batches keep their reference (rollback deleted the
// rows, but the path is the audit record of what was imported; the
// operator may re-import from it). The fee staging area gets the same
// doctrine: every HTTP fee import lands its upload content-addressed at
// uploads/fees/<sha256>_<name> while the snapshot layer keeps its own
// durable copy — the staging copy is the transport artifact, and a fee
// batch of ANY status whose (sha256, workbook_name) derives the file's
// name is the audit record that keeps it. Only never-referenced entries
// are orphans: crash debris of interrupted uploads (the analyzing crash
// window writes the file before the batch becomes durable), pre-batch
// refusals, stale re-uploads of the same content under different original
// names, and interrupted staging temps (".tmp-*" — the resource and
// metrics channels stage in the flat dir since #112, the fee channel
// has staged in uploads/fees since #106; see the GCResult.Referenced
// note for the concurrency window a manual gc run must tolerate).
// Pre-#106 the whole fees directory
// was skipped as "a directory" and accumulated forever (every HTTP fee
// import stores the file TWICE: staging + snapshot).
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

	res := GCResult{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			// Directories are not uploads; leave them alone (conservative)
			// — EXCEPT uploads/fees, the fee channel's staging area: it is
			// descended into and judged by its own batch-referenced sweep
			// right below (#106-B).
			if name == "fees" {
				continue
			}
			res.Scanned++
			res.Referenced++
			continue
		}
		path := filepath.Join(uploadsDir, name)
		clean := filepath.Clean(path)
		abs := clean
		if absPath, err := filepath.Abs(clean); err == nil {
			abs = absPath
		}
		res.Scanned++
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

	// The fee staging sweep (#106-B): the keep-set is the batch-derived
	// content-addressed names; everything else under uploads/fees is
	// orphan debris (interrupted staging temps included).
	feeRefs, err := a.feeStagingRefs(ctx)
	if err != nil {
		return GCResult{}, err
	}
	feesDir := filepath.Join(uploadsDir, "fees")
	feeEntries, ferr := os.ReadDir(feesDir)
	if ferr != nil && !os.IsNotExist(ferr) {
		return GCResult{}, fmt.Errorf("app: gc: read fee uploads dir %s: %w", feesDir, ferr)
	}
	for _, entry := range feeEntries {
		res.Scanned++
		name := entry.Name()
		// Directories are not staged uploads; leave them alone
		// (conservative, same as the flat dir above).
		if entry.IsDir() {
			res.Referenced++
			continue
		}
		if _, ok := feeRefs[name]; ok {
			res.Referenced++
			continue
		}
		path := filepath.Join(feesDir, name)
		if err := os.Remove(path); err != nil {
			res.Errors++
			a.Log.Warn("gc: remove orphan fee upload failed", "path", path, "error", err.Error())
			continue
		}
		res.Deleted++
	}
	return res, nil
}

// feeStagingRefs builds the keep-set of the fee staging sweep: the
// content-addressed base name (<sha256>_<workbook_name>) derived from
// every fee batch row of ANY status — the fee twin of the workbook_path
// audit record (the staging channel's identity is the pair it was named
// from, not a recorded path).
func (a *App) feeStagingRefs(ctx context.Context) (map[string]struct{}, error) {
	batches, err := a.DB.FeeBatches.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("app: gc: list fee batches: %w", err)
	}
	refs := make(map[string]struct{}, len(batches))
	for _, b := range batches {
		refs[b.SHA256+"_"+b.WorkbookName] = struct{}{}
	}
	return refs, nil
}

// gcSweep is the serve-startup garbage collection — the uploads sweep
// (W5-D) plus the fee analysis sweep (#106-C) — run serially BEFORE the
// listener starts: no upload or fee import of THIS process can be in
// flight while orphans are judged (the RecoverInterrupted precedent). The
// fees CLI imports in a separate process that may overlap a serve start;
// GCAnalysis's mtime milestone (service.go) covers that cross-process
// window. Failures are logged and NEVER fatal: a broken sweep must not
// keep the server down — the operator can retry with `netopslab gc`.
// Split out of cmdServe so the placement and failure doctrine is pinned
// by tests; the MCP process never reaches this (it owns no serve loop).
func (a *App) gcSweep(ctx context.Context) {
	if gc, err := a.GCOrphanUploads(ctx); err != nil {
		a.Log.Error("uploads gc failed (server continues)", "error", err.Error())
	} else if gc.Deleted > 0 {
		a.Log.Info("gc: removed orphan uploads", "deleted", gc.Deleted, "scanned", gc.Scanned)
	}
	if a.Fees == nil {
		return
	}
	if files, staging, err := a.Fees.GCAnalysis(ctx); err != nil {
		a.Log.Error("fees analysis gc failed (server continues)", "error", err.Error())
	} else if files+staging > 0 {
		a.Log.Info("gc: swept fee analysis orphans", "parquetFiles", files, "stagingDirs", staging)
	}
}

// gcResponse is the single JSON object the `netopslab gc` subcommand prints
// (the §10 stdout contract: exactly one JSON object). The analysisFiles /
// analysisStaging fields are the fee analysis tree's sweep counts (#106-C):
// orphan Parquet files without a completed batch row, stale .staging dirs.
type gcResponse struct {
	Scanned         int `json:"scanned"`
	Deleted         int `json:"deleted"`
	Referenced      int `json:"referenced"`
	Errors          int `json:"errors"`
	AnalysisFiles   int `json:"analysisFiles"`
	AnalysisStaging int `json:"analysisStaging"`
}

// cmdGC implements `netopslab gc --db --data`: manual garbage collection —
// the serve-startup sweep, on demand: the uploads/fees staging areas plus
// the fee analysis tree (#106). Success prints the GC counts as one JSON
// object on stdout; the n>0 counts are ALSO slog-logged to stderr (info)
// so serve-style logs carry them.
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

	ctx := context.Background()
	res, err := a.GCOrphanUploads(ctx)
	if err != nil {
		return err
	}
	// The fee analysis sweep rides the same manual trigger (#106-C) —
	// the CLI may run while a fees import is in flight elsewhere, which
	// is exactly what GCAnalysis's mtime milestone guards.
	var analysisFiles, analysisStaging int
	if a.Fees != nil {
		if analysisFiles, analysisStaging, err = a.Fees.GCAnalysis(ctx); err != nil {
			return err
		}
	}
	if res.Deleted > 0 {
		a.Log.Info("gc: removed orphan uploads", "deleted", res.Deleted, "scanned", res.Scanned)
	}
	if n := analysisFiles + analysisStaging; n > 0 {
		a.Log.Info("gc: swept fee analysis orphans", "parquetFiles", analysisFiles, "stagingDirs", analysisStaging)
	}
	return writeJSON(stdout, gcResponse{
		Scanned:         res.Scanned,
		Deleted:         res.Deleted,
		Referenced:      res.Referenced,
		Errors:          res.Errors,
		AnalysisFiles:   analysisFiles,
		AnalysisStaging: analysisStaging,
	})
}
