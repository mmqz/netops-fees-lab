package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mmqz/netops-lab/internal/changes"
	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/exporter"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/mcpserver"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/sim"
	"github.com/mmqz/netops-lab/internal/testfixtures"
	"github.com/mmqz/netops-lab/internal/topology"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// CLI exit codes (§10): 0 success, 1 failure, 2 usage.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// envDBPath is the environment variable the mcp subcommand honors.
const envDBPath = "NETOPS_DB"

// CLI dispatches the netopslab subcommands. Every successful command writes
// exactly one JSON object to stdout (the report --format html command writes
// an HTML document instead, per §10); failures write one JSON error object
// {"error":{"code","message"}} to stdout; structured logs go to stderr.
// The returned value is the process exit code.
func CLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeCLIError(stdout, "usage_error", "缺少子命令；可用：serve, analyze, import, ingest, synthdata, detect, export, validate, report, query, changes, renames, fees, lab, templates, mcp, gc, testdata")
		return exitUsage
	}

	var err error
	switch args[0] {
	case "serve":
		err = cmdServe(args[1:], stdout, stderr)
	case "analyze":
		err = cmdAnalyze(args[1:], stdout, stderr)
	case "import":
		err = cmdImport(args[1:], stdout, stderr)
	case "ingest":
		err = cmdIngest(args[1:], stdout, stderr)
	case "synthdata":
		err = cmdSynthdata(args[1:], stdout, stderr)
	case "detect":
		err = cmdDetect(args[1:], stdout, stderr)
	case "export":
		err = cmdExport(args[1:], stdout, stderr)
	case "validate":
		err = cmdValidate(args[1:], stdout, stderr)
	case "report":
		err = cmdReport(args[1:], stdout, stderr)
	case "query":
		err = cmdQuery(args[1:], stdout, stderr)
	case "changes":
		err = cmdChanges(args[1:], stdout, stderr)
	case "renames":
		err = cmdRenames(args[1:], stdout, stderr)
	case "fees":
		err = cmdFees(args[1:], stdout, stderr)
	case "lab":
		err = cmdLab(args[1:], stdout, stderr)
	case "templates":
		err = cmdTemplates(args[1:], stdout, stderr)
	case "mcp":
		err = cmdMCP(args[1:], stdout, stderr)
	case "gc":
		err = cmdGC(args[1:], stdout, stderr)
	case "backup":
		err = cmdBackup(args[1:], stdout, stderr)
	case "testdata":
		err = cmdTestdata(args[1:], stdout, stderr)
	default:
		writeCLIError(stdout, "usage_error", fmt.Sprintf("未知子命令 %q；可用：serve, analyze, import, ingest, synthdata, detect, export, validate, report, query, changes, renames, fees, lab, templates, mcp, gc, backup, testdata", args[0]))
		return exitUsage
	}

	if err == nil {
		return exitOK
	}
	var uerr *usageError
	if errors.As(err, &uerr) {
		if uerr.usage != "" {
			fmt.Fprintln(stderr, uerr.usage)
		}
		writeCLIError(stdout, "usage_error", err.Error())
		return exitUsage
	}
	writeCLIError(stdout, cliErrorCode(err), err.Error())
	return exitFailure
}

// usageError marks flag/usage problems (exit code 2).
type usageError struct {
	msg   string
	usage string
}

func (e *usageError) Error() string { return e.msg }

