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

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/mcpserver"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testfixtures"
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
		writeCLIError(stdout, "usage_error", "缺少子命令；可用：serve, analyze, import, validate, report, query, mcp, testdata")
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
	case "validate":
		err = cmdValidate(args[1:], stdout, stderr)
	case "report":
		err = cmdReport(args[1:], stdout, stderr)
	case "query":
		err = cmdQuery(args[1:], stdout, stderr)
	case "mcp":
		err = cmdMCP(args[1:], stdout, stderr)
	case "testdata":
		err = cmdTestdata(args[1:], stdout, stderr)
	default:
		writeCLIError(stdout, "usage_error", fmt.Sprintf("未知子命令 %q；可用：serve, analyze, import, validate, report, query, mcp, testdata", args[0]))
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
	switch {
	case errors.Is(err, workbook.ErrNotXLSX):
		return "not_xlsx"
	case errors.Is(err, workbook.ErrEmptyWorkbook):
		return "empty_workbook"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
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

// cmdServe implements `netopslab serve --addr --db --data-dir`.
func cmdServe(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("serve")
	addr := fs.String("addr", ":8080", "HTTP 监听地址")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	dataDir := fs.String("data-dir", "./data", "上传文件存储目录")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "serve 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	a, err := New(Config{Addr: *addr, DBPath: *db, DataDir: *dataDir})
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	return a.Run(context.Background())
}

// cmdAnalyze implements `netopslab analyze --file`: OFFLINE workbook
// analysis (PROJECT_BRIEF §10: "parse+suggest only, no DB write") — the
// printed Analysis JSON has the same shape as the POST /api/imports response
// except that no batch is created, so batchId stays empty. The confirmed
// import itself (analyze + execute + auto-validation) is `netopslab import`.
func cmdAnalyze(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("analyze")
	file := fs.String("file", "", "工作簿路径 (.xlsx)，必填")
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

// cmdImport implements `netopslab import --file --map --db`: analyze +
// execute the confirmed mapping + automatic set-level validation; prints the
// import summary with issue counts.
func cmdImport(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("import")
	file := fs.String("file", "", "工作簿路径 (.xlsx)，必填")
	mapFile := fs.String("map", "", "映射 JSON 文件路径（§8 结构），必填")
	db := fs.String("db", "./data/netops.db", "SQLite 数据库路径")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" || *mapFile == "" {
		return &usageError{msg: "缺少 --file 或 --map 参数", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "import 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}

	raw, err := os.ReadFile(*mapFile)
	if err != nil {
		return fmt.Errorf("读取映射文件 %s: %w", *mapFile, err)
	}
	m, err := mapping.Parse(raw)
	if err != nil {
		return fmt.Errorf("映射文件 %s 不是合法 JSON: %w", *mapFile, err)
	}

	a, err := openApp(*db)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()

	res, err := a.Importer.Analyze(ctx, *file)
	if err != nil {
		return err
	}
	sum, err := a.Importer.Execute(ctx, res.BatchID, m)
	if err != nil {
		return err
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
		_, err = io.WriteString(dest, html)
		return err
	}
	return a.Report.WriteJSON(ctx, *batch, dest)
}

// cmdQuery implements `netopslab query --db <path> resources|stats|relations …`.
// The sub-subcommand may appear before or after the query-level flags; only
// --db is accepted at the query level.
func cmdQuery(args []string, stdout, stderr io.Writer) error {
	dbPath, sub, subArgs, err := splitQueryArgs(args)
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
	default:
		return &usageError{msg: fmt.Sprintf("query 子命令必须是 resources、stats 或 relations，收到 %q", sub)}
	}
}

// splitQueryArgs separates leading query-level flags (--db[=path]) from the
// sub-subcommand (resources|stats|relations) and its own flags.
func splitQueryArgs(args []string) (dbPath, sub string, subArgs []string, err error) {
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
			return "", "", nil, &usageError{msg: fmt.Sprintf("query 不支持参数 %q（仅 --db）", arg)}
		default:
			return dbPath, arg, args[i+1:], nil
		}
	}
	return "", "", nil, &usageError{msg: "缺少 query 子命令（resources / stats / relations）"}
}

// cmdQueryResources implements `query resources --q --type --status --page
// --pageSize` (the database path is the query-level --db flag).
func cmdQueryResources(ctx context.Context, a *App, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("query resources")
	q := fs.String("q", "", "搜索关键词（natural_key/name/attributes 模糊匹配）")
	typ := fs.String("type", "", "资源类型：device / ip_pool / circuit")
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
			return &usageError{msg: fmt.Sprintf("--type 必须是 device/ip_pool/circuit，收到 %q", *typ), usage: usage.String()}
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
	// The MCP server speaks JSON-RPC over the process's real stdio.
	return mcpserver.Run(ctx, a.DB, os.Stdin, os.Stdout)
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
