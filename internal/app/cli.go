package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/mmqz/netops-fees-lab/internal/mcpserver"
	"github.com/mmqz/netops-fees-lab/internal/metrics"
	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
	"github.com/mmqz/netops-fees-lab/internal/workbook"
)

// CLI exit codes (§10): 0 success, 1 failure, 2 usage.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// envDBPath is the environment variable the mcp subcommand honors.
const envDBPath = "NETOPS_DB"

// cliSubcommands lists every top-level subcommand of the dispatch switch in
// CLI below. Both usage surfaces (missing and unknown subcommand) join this
// slice, so there is exactly ONE list to keep beside the switch — and the
// closed-set test (usage_closset_test.go) parses that switch via go/ast and
// fails on drift in either direction (#108: `backup` once shipped unlisted;
// two hand-copied strings agreeing only by luck is how it happened).
var cliSubcommands = []string{
	"serve", "analyze", "import", "ingest", "metrics",
	"validate", "report", "fees", "templates", "mcp", "gc", "backup", "testdata",
	"convert",
}

// commandEnum renders the enumeration of the "必须是 …" usage errors:
// A、B、C 或 D — the last item carries 或. Deriving it from the same slice
// the "可用：" list joins means the two surfaces can no longer disagree
// about which subcommands exist (#108).
func commandEnum(subs []string) string {
	if len(subs) == 0 {
		return ""
	}
	if len(subs) == 1 {
		return subs[0]
	}
	return strings.Join(subs[:len(subs)-1], "、") + " 或 " + subs[len(subs)-1]
}

// CLI dispatches the netopsfees subcommands. Every successful command writes
// exactly one JSON object to stdout (the report --format html command writes
// an HTML document instead, per §10); failures write one JSON error object
// {"error":{"code","message"}} to stdout; structured logs go to stderr.
// The returned value is the process exit code.
func CLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeCLIError(stdout, "usage_error", "缺少子命令；可用："+strings.Join(cliSubcommands, ", "))
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
	case "metrics":
		err = cmdMetrics(args[1:], stdout, stderr)
	case "validate":
		err = cmdValidate(args[1:], stdout, stderr)
	case "report":
		err = cmdReport(args[1:], stdout, stderr)
	case "fees":
		err = cmdFees(args[1:], stdout, stderr)
	case "templates":
		err = cmdTemplates(args[1:], stdout, stderr)
	case "mcp":
		err = cmdMCP(args[1:], stdout, stderr)
	case "convert":
		err = cmdConvert(args[1:], stdout, stderr)
	case "gc":
		err = cmdGC(args[1:], stdout, stderr)
	case "backup":
		err = cmdBackup(args[1:], stdout, stderr)
	case "testdata":
		err = cmdTestdata(args[1:], stdout, stderr)
	default:
		writeCLIError(stdout, "usage_error", fmt.Sprintf("未知子命令 %q；可用：%s", args[0], strings.Join(cliSubcommands, ", ")))
		return exitUsage
	}

	if err == nil {
		return exitOK
	}
	if errors.Is(err, errAlreadyPrinted) {
		return exitFailure
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

// errAlreadyPrinted is the sentinel a command returns when it has ALREADY
// written its own single §10 JSON object to stdout and only wants the
// non-zero exit code reflected (batch partial failures — e.g. convert with
// some files unparseable: the Result JSON on stdout carries the per-file
// reasons, dispatch must not write a second envelope).
var errAlreadyPrinted = errors.New("cli: 单 JSON 已输出，仅反射退出码")

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

// openAppData opens the app with an explicit data dir (the fees analysis
// root). cmdMCP uses this so the MCP composition resolves the SAME analysis
// tree the serve/fees channels write (#97: a bare openApp left the fee
// service rooted at the CWD-relative "analysis", silently answering zero
// risk clues).
func openAppData(dbPath, dataDir string) (*App, error) {
	a, err := New(Config{DBPath: dbPath, DataDir: dataDir})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// cmdServe implements `netopsfees serve --addr --db --data-dir
// [--auth-token T] [--admin-token A]` (issue #9: two tiers + default-safe
// bind — the naked external bind that used to be the default is refused).
func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("serve")
	addr := fs.String("addr", "127.0.0.1:8080", "HTTP 监听地址（默认仅回环；非回环绑定必须配置写令牌，见 --auth-token/--admin-token）")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	dataDir := fs.String("data-dir", "./data", "上传文件存储目录")
	authToken := fs.String("auth-token", "", "申报级写令牌：非空则所有 POST/PATCH/DELETE /api 请求需携带 Authorization: Bearer <token>；只读请求与健康检查不受影响；空 = 不启用")
	adminToken := fs.String("admin-token", "", "管理级写令牌（issue #9）：配置后删除/回滚/审核类管理级路由仅接受本令牌（申报级令牌得 403）；单独配置时本令牌是唯一写令牌；与 --auth-token 相同会被拒绝启动")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "serve 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	if err := validateServeGuards(*addr, *authToken, *adminToken); err != nil {
		return err
	}
	cfg := Config{Addr: *addr, DBPath: *db, DataDir: *dataDir, AuthToken: *authToken, AdminToken: *adminToken}
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
	// The metric channel's twin heal (#87): metric ingests write their
	// terminal status inside the ingest transaction, so a crashed previous
	// run leaves its batch importing forever — sweep it here, at the same
	// call site discipline (serve startup, before the listener starts).
	if a.Metrics != nil {
		if n, err := a.Metrics.RecoverInterrupted(ctx); err != nil {
			return fmt.Errorf("recover interrupted metric batches: %w", err)
		} else if n > 0 {
			a.Log.Info("recovered interrupted metric batches", "count", n)
		}
	}

	// Garbage collection (W5-D uploads + #106 fee analysis), same placement
	// rationale as RecoverInterrupted above: run serially BEFORE the
	// listener starts, so no upload or fee import of this (or any other)
	// process can be in flight while we judge orphans — a file being
	// written right now is not yet referenced by any batch and would be
	// misjudged (the fees CLI is a separate process; GCAnalysis's mtime
	// milestone covers that cross-process window). The MCP process never
	// reaches this call site (it owns no serve loop).
	a.gcSweep(ctx)
	return a.Run(ctx)
}

