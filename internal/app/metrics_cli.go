package app

// metrics_cli.go — the CLI side of the metric batch lifecycle (#115):
// list and rollback. The ingest channel itself is `netopslab ingest`
// (one-shot, W20); this command closes the debt api.md carried since
// v0.7 — POST /api/metrics/batches/{id}/rollback stayed HTTP-only while
// the resource and fee channels both had CLI rollbacks, leaving the
// metric channel the only one an offline operator could not undo.
// Subcommands, flags and outputs mirror the channel's own HTTP contract
// (same JSON shapes, same error taxonomy: non-completed → conflict /
// exit 1, unknown id → not_found / exit 1, usage → exit 2).

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/metrics"
)

// metricsSubcommands lists the metrics dispatcher's cases; both usage
// surfaces derive from it (see cliSubcommands for the #108 rationale —
// the closed-set test pins it against the switch via go/ast).
var metricsSubcommands = []string{"list", "forecast", "rollback"}

// cmdMetrics implements `netopslab metrics --db <path> list|rollback …`.
func cmdMetrics(args []string, stdout, stderr io.Writer) error {
	dbPath, sub, subArgs, err := splitSubArgs("metrics", strings.Join(metricsSubcommands, " / "), args)
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
		return cmdMetricsList(ctx, a.Metrics, subArgs, stdout, stderr)
	case "forecast":
		return cmdMetricsForecast(ctx, a.Metrics, subArgs, stdout, stderr)
	case "rollback":
		return cmdMetricsRollback(ctx, a.Metrics, subArgs, stdout, stderr)
	default:
		// unreachable: splitSubArgs returns the sub only if non-flag; the
		// closed-set test keeps this honest anyway
		return &usageError{msg: fmt.Sprintf("metrics 子命令必须是 %s，收到 %q", commandEnum(metricsSubcommands), sub)}
	}
}

// cmdMetricsForecast implements `metrics forecast --resource --key
// [--window N]` (ADR-0011 D4, #123): the least-squares linear trend over
// the series's daily averages with N-days-to-threshold and the 80% band.
// <14 daily points answer the honest refusal, exit non-zero.
func cmdMetricsForecast(ctx context.Context, svc *metrics.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("metrics forecast")
	resource := fs.String("resource", "", "资源 id（uuid；端口类资源有性能序列），必填")
	key := fs.String("key", "", "指标键（封闭目录，netopslab metrics 或 GET /api/metrics/keys 可查），必填")
	window := fs.Int("window", 30, "外推天数（1–90，缺省 30）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *resource == "" || *key == "" {
		return &usageError{msg: "metrics forecast 需要 --resource、--key", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "metrics forecast 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	out, err := svc.Forecast(ctx, *resource, *key, *window)
	if err != nil {
		return err
	}
	return writeJSON(stdout, out)
}

// cmdMetricsList implements `metrics list [--page N] [--page-size N]` —
// the paged batch list, newest first (GET /api/metrics/batches same
// shape: items carry id/workbookName/sha256/status/row counts and
// timestamps; status is the string form).
func cmdMetricsList(ctx context.Context, svc *metrics.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("metrics list")
	page := fs.Int("page", 1, "页码（从 1 起）")
	pageSize := fs.Int("page-size", 20, "每页条数（1–100）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "metrics list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *page < 1 {
		return &usageError{msg: "page 必须从 1 起", usage: usage.String()}
	}
	if *pageSize < 1 || *pageSize > 100 {
		return &usageError{msg: "page-size 必须在 1–100", usage: usage.String()}
	}
	batches, total, err := svc.ListBatches(ctx, *pageSize, (*page-1)**pageSize)
	if err != nil {
		return err
	}
	type batchItem struct {
		ID           string     `json:"id"`
		WorkbookName string     `json:"workbookName"`
		SHA256       string     `json:"sha256"`
		Status       string     `json:"status"`
		RowsTotal    int        `json:"rowsTotal"`
		RowsImported int        `json:"rowsImported"`
		RowsFailed   int        `json:"rowsFailed"`
		Error        string     `json:"error,omitempty"`
		StartedAt    time.Time  `json:"startedAt"`
		FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	}
	items := make([]batchItem, 0, len(batches))
	for _, b := range batches {
		items = append(items, batchItem{
			ID: b.ID, WorkbookName: b.WorkbookName, SHA256: b.WorkbookSHA256,
			Status: b.Status.String(), RowsTotal: b.RowsTotal, RowsImported: b.RowsImported,
			RowsFailed: b.RowsFailed, Error: b.Error, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt,
		})
	}
	return writeJSON(stdout, struct {
		Items    []batchItem `json:"items"`
		Total    int         `json:"total"`
		Page     int         `json:"page"`
		PageSize int         `json:"pageSize"`
	}{Items: items, Total: total, Page: *page, PageSize: *pageSize})
}

// cmdMetricsRollback implements `metrics rollback --batch <id>` — the CLI
// closure of the HTTP rollback (same engine, same semantics: deletes the
// points the batch CURRENTLY owns — a later batch's overwrite survives —
// prunes now-empty series, flips the status to rolled_back).
func cmdMetricsRollback(ctx context.Context, svc *metrics.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("metrics rollback")
	batch := fs.String("batch", "", "要回滚的指标批次 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *batch == "" {
		return &usageError{msg: "metrics rollback 需要 --batch", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "metrics rollback 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	res, err := svc.Rollback(ctx, *batch)
	if err != nil {
		// the taxonomy parity the HTTP contract pins: non-completed batch
		// → conflict (exit 1), unknown id → not_found (exit 1) — the
		// cliErrorCode mapping handles both via the domain sentinel errors.
		return err
	}
	return writeJSON(stdout, res)
}
