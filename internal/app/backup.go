package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// backupResponse is the `netopsfees backup` stdout contract: one JSON
// object naming the snapshot's path and size.
type backupResponse struct {
	Out   string `json:"out"`
	Bytes int64  `json:"bytes"`
}

// cmdBackup implements `netopsfees backup --db path --out file.db`: a
// byte-consistent snapshot of the WHOLE database (every table, every
// migration's data) written via the SQLite Online Backup API — never a
// raw file copy of the live database (PROJECT_BRIEF_P2 §1.6; the same
// machinery the lab checkpoints use, exposed as the operator's one-shot
// command). The source keeps serving reads and writes while the backup
// runs (that is the point of the online API); the output file is created
// or TRUNCATED (script-friendly, the export/report precedent).
//
// The parent directory of --out is created first (backup to a path whose
// directory does not exist is the obvious operator intent, not an error).
func cmdBackup(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("backup")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	out := fs.String("out", "", "备份输出文件路径，必填（已存在则覆盖；建议 .db 后缀）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "backup 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *out == "" {
		return &usageError{msg: "缺少 --out 参数（备份输出文件路径）", usage: usage.String()}
	}
	if abs, err := filepath.Abs(*db); err == nil && abs == *db {
		// no-op: keep relative paths as given (the --db convention)
		_ = abs
	}

	a, err := New(Config{DBPath: *db})
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	ctx := context.Background()
	if err := a.DB.Backup(ctx, *out); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	info, err := os.Stat(*out)
	if err != nil {
		return fmt.Errorf("backup: stat %s: %w", *out, err)
	}
	a.Log.Info("backup: database snapshot written", "out", *out, "bytes", info.Size())
	return writeJSON(stdout, backupResponse{Out: *out, Bytes: info.Size()})
}