// validateServeGuards enforces the two v0.18/issue #9 startup refusals:
// (1) the two tokens must differ (a declarer tier that secretly holds
// admin rights is a broken model, refuse it loudly);
// (2) a non-loopback bind without ANY write token is the naked-network
// default the issue kills — the operator must either configure a token or
// bind loopback explicitly. Both answer *usageError (exit 2).
func validateServeGuards(addr, authToken, adminToken string) error {
	if authToken != "" && adminToken != "" && authToken == adminToken {
		return &usageError{msg: "--admin-token 与 --auth-token 不能相同：两级模型中申报级令牌将持有管理级权限，模型失效；请设置两个不同的令牌"}
	}
	if !loopbackOnly(addr) && authToken == "" && adminToken == "" {
		return &usageError{msg: fmt.Sprintf("拒绝启动：--addr %s 绑定到非回环地址（对外可达）但未配置任何写令牌（--auth-token / --admin-token），删除/回滚等写操作将裸奔；要么配置令牌，要么显式绑定回环地址（如 127.0.0.1:8080）", addr)}
	}
	return nil
}

// loopbackOnly reports whether the listen address binds a loopback
// interface ONLY (issue #9's default-safety predicate). The host part of
// addr is examined statically: an empty host (":8080" / "0.0.0.0:8080" /
// "[::]:8080") binds every interface — NOT loopback-only; a literal
// loopback IP or "localhost" is; any other resolvable name is treated as
// external (fail-safe: the guard fires, the operator states their intent
// explicitly with a token or a loopback bind).
func loopbackOnly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port at all ("localhost", "0.0.0.0"): treat as a bare host.
		host = addr
	}
	if host == "" {
		return false // ":8080" — every interface.
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// cmdAnalyze implements `netopsfees analyze --file`: OFFLINE workbook
// analysis (PROJECT_BRIEF §10: "parse+suggest only, no DB write") — the
// printed Analysis JSON has the same shape as the POST /api/imports response
// except that no batch is created, so batchId stays empty. The confirmed
// import itself (analyze + execute + auto-validation) is `netopsfees import`.
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

// cmdImport implements `netopsfees import --file --map --db` and the W24
// unattended variant `netopsfees import --file --db --auto`: analyze +
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
	// Metric channel twin heal (#87) — same placement discipline.
	if a.Metrics != nil {
		if n, err := a.Metrics.RecoverInterrupted(ctx); err != nil {
			return fmt.Errorf("recover interrupted metric batches: %w", err)
		} else if n > 0 {
			a.Log.Info("recovered interrupted metric batches", "count", n)
		}
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

// templatesSubcommands lists the dispatcher's cases; both usage surfaces
// derive from it and the closed-set test pins it against the switch (see
// cliSubcommands for the #108 rationale).
var templatesSubcommands = []string{"save", "list", "delete"}

// cmdTemplates implements the W24 mapping template subcommands
// (§12-B7 方案 A, the CLI twin of the HTTP template endpoints):
//
//	netopsfees templates --db d save --batch <id> [--name n]
//	netopsfees templates --db d list [--page P] [--pageSize S]
//	netopsfees templates --db d delete --id <tpl_id>
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
		return &usageError{msg: "缺少 templates 子命令；可用：" + strings.Join(templatesSubcommands, ", ")}
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
		return &usageError{msg: fmt.Sprintf("templates 子命令必须是 %s，收到 %q", commandEnum(templatesSubcommands), sub)}
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

// cmdIngest implements `netopsfees ingest --file x.xlsx [--map m.json]
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

// validateResponse mirrors POST /api/validate.
type validateResponse struct {
	Issues map[string]int `json:"issues"`
	Score  int            `json:"score"`
}

// cmdValidate implements `netopsfees validate --db [--batch]`: recompute the
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
		for sev, n := range rowPhase {
			issues[sev.String()] += n
		}
		// #102: the set-level half is a GROUP BY, not a full-table
		// materialization + in-memory filter of every issue row.
		setLevel, err := a.DB.Issues.CountSetLevelBySeverity(ctx)
		if err != nil {
			return err
		}
		for sev, n := range setLevel {
			issues[sev.String()] += n
		}
	}

	score := 100 - 5*issues[domain.SeverityError.String()] - issues[domain.SeverityWarning.String()]
	if score < 0 {
		score = 0
	}
	return writeJSON(stdout, validateResponse{Issues: issues, Score: score})
}