// newFlagSet builds a ContinueOnError flag set whose usage text is captured
// into the returned builder for stderr output on usage errors.
func newFlagSet(name string) (*flag.FlagSet, *strings.Builder) {
	usage := &strings.Builder{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(usage)
	return fs, usage
}

// writeCLIError writes the JSON error object to stdout.
func writeCLIError(w io.Writer, code, message string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// cliErrorCode maps service errors to the CLI error codes (same taxonomy as
// the HTTP API).
func cliErrorCode(err error) string {
	var fieldErr *mapping.FieldError
	if errors.As(err, &fieldErr) {
		return "validation"
	}
	// The W13-D ref-target guard of the attribute-change apply: the CLI
	// mirrors the HTTP 409 ref_target_missing code (the taxonomy parity the
	// docstring promises).
	var refMissing *changes.RefTargetMissingError
	if errors.As(err, &refMissing) {
		return "ref_target_missing"
	}
	// The W24 unattended-import refusal (§12-B7): the code the task contract
	// pins — the error envelope names the unmatched sheets and the batch
	// stays awaiting_mapping (HTTP answers 202 + analysis there; the CLI has
	// no response slot for that, so the envelope IS the warning).
	var noTemplate *importer.NoMatchingTemplateError
	if errors.As(err, &noTemplate) {
		return "no_matching_template"
	}
	switch {
	case errors.Is(err, workbook.ErrNotXLSX):
		return "not_xlsx"
	case errors.Is(err, workbook.ErrEmptyWorkbook):
		return "empty_workbook"
	case errors.Is(err, workbook.ErrWorkbookTooLarge):
		return "workbook_too_large"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrInvalidOp):
		return "invalid_request"
	case errors.Is(err, domain.ErrOpFailed):
		return "op_failed"
	case errors.Is(err, domain.ErrNotLatest):
		return "not_latest"
	case errors.Is(err, domain.ErrNoSourceResources):
		return "no_source_resources"
	}
	return "internal"
}

// writeJSON writes exactly one indented JSON object to stdout.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// openApp wires the application for a CLI command (stderr logging, no
// upload directory).
func openApp(dbPath string) (*App, error) {
	a, err := New(Config{DBPath: dbPath})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// openAppWithWebhook opens the app with the detection-run notification
// channel configured (v0.10-R8): an empty URL leaves the channel off
// (notify.New returns the nil no-op).
func openAppWithWebhook(dbPath, webhookURL string) (*App, error) {
	a, err := New(Config{DBPath: dbPath, WebhookURL: webhookURL})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// cmdServe implements `netopslab serve --addr --db --data-dir
// [--webhook-url U] [--detect-interval D] [--auth-token T]`.
func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("serve")
	addr := fs.String("addr", ":8080", "HTTP 监听地址")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	dataDir := fs.String("data-dir", "./data", "上传文件存储目录")
	webhookURL := fs.String("webhook-url", "", "检测运行通知目标 URL（POST JSON 摘要；空 = 不通知，投递失败仅记日志不影响检测）")
	detectInterval := fs.String("detect-interval", "", "定时巡检间隔（如 15m / 1h；每个间隔执行一次全库检测并落库回执、触发 webhook；空 = 仅显式触发，v0.10 行为；最小 1s）")
	authToken := fs.String("auth-token", "", "写操作访问令牌（非空则所有 POST/PATCH/DELETE /api 请求需携带 Authorization: Bearer <token>；只读请求与健康检查不受影响；空 = 不启用）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "serve 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	cfg := Config{Addr: *addr, DBPath: *db, DataDir: *dataDir, WebhookURL: *webhookURL, AuthToken: *authToken}
	if *detectInterval != "" {
		d, err := time.ParseDuration(*detectInterval)
		if err != nil {
			return &usageError{msg: fmt.Sprintf("--detect-interval %q 不是 Go 时长（例 15m / 1h30m / 45s）：%v", *detectInterval, err), usage: usage.String()}
		}
		if d < time.Second {
			return &usageError{msg: fmt.Sprintf("--detect-interval %s 低于最小 1s（更短间隔会连环触发全库扫描）", d), usage: usage.String()}
		}
		cfg.DetectInterval = d
	}
	a, err := New(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	// Crash-window self-heal (R1-D6): serve startup has no in-flight import
	// of THIS process, so any batch still in a transient state (analyzing /
	// importing) is debris of a crashed previous run — reset it to
	// failed(interrupted). An import of another process that completes later
	// overwrites the heal with its own terminal status (execute.go writes
	// the terminal state inside its transaction).
	if n, err := a.Importer.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted imports: %w", err)
	} else if n > 0 {
		a.Log.Info("recovered interrupted import batches", "count", n)
	}

	// Uploads garbage collection (W5-D, v0.4.0), same placement rationale
	// as RecoverInterrupted above: run serially BEFORE the listener starts,
	// so no upload of this (or any other) process can be in flight while we
	// judge orphans — an upload being written right now is not yet
	// referenced by any batch and would be misjudged. The MCP process never
	// reaches this call site (it owns no serve loop).
	if gc, err := a.GCOrphanUploads(ctx); err != nil {
		// Not fatal: a broken sweep must not keep the server down; the
		// operator can retry with `netopslab gc`.
		a.Log.Error("uploads gc failed (server continues)", "error", err.Error())
	} else if gc.Deleted > 0 {
		a.Log.Info("gc: removed orphan uploads", "deleted", gc.Deleted, "scanned", gc.Scanned)
	}
	return a.Run(ctx)
}

// cmdAnalyze implements `netopslab analyze --file`: OFFLINE workbook
// analysis (PROJECT_BRIEF §10: "parse+suggest only, no DB write") — the
// printed Analysis JSON has the same shape as the POST /api/imports response
// except that no batch is created, so batchId stays empty. The confirmed
// import itself (analyze + execute + auto-validation) is `netopslab import`.
func cmdAnalyze(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("analyze")
	file := fs.String("file", "", "工作簿路径（.xlsx）或 PTP 配置脚本（.txt/.cfg），必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" {
		return &usageError{msg: "缺少 --file 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "analyze 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	res, err := importer.AnalyzeOffline(*file, filepath.Base(*file))
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}

// importResponse mirrors the POST /api/imports/{id}/mapping response shape
// (§9) — the CLI import command prints the identical object.
type importResponse struct {
	BatchID string `json:"batchId"`
	Status  string `json:"status"`
	Summary struct {
		RowsTotal    int `json:"rowsTotal"`
		RowsImported int `json:"rowsImported"`
		RowsSkipped  int `json:"rowsSkipped"`
		RowsFailed   int `json:"rowsFailed"`
	} `json:"summary"`
	Issues map[string]int `json:"issues"`
}

// cmdImport implements `netopslab import --file --map --db` and the W24
// unattended variant `netopslab import --file --db --auto`: analyze +
// execute the confirmed mapping + automatic set-level validation; prints
// the import summary with issue counts.
//
// --auto replaces --map (the two are mutually exclusive): the confirmed
// mapping comes from the stored mapping TEMPLATES whose header
// fingerprints match the workbook — zero human interaction. When every
// import sheet matches, the import executes immediately and the summary is
// printed (exit 0). ANY unmatched import sheet refuses honestly: the error
// envelope carries code no_matching_template and names the sheets; exit 1;
// the batch stays awaiting_mapping for the ordinary --map confirmation
// flow (表头变更感知 — a changed workbook is re-confirmation, not an
// error).
func cmdImport(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("import")
	file := fs.String("file", "", "工作簿路径（.xlsx）或 PTP 配置脚本（.txt/.cfg），必填")
	mapFile := fs.String("map", "", "映射 JSON 文件路径（§8 结构），与 --auto 互斥")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	auto := fs.Bool("auto", false, "无人值守导入：以表头指纹命中的映射模板执行（全部命中才执行；未命中→exit 1 no_matching_template）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" || (*mapFile == "" && !*auto) {
		return &usageError{msg: "缺少 --file 或 --map 参数（--auto 模式无需 --map）", usage: usage.String()}
	}
	if *auto && *mapFile != "" {
		return &usageError{msg: "--auto 与 --map 互斥：无人值守导入使用已存的映射模板", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "import 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	var m mapping.Mapping
	if !*auto {
		raw, err := os.ReadFile(*mapFile)
		if err != nil {
			return fmt.Errorf("读取映射文件 %s: %w", *mapFile, err)
		}
		m, err = mapping.Parse(raw)
		if err != nil {
			return fmt.Errorf("映射文件 %s 不是合法 JSON: %w", *mapFile, err)
		}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	// Crash-window self-heal (R1-D6) before starting OUR import: this
	// process now owns the import pipeline, so transient-state debris of
	// earlier crashed runs is reset to failed(interrupted) first. (The
	// offline `analyze` command has no database and cannot heal — it never
	// creates a batch either.)
	if n, err := a.Importer.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted imports: %w", err)
	} else if n > 0 {
		a.Log.Info("recovered interrupted import batches", "count", n)
	}

	var sum importer.Summary
	if *auto {
		// W24 unattended path: analyze + fingerprint match + (on full match)
		// execute with the stored template mappings. A refusal returns the
		// typed *importer.NoMatchingTemplateError — the CLI error envelope
		// code no_matching_template and exit 1; the batch stays
		// awaiting_mapping for the ordinary --map flow.
		res, err := a.Importer.ImportAuto(ctx, *file, filepath.Base(*file))
		if err != nil {
			return err
		}
		sum = res.Summary
	} else {
		res, err := a.Importer.Analyze(ctx, *file)
		if err != nil {
			return err
		}
		sum, err = a.Importer.Execute(ctx, res.BatchID, m)
		if err != nil {
			return err
		}
	}
	vr, err := a.Validation.Run(ctx)
	if err != nil {
		return err
	}

	issues := map[string]int{
		domain.SeverityError.String():   0,
		domain.SeverityWarning.String(): 0,
		domain.SeverityInfo.String():    0,
	}
	for sev, n := range sum.IssuesBySeverity {
		issues[sev.String()] += n
	}
	for rule, n := range vr.InsertedByRule {
		if sev, known := domain.SeverityForCode(rule); known {
			issues[sev.String()] += n
		}
	}

	out := importResponse{BatchID: sum.BatchID, Status: sum.Status.String(), Issues: issues}
	out.Summary.RowsTotal = sum.RowsTotal
	out.Summary.RowsImported = sum.RowsImported
	out.Summary.RowsSkipped = sum.RowsSkipped
	out.Summary.RowsFailed = sum.RowsFailed
	return writeJSON(stdout, out)
}

// cmdTemplates implements the W24 mapping template subcommands
// (§12-B7 方案 A, the CLI twin of the HTTP template endpoints):
//
//	netopslab templates --db d save --batch <id> [--name n]
//	netopslab templates --db d list [--page P] [--pageSize S]
//	netopslab templates --db d delete --id <tpl_id>
//
// save copies the CONFIRMED mapping of a COMPLETED batch into per-sheet
// templates (fingerprints recomputed from the batch's recorded workbook) —
// the one-click capture; re-confirmation of the same sheet shape upserts
// (idempotent). list prints the paged, newest-first template inventory with
// each stored single-sheet mapping. delete removes one template (the
// unattended channel's opt-out; the template never touches the batch data
// and a batch rollback never deletes the template).
func cmdTemplates(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "缺少 templates 子命令；可用：save, list, delete"}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "save":
		return cmdTemplatesSave(rest, stdout, stderr)
	case "list":
		return cmdTemplatesList(rest, stdout, stderr)
	case "delete":
		return cmdTemplatesDelete(rest, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("templates 子命令必须是 save、list 或 delete，收到 %q", sub)}
	}
}

// templatesItem mirrors one template of the CLI list/save responses (the
// HTTP templateItem field names).
type templatesItem struct {
	TemplateID        string         `json:"templateId"`
	SourceName        string         `json:"sourceName"`
	HeaderFingerprint string         `json:"headerFingerprint"`
	ResourceType      string         `json:"resourceType"`
	Mapping           map[string]any `json:"mapping"`
	SheetName         string         `json:"sheetName"`
	ConfirmedAt       string         `json:"confirmedAt"`
	ConfirmedBatch    string         `json:"confirmedBatch"`
}

// cmdTemplatesSave implements `templates save --batch <id> [--name]`:
// 201-equivalent (CLI: one JSON object) with the captured templates; 404
// unknown batch and 409 not-completed batch map to the error envelope via
// cliErrorCode.
func cmdTemplatesSave(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("templates save")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	batch := fs.String("batch", "", "已完成批次的 id（其确认映射将被保存为模板），必填")
	name := fs.String("name", "", "模板人类可读名称（缺省为该批次的工作簿名）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *batch == "" {
		return &usageError{msg: "缺少 --batch 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "templates save 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	captured, err := a.Importer.CaptureTemplate(context.Background(), *batch, *name)
	if err != nil {
		return err
	}
	items := make([]templatesItem, 0, len(captured))
	for _, t := range captured {
		items = append(items, templatesItemOf(t))
	}
	return writeJSON(stdout, struct {
		Items []templatesItem `json:"items"`
		Total int             `json:"total"`
	}{Items: items, Total: len(items)})
}

// cmdTemplatesList implements `templates list [--page] [--pageSize]`: the
// paged inventory, newest-confirmed first.
func cmdTemplatesList(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("templates list")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	page := fs.Int("page", 1, "页码（1 起）")
	pageSize := fs.Int("pageSize", 20, "每页条数（1-100，超出部分钳制为 100）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "templates list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *page < 1 {
		return &usageError{msg: "page 必须为正整数（1 起）", usage: usage.String()}
	}
	if *pageSize < 1 {
		return &usageError{msg: "pageSize 必须为正整数（1-100）", usage: usage.String()}
	}
	if *pageSize > 100 {
		*pageSize = 100
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	templates, total, err := a.DB.Templates.ListTemplates(context.Background(), *pageSize, (*page-1)**pageSize)
	if err != nil {
		return err
	}
	items := make([]templatesItem, 0, len(templates))
	for _, t := range templates {
		items = append(items, templatesItemOf(t))
	}
	return writeJSON(stdout, struct {
		Items    []templatesItem `json:"items"`
		Total    int             `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"pageSize"`
	}{Items: items, Total: total, Page: *page, PageSize: *pageSize})
}

// cmdTemplatesDelete implements `templates delete --id <tpl_id>`: one JSON
// object {deleted, id}; unknown id → the error envelope (not_found), exit 1.
func cmdTemplatesDelete(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("templates delete")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	id := fs.String("id", "", "模板 id（templateId），必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "templates delete 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	if err := a.DB.Templates.DeleteTemplate(context.Background(), *id); err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}{Deleted: true, ID: *id})
}

// templatesItemOf projects a domain template into the CLI response item
// (the stored mapping JSON decoded into a generic object so the output is
// one self-describing JSON document; an invalid stored document falls back
// to a JSON string echo — honest and visible).
func templatesItemOf(t domain.MappingTemplate) templatesItem {
	var mappingDoc map[string]any
	if err := json.Unmarshal([]byte(t.MappingJSON), &mappingDoc); err != nil {
		mappingDoc = map[string]any{"raw": t.MappingJSON}
	}
	return templatesItem{
		TemplateID:        t.TemplateID,
		SourceName:        t.SourceName,
		HeaderFingerprint: t.HeaderFingerprint,
		ResourceType:      string(t.ResourceType),
		Mapping:           mappingDoc,
		SheetName:         t.SheetName,
		ConfirmedAt:       t.ConfirmedAt.UTC().Format(time.RFC3339),
		ConfirmedBatch:    t.ConfirmedBatch,
	}
}

// exportResponse is the stdout contract of `netopslab export`: the written
// file path, the per-type row counts (only types with at least one
// resource) and the total row count.
type exportResponse struct {
	File      string         `json:"file"`
	Types     map[string]int `json:"types"`
	TotalRows int            `json:"totalRows"`
}

// cmdIngest implements `netopslab ingest --file x.xlsx [--map m.json]
// [--db d] [--suggest]` — the timeseries metric channel (W20, migration
// v14): with --suggest it prints the offline analysis (suggested mapping
// JSON + column stats + warnings) and exits WITHOUT touching the database;
// with --map it runs the one-shot ingest (analyze + execute in one batch
// transaction) and prints the metric batch result envelope.
func cmdIngest(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("ingest")
	file := fs.String("file", "", "性能工作簿路径（.xlsx），必填")
	mapFile := fs.String("map", "", "指标映射 JSON 文件路径（{tsField,resourceType,resolveBy,metrics}），与 --suggest 二选一")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	suggest := fs.Bool("suggest", false, "只解析并输出建议映射与列统计（不写数据库）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" {
		return &usageError{msg: "缺少 --file 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "ingest 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	if *suggest {
		// Offline: no database, no batch — parse + suggest only.
		a, err := metrics.AnalyzeOffline(*file, filepath.Base(*file))
		if err != nil {
			return err
		}
		return writeJSON(stdout, a)
	}
	if *mapFile == "" {
		return &usageError{msg: "缺少 --map 或 --suggest 参数（二者必选其一）", usage: usage.String()}
	}
	raw, err := os.ReadFile(*mapFile)
	if err != nil {
		return fmt.Errorf("读取映射文件 %s: %w", *mapFile, err)
	}
	m, err := metrics.ParseMapping(raw)
	if err != nil {
		return fmt.Errorf("映射文件 %s 不是合法 JSON: %w", *mapFile, err)
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	sum, err := a.Metrics.Ingest(ctx, *file, m, filepath.Base(*file))
	if err != nil {
		return err
	}
	return writeJSON(stdout, sum)
}

// cmdSynthdata implements `netopslab synthdata --out DIR --devices N
// --ports P --days D --scenario normal|degrade|outage --seed S
// [--metrics N]`: the deterministic synthetic dataset generator (W20).
// Defaults are demo scale (20 devices × 2 ports, 30 days, normal, seed 42,
// all 14 metric columns); the same seed yields byte-identical files.
// The output directory is whatever the caller passes (--out) — the demo
// placeholder later points at testdata/placeholder/ (§12-A5).
func cmdSynthdata(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("synthdata")
	out := fs.String("out", "", "输出目录（不存在则创建），必填")
	devices := fs.Int("devices", 20, "设备数量")
	ports := fs.Int("ports", 2, "每设备端口数（压测用 20）")
	days := fs.Int("days", 30, "性能数据天数（每天一个 perf-day-NN.xlsx）")
	scenario := fs.String("scenario", "normal", "故障场景：normal | degrade | outage")
	seed := fs.Int64("seed", 42, "确定性种子（同种子同输出）")
	metricCols := fs.Int("metrics", 0, "指标列数（注册表顺序取前 N，0=全部 14 列）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *out == "" {
		return &usageError{msg: "缺少 --out 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "synthdata 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if !metrics.ValidScenario(*scenario) {
		return &usageError{msg: fmt.Sprintf("未知 scenario %q（normal|degrade|outage）", *scenario), usage: usage.String()}
	}

	sum, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out:      *out,
		Devices:  *devices,
		Ports:    *ports,
		Days:     *days,
		Scenario: *scenario,
		Seed:     *seed,
		Metrics:  *metricCols,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, sum)
}

// cmdDetect implements `netopslab detect --db <path> [--from F] [--to T]`
// (one engine sweep — the CLI twin of POST /api/detection/run; the SAME
// composition-root engine instance, no bypass) and `netopslab detect
// --rules --db <path>` (the rules listing). NO unattended scheduling in
// this round: cron is the operator's job — the run is explicit by design
// (determinism doctrine: the engine runs when the operator or the daily
// inspection calls it).
func cmdDetect(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("detect")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	from := fs.String("from", "", "评估窗口下界（RFC3339，可选）")
	to := fs.String("to", "", "评估窗口上界（RFC3339，可选）")
	rules := fs.Bool("rules", false, "只列出检测规则（不执行检测）")
	webhookURL := fs.String("webhook-url", "", "检测运行通知目标 URL（POST JSON 摘要；空 = 不通知，投递失败仅记日志不影响检测）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "detect 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	for name, raw := range map[string]string{"from": *from, "to": *to} {
		if raw == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			return &usageError{msg: fmt.Sprintf("--%s %q 不是 RFC3339 时间戳", name, raw), usage: usage.String()}
		}
	}

	a, err := openAppWithWebhook(*db, *webhookURL)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	if *rules {
		if err := a.DB.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
			return err
		}
		list, err := a.DB.DetectionRules.ListRules(ctx, false)
		if err != nil {
			return err
		}
		items := make([]map[string]any, 0, len(list))
		for _, r := range list {
			items = append(items, map[string]any{
				"ruleId":     r.RuleID,
				"metricKey":  r.MetricKey,
				"comparator": r.Comparator.String(),
				"threshold":  r.Threshold,
				"window":     r.Window,
				"severity":   r.Severity.String(),
				"enabled":    r.Enabled,
				"label":      r.Label,
				"createdAt":  r.CreatedAt,
				"updatedAt":  r.UpdatedAt,
			})
		}
		return writeJSON(stdout, struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}{Items: items, Total: len(items)})
	}

	res, rec, err := detect.RunAndRecord(ctx, a.DB, a.Detect, detect.Options{From: *from, To: *to}, domain.RunSourceCLI, time.Now)
	if err != nil {
		return err
	}
	// Notification side path (v0.10-R8): deliver synchronously so the
	// delivery is attempted BEFORE the process exits (the CLI is the
	// daily-inspection twin of the HTTP trigger). Best-effort: the
	// notifier logs the failure itself; the run result is unaffected —
	// the single-JSON-object stdout contract stays.
	_ = a.Webhook.NotifyDetectionRun(ctx, res, rec.RunID)
	// The receipt block (runId/source/startedAt/…) rides along in the
	// same single JSON object — the HTTP envelope's CLI twin, so a
	// scripted `netopslab detect` and a POST /api/detection/run leave
	// identically-shaped evidence.
	return writeJSON(stdout, cliRunEnvelope{
		RunResult:  res,
		RunID:      rec.RunID,
		Source:     rec.Source.String(),
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
		DurationMS: rec.DurationMS,
		From:       rec.From,
		To:         rec.To,
	})
}

// cliRunEnvelope mirrors the HTTP POST /api/detection/run response shape
// (api.detectionRunEnvelope): the full RunResult plus the persisted
// receipt block. One JSON object on stdout, the CLI contract.
type cliRunEnvelope struct {
	detect.RunResult
	RunID      string `json:"runId"`
	Source     string `json:"source"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMS int64  `json:"durationMs"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// cmdExport implements `netopslab export --db <path> --out <file.xlsx>
// [--types a,b]`: project the CURRENT resource inventory into an .xlsx
// workbook that is isomorphic to an import workbook (W12-D — one sheet per
// resource type in mapping.Specs order, headers = field Labels, rows =
// attribute snapshot sorted by natural key; see internal/exporter for the
// round-trip semantics and the determinism guarantees). The output file is
// created or TRUNCATED — an existing file is overwritten (script-friendly,
// same semantics as `report --out`). An empty database (or a types filter
// matching no resources) exports successfully with zero rows: the workbook
// then carries only the neutral placeholder sheet. A bad --types value is
// a usage error (exit 2) naming the offending type.
func cmdExport(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("export")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	out := fs.String("out", "", "输出 xlsx 文件路径，必填（已存在则覆盖）")
	types := fs.String("types", "", "逗号分隔的资源类型列表（缺省导出全部类型：device, ip_pool, circuit, port, base_station, site, machine_room, ptp_port）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *out == "" {
		return &usageError{msg: "缺少 --out 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "export 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	filter, err := exporter.ParseTypes(*types)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	resources, err := a.Query.AllByTypes(ctx, filter)
	if err != nil {
		return err
	}
	summary, err := exporter.WriteFile(*out, resources, filter)
	if err != nil {
		return err
	}
	return writeJSON(stdout, exportResponse{File: *out, Types: summary.Types, TotalRows: summary.TotalRows})
}

// validateResponse mirrors POST /api/validate.
type validateResponse struct {
	Issues map[string]int `json:"issues"`
	Score  int            `json:"score"`
}

// cmdValidate implements `netopslab validate --db [--batch]`: recompute the
// set-level rules, print issue counts and the quality score
// (100 − 5·error − 1·warning, floor 0). --batch scopes the counts to the
// batch's row-phase issues plus all set-level issues (report batch-scope
// semantics).
func cmdValidate(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("validate")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	batch := fs.String("batch", "", "限定批次的 row-phase 问题计数（可选）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "validate 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	if _, err := a.Validation.Run(ctx); err != nil {
		return err
	}

	issues := map[string]int{
		domain.SeverityError.String():   0,
		domain.SeverityWarning.String(): 0,
		domain.SeverityInfo.String():    0,
	}
	if *batch == "" {
		counts, err := a.DB.Issues.CountBySeverity(ctx, nil)
		if err != nil {
			return err
		}
		for sev, n := range counts {
			issues[sev.String()] += n
		}
	} else {
		if _, err := a.DB.Batches.Get(ctx, *batch); err != nil {
			return err
		}
		rowPhase, err := a.DB.Issues.CountBySeverity(ctx, batch)
		if err != nil {
			return err
		}
		all, _, err := a.DB.Issues.List(ctx, repo.IssueFilter{})
		if err != nil {
			return err
		}
		for sev, n := range rowPhase {
			issues[sev.String()] += n
		}
		for _, iss := range all {
			if iss.BatchID == nil {
				issues[iss.Severity.String()]++
			}
		}
	}

	score := 100 - 5*issues[domain.SeverityError.String()] - issues[domain.SeverityWarning.String()]
	if score < 0 {
		score = 0
	}
	return writeJSON(stdout, validateResponse{Issues: issues, Score: score})
}

// cmdReport implements `netopslab report --db [--batch] --format html|json
// [--out]` (default stdout; --batch empty means the global scope).
//
// §10 contract (issue #6): EVERY command prints exactly one JSON object
// on stdout. With --out the artifact goes to the file and stdout carries a
// compact reportResponse summary (the export command's pattern); without
// --out the artifact itself goes to stdout (json = the report object, html
// = the standalone document) as before.
func cmdReport(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("report")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	batch := fs.String("batch", "", "限定批次（可选，缺省为全局）")
	format := fs.String("format", "json", "输出格式：html 或 json")
	out := fs.String("out", "", "输出文件路径（缺省写入 stdout）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *format != "html" && *format != "json" {
		return &usageError{msg: fmt.Sprintf("--format 必须是 html 或 json，收到 %q", *format), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "report 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	var dest io.Writer = stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fmt.Errorf("创建输出文件 %s: %w", *out, err)
		}
		defer func() { _ = f.Close() }()
		dest = f
	}

	if *format == "html" {
		html, err := a.Report.RenderHTML(ctx, *batch)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(dest, html); err != nil {
			return err
		}
	} else {
		if err := a.Report.WriteJSON(ctx, *batch, dest); err != nil {
			return err
		}
	}
	// --out: stdout answers the one-JSON-object summary (issue #6: the
	// command used to exit 0 with silent stdout, indistinguishable from
	// a failure in scripts).
	if *out != "" {
		r, err := a.Report.JSON(ctx, *batch)
		if err != nil {
			return err
		}
		return writeJSON(stdout, reportResponse{
			File:             *out,
			Format:           *format,
			Scope:            r.Scope,
			TotalResources:   r.Summary.TotalResources,
			QualityScore:     r.Summary.QualityScore,
			IssuesBySeverity: r.Summary.IssuesBySeverity,
		})
	}
	return nil
}

// reportResponse is the stdout summary of `report --out` (issue #6): enough
// to judge the outcome without opening the file.
type reportResponse struct {
	File             string         `json:"file"`
	Format           string         `json:"format"`
	Scope            string         `json:"scope"`
	TotalResources   int            `json:"totalResources"`
	QualityScore     int            `json:"qualityScore"`
	IssuesBySeverity map[string]int `json:"issuesBySeverity"`
}

// cmdQuery implements `netopslab query --db <path> resources|stats|relations|governance …`.
// The sub-subcommand may appear before or after the query-level flags; only
// --db is accepted at the query level.
func cmdQuery(args []string, stdout, stderr io.Writer) error {
	dbPath, sub, subArgs, err := splitSubArgs("query", "resources / stats / relations / governance / traverse / impact / context / nearest", args)
	if err != nil {
		return err
	}
	a, err := openApp(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	switch sub {
	case "resources":
		return cmdQueryResources(ctx, a, subArgs, stdout, stderr)
	case "stats":
		return cmdQueryStats(ctx, a, subArgs, stdout, stderr)
	case "relations":
		return cmdQueryRelations(ctx, a, subArgs, stdout, stderr)
	case "governance":
		return cmdQueryGovernance(ctx, a, subArgs, stdout, stderr)
	// Issue #41 (audit §2.5): the v0.7.0 graph queries were HTTP-only,
	// which left scripted/offline workflows (daily inspection without a
	// serve) without traverse/impact/context. Same service layer as the
	// HTTP endpoints (the composition root's ONE store binding and ONE
	// context assembly), same stdout-exactly-one-JSON contract (§10).
	case "traverse":
		return cmdQueryTraverse(ctx, a, subArgs, stdout, stderr)
	case "impact":
		return cmdQueryImpact(ctx, a, subArgs, stdout, stderr)
	case "context":
		return cmdQueryContext(ctx, a, subArgs, stdout, stderr)
	case "nearest":
		return cmdQueryNearest(ctx, a, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("query 子命令必须是 resources、stats、relations、governance、traverse、impact、context 或 nearest，收到 %q", sub)}
	}
}

// splitSubArgs separates the leading command-level flags (--db[=path])
// from the sub-subcommand and its own flags. cmdName names the parent
// command for the error messages; subUsage lists the accepted
// sub-subcommands (e.g. "resources / stats / relations").
func splitSubArgs(cmdName, subUsage string, args []string) (dbPath, sub string, subArgs []string, err error) {
	dbPath = "./data/netops.db"
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--db" || arg == "-db":
			if i+1 >= len(args) {
				return "", "", nil, &usageError{msg: "--db 需要一个路径参数"}
			}
			dbPath = args[i+1]
			i += 2
		case strings.HasPrefix(arg, "--db="):
			dbPath = strings.TrimPrefix(arg, "--db=")
			i++
		case strings.HasPrefix(arg, "-"):
			return "", "", nil, &usageError{msg: fmt.Sprintf("%s 不支持参数 %q（仅 --db）", cmdName, arg)}
		default:
			return dbPath, arg, args[i+1:], nil
		}
	}
	return "", "", nil, &usageError{msg: fmt.Sprintf("缺少 %s 子命令（%s）", cmdName, subUsage)}
}

// cmdChanges implements `netopslab changes --db <path> list|apply|dismiss …`
// — the CLI side of the attribute change confirmation flow (W4-D; the
// query subcommand pattern: leading --db, then the sub-subcommand). All
// outputs are the single-JSON-object stdout contract: list → {items,total};
// apply/dismiss → the change object after the transition.
func cmdChanges(args []string, stdout, stderr io.Writer) error {
	dbPath, sub, subArgs, err := splitSubArgs("changes", "list / apply / dismiss", args)
	if err != nil {
		return err
	}
	a, err := openApp(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	switch sub {
	case "list":
		return cmdChangesList(ctx, a, subArgs, stdout, stderr)
	case "apply":
		return cmdChangesApply(ctx, a, subArgs, stdout, stderr)
	case "dismiss":
		return cmdChangesDismiss(ctx, a, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("changes 子命令必须是 list、apply 或 dismiss，收到 %q", sub)}
	}
}

// cmdChangesList implements `changes list [--status] [--resource-id]
// [--batch-id] [--limit] [--offset]`.
func cmdChangesList(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("changes list")
	status := fs.String("status", "", "状态过滤：pending / applied / dismissed / all（缺省 all）")
	resourceID := fs.String("resource-id", "", "按资源 id 过滤")
	batchID := fs.String("batch-id", "", "按导入批次 id 过滤")
	limit := fs.Int("limit", changes.DefaultPageSize, "每页条数（最大 100）")
	offset := fs.Int("offset", 0, "偏移量（非负）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "changes list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	filter := changes.Filter{
		ResourceID: *resourceID,
		BatchID:    *batchID,
		Limit:      *limit,
		Offset:     *offset,
	}
	if *status != "" && *status != "all" {
		st, err := domain.ParseChangeStatus(*status)
		if err != nil {
			return &usageError{msg: fmt.Sprintf("--status 必须是 pending/applied/dismissed/all，收到 %q", *status), usage: usage.String()}
		}
		filter.Status = st
	}
	result, err := a.Changes.List(ctx, filter)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdChangesApply implements `changes apply --id <changeId>`.
func cmdChangesApply(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("changes apply")
	id := fs.String("id", "", "待确认变更 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "changes apply 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	item, err := a.Changes.Apply(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, item)
}

// cmdChangesDismiss implements `changes dismiss --id <changeId>`.
func cmdChangesDismiss(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("changes dismiss")
	id := fs.String("id", "", "待确认变更 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "changes dismiss 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	item, err := a.Changes.Dismiss(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, item)
}

// cmdQueryResources implements `query resources --q --type --status --page
// --pageSize` (the database path is the query-level --db flag).
func cmdQueryResources(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query resources")
	q := fs.String("q", "", "搜索关键词（natural_key/name/attributes 模糊匹配）")
	typ := fs.String("type", "", "资源类型：device / ip_pool / circuit / port / base_station / site / machine_room / ptp_port")
	status := fs.String("status", "", "状态过滤")
	page := fs.Int("page", 1, "页码（1 起）")
	pageSize := fs.Int("pageSize", query.DefaultPageSize, "每页条数（最大 100）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query resources 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	filter := query.SearchFilter{Q: *q, Status: *status, Page: *page, PageSize: *pageSize}
	if *typ != "" {
		t, err := domain.ParseResourceType(*typ)
		if err != nil {
			// W21 (v0.7.0): the closed set extends with the registry (33
			// types) — derived, never hand-copied (issue #26 discipline).
			types := make([]string, 0, len(domain.AllResourceTypes()))
			for _, rt := range domain.AllResourceTypes() {
				types = append(types, rt.String())
			}
			return &usageError{msg: fmt.Sprintf("--type 必须是注册资源类型之一（%s），收到 %q", strings.Join(types, "/"), *typ), usage: usage.String()}
		}
		filter.Type = t
	}
	result, err := a.Query.SearchResources(ctx, filter)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdQueryStats implements `query stats`.
func cmdQueryStats(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query stats")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query stats 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	stats, err := a.Query.Stats(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, stats)
}

// cmdQueryRelations implements `query relations --id --depth`.
func cmdQueryRelations(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query relations")
	id := fs.String("id", "", "资源 id，必填")
	depth := fs.Int("depth", 1, "跳数：1 或 2")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if *depth != 1 && *depth != 2 {
		return &usageError{msg: fmt.Sprintf("--depth 必须是 1 或 2，收到 %s", strconv.Itoa(*depth)), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query relations 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	result, err := a.Query.Relations(ctx, *id, *depth)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdQueryGovernance implements `query governance` — the governance
// overview aggregate (the CLI twin of GET /api/governance/summary): the
// pending confirmation queues, the decision audit counters, the issue
// distribution and the inventory shapes as ONE JSON object on stdout. It
// takes no flags of its own (the database path is the query-level --db).
func cmdQueryGovernance(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query governance")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query governance 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	summary, err := a.Governance.Summary(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, summary)
}

// cmdQueryTraverse implements `query traverse --id [--direction
// down|up|both] [--layers 0,1,2] [--relations a,b] [--max-depth 1..8]
// [--max-nodes 1..500] [--path-only]` — the CLI twin of
// GET /api/resources/{id}/traverse (issue #41). Flags mirror the HTTP query
// parameters one-for-one (kebab-case per CLI convention); defaults and
// validation bounds are the same constants the HTTP layer applies, so the
// two channels answer the same documents from the same engine call.
func cmdQueryTraverse(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query traverse")
	id := fs.String("id", "", "根资源 id，必填")
	direction := fs.String("direction", string(topology.DirectionDown),
		"遍历方向：down（默认，追根因）/ up / both")
	layers := fs.String("layers", "", "层号过滤，逗号分隔（0..6，缺省全部）")
	relations := fs.String("relations", "", "关系类型过滤，逗号分隔（合法值见 query stats 或 /api/schema，缺省全部）")
	maxDepth := fs.Int("max-depth", topology.DefaultTraverseMaxDepth, "最大深度（1..8）")
	maxNodes := fs.Int("max-nodes", topology.DefaultTraverseMaxNodes, "节点预算（1..500）")
	pathOnly := fs.Bool("path-only", false, "仅返回根到最深节点的一条最短路径")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query traverse 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	params := topology.TraverseParams{MaxDepth: *maxDepth, MaxNodes: *maxNodes, PathOnly: *pathOnly}
	switch *direction {
	case topology.DirectionDown, topology.DirectionUp, topology.DirectionBoth:
		params.Direction = *direction
	default:
		return &usageError{msg: fmt.Sprintf("--direction 必须是 down/up/both，收到 %q", *direction), usage: usage.String()}
	}
	if *layers != "" {
		params.Layers = map[int]bool{}
		for _, part := range strings.Split(*layers, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			v, err := strconv.Atoi(part)
			if err != nil || v < 0 || v > 6 {
				return &usageError{msg: fmt.Sprintf("--layers 含非法层号 %q（层号必须是 0..6）", part), usage: usage.String()}
			}
			params.Layers[v] = true
		}
	}
	if *relations != "" {
		params.Relations = map[domain.RelationType]bool{}
		for _, part := range strings.Split(*relations, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			rt := domain.RelationType(part)
			if !rt.IsValid() {
				return &usageError{msg: fmt.Sprintf("--relations 含未登记的关系类型 %q（合法值见 query stats 或 /api/schema）", part), usage: usage.String()}
			}
			params.Relations[rt] = true
		}
	}
	if *maxDepth < 1 || *maxDepth > topology.MaxTraverseMaxDepth {
		return &usageError{msg: fmt.Sprintf("--max-depth 必须是 1..%d 的整数", topology.MaxTraverseMaxDepth), usage: usage.String()}
	}
	if *maxNodes < 1 || *maxNodes > topology.MaxTraverseMaxNodes {
		return &usageError{msg: fmt.Sprintf("--max-nodes 必须是 1..%d 的整数", topology.MaxTraverseMaxNodes), usage: usage.String()}
	}

	result, err := topology.Traverse(ctx, a.Services.GraphStore(), *id, params)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdQueryImpact implements `query impact --id [--direction up|down]
// [--max-depth 1..8]` — the CLI twin of GET /api/resources/{id}/impact
// (issue #41). direction defaults to up（影响面）; down is the root-cause
// walk with candidates.
func cmdQueryImpact(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query impact")
	id := fs.String("id", "", "根资源 id，必填")
	direction := fs.String("direction", string(topology.DirectionUp), "收集方向：up（默认，影响面）/ down（根因候选）")
	maxDepth := fs.Int("max-depth", 8, "最大深度（1..8）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query impact 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	switch *direction {
	case topology.DirectionUp, topology.DirectionDown:
	default:
		return &usageError{msg: fmt.Sprintf("--direction 必须是 up/down（impact 不支持 both），收到 %q", *direction), usage: usage.String()}
	}
	if *maxDepth < 1 || *maxDepth > topology.MaxTraverseMaxDepth {
		return &usageError{msg: fmt.Sprintf("--max-depth 必须是 1..%d 的整数", topology.MaxTraverseMaxDepth), usage: usage.String()}
	}

	result, err := topology.Impact(ctx, a.Services.GraphStore(), *id, *direction, *maxDepth)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdQueryContext implements `query context --id [--depth 1|2]
// [--metrics latest|none]` — the CLI twin of GET /api/resources/{id}/
// context (issue #41). The §7.3 one-shot AI context document comes from the
// composition root's ONE assembly (Services.GraphContext) — the HTTP and
// CLI channels are byte-identical by construction.
func cmdQueryContext(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query context")
	id := fs.String("id", "", "资源 id，必填")
	depth := fs.Int("depth", 1, "邻域跳数：1 或 2")
	withMetrics := fs.String("metrics", "latest", "指标块：latest（默认，每序列最新点）/ none")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query context 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *depth != 1 && *depth != 2 {
		return &usageError{msg: fmt.Sprintf("--depth 必须是 1 或 2，收到 %d", *depth), usage: usage.String()}
	}
	metricsMode := true
	switch *withMetrics {
	case "latest":
		metricsMode = true
	case "none":
		metricsMode = false
	default:
		return &usageError{msg: fmt.Sprintf("--metrics 必须是 latest 或 none，收到 %q", *withMetrics), usage: usage.String()}
	}

	result, err := a.Services.GraphContext(ctx, *id, *depth, metricsMode)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdQueryNearest implements `query nearest --type T --lat F --lng F
// [--limit N]` — the SQL-level spatial query the v20 geo columns enable
// (issue #45's acceptance: "resources 有 geo 列且可做空间查询"). Returns
// the nearest resources of one type by true haversine distance within the
// documented ~1.5°×3° window (see repo.ResourceRepo.Nearest).
func cmdQueryNearest(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query nearest")
	typ := fs.String("type", "", "资源类型（注册类型之一），必填")
	lat := fs.Float64("lat", 0, "查询点纬度（-90..90），必填")
	lng := fs.Float64("lng", 0, "查询点经度（-180..180），必填")
	limit := fs.Int("limit", 5, "返回数量（1..100）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "query nearest 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *typ == "" {
		return &usageError{msg: "缺少 --type 参数", usage: usage.String()}
	}
	rt, err := domain.ParseResourceType(*typ)
	if err != nil {
		return &usageError{msg: fmt.Sprintf("--type 必须是注册资源类型之一，收到 %q", *typ), usage: usage.String()}
	}
	if *lat < -90 || *lat > 90 {
		return &usageError{msg: fmt.Sprintf("--lat 必须在 -90..90，收到 %v", *lat), usage: usage.String()}
	}
	if *lng < -180 || *lng > 180 {
		return &usageError{msg: fmt.Sprintf("--lng 必须在 -180..180，收到 %v", *lng), usage: usage.String()}
	}
	if *limit < 1 || *limit > 100 {
		return &usageError{msg: fmt.Sprintf("--limit 必须在 1..100，收到 %d", *limit), usage: usage.String()}
	}

	resources, err := a.DB.Resources.Nearest(ctx, rt, *lat, *lng, *limit)
	if err != nil {
		return err
	}
	items := make([]query.ResourceItem, 0, len(resources))
	for _, r := range resources {
		items = append(items, query.NewResourceItem(r))
	}
	return writeJSON(stdout, map[string]any{
		"type":  rt.String(),
		"lat":   *lat,
		"lng":   *lng,
		"count": len(items),
		"items": items,
	})
}

// cmdMCP implements `netopslab mcp [--db]` (also env NETOPS_DB): run the MCP
// STDIO server over the real os.Stdin/os.Stdout — stdout carries the MCP
// protocol, not the CLI JSON object.
func cmdMCP(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("mcp")
	db := fs.String("db", "", fmt.Sprintf("SQLite 数据库路径（缺省读取环境变量 %s 或 ./data/netops.db）", envDBPath))
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "mcp 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	dbPath := *db
	if dbPath == "" {
		if env := os.Getenv(envDBPath); env != "" {
			dbPath = env
		} else {
			dbPath = "./data/netops.db"
		}
	}

	a, err := openApp(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The MCP server speaks JSON-RPC over the process's real stdio; it
	// consumes the app's single service composition (W9-D) — the same
	// instances the HTTP API and the CLI use.
	return mcpserver.Run(ctx, a.Services, os.Stdin, os.Stdout)
}

// cmdTestdata implements `netopslab testdata --out <dir>`: regenerate the
// three deterministic synthetic workbooks plus expected-summary.json.
func cmdTestdata(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("testdata")
	out := fs.String("out", "testdata", "输出目录（不存在则创建）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "testdata 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if err := testfixtures.WriteAll(*out); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"out":   *out,
		"files": []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx", "expected-summary.json"},
	})
}

// ---- lab: the simulation workbench subcommand tree (PROJECT_BRIEF_P2 §9) ----

// cmdLab implements `netopslab lab …`:
//
//	lab workspace create --db <path> --name <n> [--desc <s>] [--batches id1,id2 | --all-batches]
//	lab topology      --db <path> --workspace <id>
//	lab path          --db <path> --workspace <id> --from <code> --to <code>
//	lab apply         --db <path> --workspace <id> --ops <ops.json> [--desc <s>] [--no-checks]
//	lab checks        --db <path> --workspace <id> [--run]
//	lab diff          --db <path> --workspace <id> --from <runId> --to <runId>
//	lab undo          --db <path> --workspace <id> [--changeset <id>]
//	lab checkpoint    --db <path> --workspace <id> (--list | --create --label <l> | --restore --id <cpId>)
//
// Every subcommand prints exactly one JSON object on stdout and follows the
// P1 exit-code contract (0 success / 1 failure / 2 usage). All mutations
// flow through the sim engine — no sim table is written directly here.
func cmdLab(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "缺少 lab 子命令；可用：workspace, topology, path, apply, checks, diff, undo, checkpoint"}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "workspace":
		return cmdLabWorkspace(rest, stdout, stderr)
	case "topology":
		return cmdLabTopology(rest, stdout, stderr)
	case "path":
		return cmdLabPath(rest, stdout, stderr)
	case "apply":
		return cmdLabApply(rest, stdout, stderr)
	case "checks":
		return cmdLabChecks(rest, stdout, stderr)
	case "diff":
		return cmdLabDiff(rest, stdout, stderr)
	case "undo":
		return cmdLabUndo(rest, stdout, stderr)
	case "checkpoint":
		return cmdLabCheckpoint(rest, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("lab 子命令必须是 workspace、topology、path、apply、checks、diff、undo 或 checkpoint，收到 %q", sub)}
	}
}

// cmdLabWorkspace implements `lab workspace create --db --name
// [--batches id1,id2 | --all-batches]` → MaterializeResult JSON. An absent
// --batches (and --all-batches) selects ALL completed batches — the §6
// default scope.
func cmdLabWorkspace(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return &usageError{msg: "lab workspace 的子命令必须是 create"}
	}
	fs, usage := newFlagSet("lab workspace create")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	name := fs.String("name", "", "工作台名称，必填")
	batches := fs.String("batches", "", "逗号分隔的导入批次 id（缺省为全部 completed 批次）")
	allBatches := fs.Bool("all-batches", false, "显式选择全部 completed 批次（与缺省行为相同）")
	desc := fs.String("desc", "", "工作台描述（可选，透传到 workspaces.description）")
	if err := fs.Parse(args[1:]); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *name == "" {
		return &usageError{msg: "缺少 --name 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab workspace create 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	var batchIDs []string
	if *batches != "" {
		for _, id := range strings.Split(*batches, ",") {
			if id = strings.TrimSpace(id); id != "" {
				batchIDs = append(batchIDs, id)
			}
		}
		if len(batchIDs) == 0 {
			return &usageError{msg: "--batches 需要至少一个批次 id", usage: usage.String()}
		}
	}
	// *allBatches deliberately leaves batchIDs nil (the default scope);
	// passing BOTH --batches and --all-batches is rejected to avoid the
	// ambiguous combination.
	if *allBatches && len(batchIDs) > 0 {
		return &usageError{msg: "--all-batches 与 --batches 不能同时使用", usage: usage.String()}
	}
	_ = *allBatches

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	res, err := a.Lab.Materialize(context.Background(), *name, *desc, batchIDs)
	if err != nil {
		return err
	}
	// issue #8: carry the ABSOLUTE db path this workspace lives in so
	// the value can be diffed against `serve`'s binding (its startup
	// log and GET /api/health .dbPath) — the one-field check that
	// separates "连错库" from "数据丢失".
	dbAbs := *db
	if abs, err := filepath.Abs(dbAbs); err == nil {
		dbAbs = abs
	}
	return writeJSON(stdout, labCreateResponse{MaterializeResult: res, DB: dbAbs})
}

// labCreateResponse wraps MaterializeResult with the db the workspace was
// created in (issue #8). MaterializeResult is embedded so the JSON keeps
// every original top-level field — workspaceId, name, counts, hash — and
// adds exactly one.
type labCreateResponse struct {
	sim.MaterializeResult
	DB string `json:"db"`
}

// cmdLabTopology implements `lab topology --db --workspace` → Topology JSON.
func cmdLabTopology(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab topology")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" {
		return &usageError{msg: "缺少 --workspace 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab topology 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	res, err := a.Lab.Topology(context.Background(), *workspace)
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}

// cmdLabPath implements `lab path --db --workspace --from <code> --to <code>`
// → PathResult JSON.
func cmdLabPath(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab path")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	from := fs.String("from", "", "起点设备编码，必填")
	to := fs.String("to", "", "终点设备编码，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" || *from == "" || *to == "" {
		return &usageError{msg: "缺少 --workspace、--from 或 --to 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab path 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	res, err := a.Lab.Path(context.Background(), *workspace, *from, *to)
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}

// labOpsBody is the ops file shape of `lab apply` (§9: the §7 ops request
// body {ops:[…], description?, runChecks?}). The ops array itself is
// delegated to sim.DecodeOps.
type labOpsBody struct {
	Description string `json:"description"`
	RunChecks   *bool  `json:"runChecks"`
}

// cmdLabApply implements `lab apply --db --workspace --ops <ops.json>
// [--desc <s>] [--no-checks]` → ApplyResult JSON. --desc overrides the ops
// file's description; --no-checks forces runChecks=false (file value or
// default true otherwise).
func cmdLabApply(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab apply")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	opsFile := fs.String("ops", "", "操作文件路径（§7 ops 请求体：{ops:[…], description?, runChecks?}），必填")
	desc := fs.String("desc", "", "变更集描述（覆盖 ops 文件中的 description）")
	noChecks := fs.Bool("no-checks", false, "跳过变更后的自动校验")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" || *opsFile == "" {
		return &usageError{msg: "缺少 --workspace 或 --ops 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab apply 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	raw, err := os.ReadFile(*opsFile)
	if err != nil {
		return fmt.Errorf("读取操作文件 %s: %w", *opsFile, err)
	}
	var body labOpsBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("操作文件 %s 不是合法 JSON: %w: %w", *opsFile, domain.ErrInvalidOp, err)
	}
	ops, err := sim.DecodeOps(raw)
	if err != nil {
		return err
	}
	description := body.Description
	if *desc != "" {
		description = *desc
	}
	runChecks := true
	if body.RunChecks != nil {
		runChecks = *body.RunChecks
	}
	if *noChecks {
		runChecks = false
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	res, err := a.Lab.ApplyOps(context.Background(), *workspace, ops, description, runChecks)
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}

// labCheckResult is the JSON rendering of one check result row (the CLI's
// camelCase projection of domain.CheckResult — the domain struct carries no
// json tags on purpose).
type labCheckResult struct {
	Seq        int             `json:"seq"`
	CheckCode  string          `json:"checkCode"`
	Severity   string          `json:"severity"`
	Passed     bool            `json:"passed"`
	EntityKind string          `json:"entityKind"`
	EntityID   string          `json:"entityId"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail"`
}

// labCheckResults maps stored result rows to their JSON projection (stored
// order: findings first, then the per-check baselines).
func labCheckResults(results []domain.CheckResult) []labCheckResult {
	out := make([]labCheckResult, 0, len(results))
	for _, r := range results {
		out = append(out, labCheckResult{
			Seq:        r.Seq,
			CheckCode:  r.CheckCode,
			Severity:   r.Severity.String(),
			Passed:     r.Passed,
			EntityKind: r.EntityKind,
			EntityID:   r.EntityID,
			Message:    r.Message,
			Detail:     json.RawMessage(r.DetailJSON),
		})
	}
	return out
}

// sevCountsOf counts the findings of a result row set per severity.
func sevCountsOf(results []domain.CheckResult) sim.SevCounts {
	var c sim.SevCounts
	for _, r := range results {
		if r.Passed {
			continue
		}
		switch r.Severity {
		case domain.SeverityError:
			c.Error++
		case domain.SeverityWarning:
			c.Warning++
		case domain.SeverityInfo:
			c.Info++
		}
	}
	return c
}

// labRunDetail is the JSON payload of `lab checks` without --run: the
// latest run with its full result rows (§7 GET …/checks/{runId} shape).
type labRunDetail struct {
	RunID       string           `json:"runId"`
	ChangesetID *string          `json:"changesetId"` // null = not tied to a changeset
	Trigger     string           `json:"trigger"`
	RuleVersion string           `json:"ruleVersion"`
	StartedAt   time.Time        `json:"startedAt"`
	DurationMs  int              `json:"durationMs"`
	Score       int              `json:"score"`
	Counts      sim.SevCounts    `json:"counts"`
	Results     []labCheckResult `json:"results"`
}

// cmdLabChecks implements `lab checks --db --workspace [--run]`: --run
// executes a new manual run (RunResult JSON summary); the default prints the
// LATEST run with its result rows — exit 1 not_found when none exists yet.
func cmdLabChecks(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab checks")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	run := fs.Bool("run", false, "立即执行一次新的校验（缺省打印最近一次运行）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" {
		return &usageError{msg: "缺少 --workspace 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab checks 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	if *run {
		res, err := a.Lab.RunChecks(ctx, *workspace, domain.CheckTriggerManual)
		if err != nil {
			return err
		}
		return writeJSON(stdout, res)
	}
	runRow, results, err := a.Lab.LatestRun(ctx, *workspace)
	if err != nil {
		return err
	}
	detail := labRunDetail{
		RunID:       runRow.ID,
		Trigger:     runRow.Trigger.String(),
		RuleVersion: runRow.RuleVersion,
		StartedAt:   runRow.StartedAt,
		DurationMs:  runRow.DurationMs,
		Score:       runRow.Score,
		Counts:      sevCountsOf(results),
		Results:     labCheckResults(results),
	}
	if runRow.ChangesetID != "" {
		changeset := runRow.ChangesetID
		detail.ChangesetID = &changeset
	}
	return writeJSON(stdout, detail)
}

// labRunRef is the JSON projection of a diff's run reference.
type labRunRef struct {
	RunID     string        `json:"runId"`
	StartedAt time.Time     `json:"startedAt"`
	Score     int           `json:"score"`
	Counts    sim.SevCounts `json:"counts"`
}

// labDiffResult is the JSON payload of `lab diff`: the sim.DiffResult with
// the finding rows projected to their camelCase shape.
type labDiffResult struct {
	From               labRunRef        `json:"from"`
	To                 labRunRef        `json:"to"`
	FindingsAdded      []labCheckResult `json:"findingsAdded"`
	FindingsRemoved    []labCheckResult `json:"findingsRemoved"`
	ConnectivityLost   []sim.PairRef    `json:"connectivityLost"`
	ConnectivityGained []sim.PairRef    `json:"connectivityGained"`
	ScoreDelta         int              `json:"scoreDelta"`
}

// cmdLabDiff implements `lab diff --db --workspace --from <runId> --to
// <runId>` → diff JSON.
func cmdLabDiff(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab diff")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	from := fs.String("from", "", "对比基准 run id，必填")
	to := fs.String("to", "", "对比目标 run id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" || *from == "" || *to == "" {
		return &usageError{msg: "缺少 --workspace、--from 或 --to 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab diff 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	diff, err := a.Lab.Diff(context.Background(), *workspace, *from, *to)
	if err != nil {
		return err
	}
	return writeJSON(stdout, labDiffResult{
		From: labRunRef{RunID: diff.From.RunID, StartedAt: diff.From.StartedAt,
			Score: diff.From.Score, Counts: diff.From.Counts},
		To: labRunRef{RunID: diff.To.RunID, StartedAt: diff.To.StartedAt,
			Score: diff.To.Score, Counts: diff.To.Counts},
		FindingsAdded:      labCheckResults(diff.FindingsAdded),
		FindingsRemoved:    labCheckResults(diff.FindingsRemoved),
		ConnectivityLost:   diff.ConnectivityLost,
		ConnectivityGained: diff.ConnectivityGained,
		ScoreDelta:         diff.ScoreDelta,
	})
}

// cmdLabUndo implements `lab undo --db --workspace [--changeset <id>]` →
// UndoResult JSON (checks run by default).
func cmdLabUndo(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab undo")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	changeset := fs.String("changeset", "", "要撤销的变更集 id（缺省为最近一个已应用变更集）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" {
		return &usageError{msg: "缺少 --workspace 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab undo 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	res, err := a.Lab.Undo(context.Background(), *workspace, *changeset, true)
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}

// labCheckpointItem is the JSON projection of one checkpoint row.
type labCheckpointItem struct {
	CheckpointID  string    `json:"checkpointId"`
	Label         string    `json:"label"`
	WorkspaceHash string    `json:"workspaceHash"`
	CreatedAt     time.Time `json:"createdAt"`
}

// labCheckpointList is the JSON payload of `lab checkpoint --list`.
type labCheckpointList struct {
	Items []labCheckpointItem `json:"items"`
	Total int                 `json:"total"`
}

// cmdLabCheckpoint implements `lab checkpoint --db --workspace (--list |
// --create --label <l> | --restore --id <cpId>)`: exactly one of the three
// modes is required.
func cmdLabCheckpoint(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("lab checkpoint")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	workspace := fs.String("workspace", "", "工作台 id，必填")
	list := fs.Bool("list", false, "列出工作台的检查点")
	create := fs.Bool("create", false, "创建检查点（需要 --label）")
	label := fs.String("label", "", "检查点标签（--create 时必填）")
	restore := fs.Bool("restore", false, "恢复到检查点（需要 --id）")
	id := fs.String("id", "", "检查点 id（--restore 时必填）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *workspace == "" {
		return &usageError{msg: "缺少 --workspace 参数", usage: usage.String()}
	}
	modes := 0
	for _, m := range []bool{*list, *create, *restore} {
		if m {
			modes++
		}
	}
	if modes != 1 {
		return &usageError{msg: "必须且只能指定 --list、--create --label 或 --restore --id 之一", usage: usage.String()}
	}
	if *create && *label == "" {
		return &usageError{msg: "--create 需要 --label 参数", usage: usage.String()}
	}
	if *restore && *id == "" {
		return &usageError{msg: "--restore 需要 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "lab checkpoint 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	switch {
	case *list:
		cps, err := a.Lab.ListCheckpoints(ctx, *workspace)
		if err != nil {
			return err
		}
		out := labCheckpointList{Items: make([]labCheckpointItem, 0, len(cps)), Total: len(cps)}
		for _, cp := range cps {
			out.Items = append(out.Items, labCheckpointItem{
				CheckpointID:  cp.ID,
				Label:         cp.Label,
				WorkspaceHash: cp.WorkspaceHash,
				CreatedAt:     cp.CreatedAt,
			})
		}
		return writeJSON(stdout, out)
	case *create:
		res, err := a.Lab.CreateCheckpoint(ctx, *workspace, *label)
		if err != nil {
			return err
		}
		return writeJSON(stdout, res)
	default: // *restore
		res, err := a.Lab.RestoreCheckpoint(ctx, *workspace, *id, true)
		if err != nil {
			return err
		}
		return writeJSON(stdout, res)
	}
}

// cmdRenames implements `netopslab renames --db <path> list|apply|reject …`
// — the CLI side of the rename confirmation flow (W6-D; the changes
// subcommand pattern: leading --db, then the sub-subcommand). All outputs
// are the single-JSON-object stdout contract: list → {items,total};
// apply/reject → the proposal object after the transition.
func cmdRenames(args []string, stdout, stderr io.Writer) error {
	dbPath, sub, subArgs, err := splitSubArgs("renames", "list / apply / reject", args)
	if err != nil {
		return err
	}
	a, err := openApp(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	switch sub {
	case "list":
		return cmdRenamesList(ctx, a, subArgs, stdout, stderr)
	case "apply":
		return cmdRenamesApply(ctx, a, subArgs, stdout, stderr)
	case "reject":
		return cmdRenamesReject(ctx, a, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("renames 子命令必须是 list、apply 或 reject，收到 %q", sub)}
	}
}

// cmdRenamesList implements `renames list [--status] [--type]
// [--resource-id] [--batch-id] [--limit] [--offset]`.
func cmdRenamesList(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("renames list")
	status := fs.String("status", "", "状态过滤：pending / applied / rejected / all（缺省 all）")
	typ := fs.String("type", "", "资源类型过滤：device / ip_pool / circuit / port / base_station / site / machine_room / ptp_port")
	resourceID := fs.String("resource-id", "", "按资源 id 过滤（提案任一侧：旧资源或新资源均可命中）")
	batchID := fs.String("batch-id", "", "按导入批次 id 过滤")
	limit := fs.Int("limit", renames.DefaultPageSize, "每页条数（最大 100）")
	offset := fs.Int("offset", 0, "偏移量（非负）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "renames list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	filter := renames.Filter{
		ResourceID: *resourceID,
		BatchID:    *batchID,
		Limit:      *limit,
		Offset:     *offset,
	}
	if *status != "" && *status != "all" {
		st, err := domain.ParseRenameStatus(*status)
		if err != nil {
			return &usageError{msg: fmt.Sprintf("--status 必须是 pending/applied/rejected/all，收到 %q", *status), usage: usage.String()}
		}
		filter.Status = st
	}
	if *typ != "" {
		t, err := domain.ParseResourceType(*typ)
		if err != nil {
			// W21 (v0.7.0): the closed set extends with the registry (33
			// types) — derived, never hand-copied (issue #26 discipline).
			types := make([]string, 0, len(domain.AllResourceTypes()))
			for _, rt := range domain.AllResourceTypes() {
				types = append(types, rt.String())
			}
			return &usageError{msg: fmt.Sprintf("--type 必须是注册资源类型之一（%s），收到 %q", strings.Join(types, "/"), *typ), usage: usage.String()}
		}
		filter.ResourceType = t
	}
	result, err := a.Renames.List(ctx, filter)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdRenamesApply implements `renames apply --id <proposalId>`.
func cmdRenamesApply(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("renames apply")
	id := fs.String("id", "", "待确认改名提案 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "renames apply 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	item, err := a.Renames.Apply(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, item)
}

// cmdRenamesReject implements `renames reject --id <proposalId>`.
func cmdRenamesReject(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("renames reject")
	id := fs.String("id", "", "待确认改名提案 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "缺少 --id 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "renames reject 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	item, err := a.Renames.Reject(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, item)
}