// cmdReport implements `netopsfees report --db [--batch] --format html|json
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

// cmdMCP implements `netopsfees mcp [--db] [--data]` (also env NETOPS_DB): run
// the MCP STDIO server over the real os.Stdin/os.Stdout — stdout carries the
// MCP protocol, not the CLI JSON object. --data roots the fee analysis layer
// at the SAME directory serve (--data-dir) and the fees CLI (--data) use
// (#97); before that flag the composition silently answered fee_risks from a
// CWD-relative "analysis" tree.
func cmdMCP(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("mcp")
	db := fs.String("db", "", fmt.Sprintf("SQLite 数据库路径（缺省读取环境变量 %s 或 ./data/netops.db）", envDBPath))
	data := fs.String("data", "./data", "费用分析数据目录（快照与分析树根；须与 fees --data / serve --data-dir 指向同一目录，缺省 ./data）")
	readOnly := fs.Bool("read-only", false, "只读模式（issue #10）：只注册标注 readOnlyHint=true 的纯查询工具（当前即全部 4 个）——纯分析接入的显式声明，未来出现写工具时本模式天然不含它")
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

	a, err := openAppData(dbPath, *data)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The MCP server speaks JSON-RPC over the process's real stdio; it
	// consumes the app's single service composition (W9-D) — the same
	// instances the HTTP API and the CLI use. --read-only (issue #10) swaps
	// in the variant that registers only the readOnlyHint-annotated tools.
	if *readOnly {
		return mcpserver.RunReadOnly(ctx, a.Services, os.Stdin, os.Stdout)
	}
	return mcpserver.Run(ctx, a.Services, os.Stdin, os.Stdout)
}

// cmdTestdata implements `netopsfees testdata --out <dir>`: regenerate the
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
