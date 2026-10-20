// Package mcpserver exposes the NetOps Lab MCP STDIO server: resource_search
// of PROJECT_BRIEF §10, the four simulation-workbench lab tools of
// PROJECT_BRIEF_P2 §8 (lab_apply_ops, lab_run_checks, lab_diff_undo,
// lab_checkpoint), the five read-only analysis tools of v0.9 R4
// (graph_traverse, graph_impact, graph_context, alerts_query, metrics_query)
// and the fee risk tool of v0.15 (#58 stage 5). The tool surface is a
// CLOSED READ-ONLY SET pinned by TestMCPToolRegistry (the count lives in
// the registry test, not here — a number frozen in prose is a sentence
// that lies on every addition): no SQL, shell, SSH or file tools, no
// production write capability of any kind (the SEC non-negotiable of
// PROJECT_BRIEF §1 and PROJECT_BRIEF_P2 §1; later additions widened only the
// READ surface — ack/clear and detection runs stay human/HTTP/CLI).
//
// The AI mutates simulation state ONLY through these tools, and only by
// typed application operations on an existing workspace — no tool can create
// or delete a workspace (materialization stays a human/HTTP action), and
// restore_* op codes are rejected inside lab_apply_ops because the engine's
// own decoder guards every external boundary. The R4 analysis tools are
// strictly read-only: the resource domain stays human-write-only, alert
// lifecycle edges (ack/clear) stay human-only, and detection runs stay
// explicit operator actions.
//
// The server speaks MCP over newline-delimited JSON-RPC 2.0 via the
// modelcontextprotocol go-sdk v1.8.0 (IOTransport over the caller-provided
// stdin/stdout).
package mcpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/services"
)

// Server identity constants (§8): name netops-lab, version domain.AppVersion
// (v0.2.0 in Phase 2 — reported by the initialize handshake automatically).
const (
	ServerName    = "netops-lab"
	ServerVersion = domain.AppVersion
)

// Tool name — the Phase 1 read-only search tool (unchanged, §10). The four
// Phase 2 lab tools are registered alongside it in labtools.go; together
// they are the exactly five tools of PROJECT_BRIEF_P2 §8.
const toolName = "resource_search"

// Tool input/output limits (§10): limit defaults to 20 and is capped at 100.
const (
	defaultLimit = 20
	maxLimit     = 100
)

// searchInput is the tool's input schema (all fields optional). The
// jsonschema tags are plain prose descriptions (the google/jsonschema-go
// contract: the whole tag is the property description; a "WORD=" prefix is
// reserved and rejected).
type searchInput struct {
	Q      string `json:"q,omitempty" jsonschema:"搜索关键词：natural_key/name/attributes 不区分大小写的包含匹配"`
	Type   string `json:"type,omitempty" jsonschema:"资源类型过滤：device、ip_pool、circuit、port、base_station、site、machine_room 或 ptp_port"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 20，最大 100"`
	Offset *int   `json:"offset,omitempty" jsonschema:"返回起始偏移，缺省 0"`
}

// source is the resolved provenance of one result item.
type source struct {
	Workbook  string `json:"workbook"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
}

// searchItem is one result resource. Every item carries either its resolved
// workbook source or applicationGenerated: true.
type searchItem struct {
	ID                   string            `json:"id"`
	Type                 string            `json:"type"`
	NaturalKey           string            `json:"naturalKey"`
	Name                 string            `json:"name"`
	Status               string            `json:"status"`
	Attributes           map[string]string `json:"attributes"`
	Source               *source           `json:"source,omitempty"`
	ApplicationGenerated bool              `json:"applicationGenerated"`
}

// searchOutput is the tool's structured result (§10): total plus one page
// of items. ApplicationGenerated is always false at the top level — the
// items themselves carry the per-item provenance.
type searchOutput struct {
	Total                int          `json:"total"`
	Items                []searchItem `json:"items"`
	ApplicationGenerated bool         `json:"applicationGenerated"`
}

// NewServer builds the MCP server with the five registered tools
// (PROJECT_BRIEF_P2 §8): the Phase 1 resource_search plus the four lab tools.
// W9-D: the server consumes the shared *services.Services composition root —
// resource_search reads through the SAME query service the HTTP API uses
// (query.Service.SearchResourcesWithSource, batched provenance resolution),
// and the lab tools bind to the SAME sim.Service engine (svc.Lab) — no more
// locally-built second engine, exactly like the HTTP API (no SQL here).
// Exported for tests and future embedding.
func NewServer(svc *services.Services) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{
			// Logs go to stderr — stdout carries the MCP protocol.
			Logger: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName,
		Description: "只读检索 NetOps Lab 中已导入的网络资源（设备 / 地址池 / 电路 / 端口 / 基站 / 局站 / 机房 / PTP端口）。返回 total 与 items，每个条目含 id、type、naturalKey、name、status、attributes 及其来源（workbook/sheet/rowNumber）或 applicationGenerated 标记。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		out, err := resourceSearch(ctx, svc.Query, in)
		if err != nil {
			// A ToolHandlerFor error is packed into the tool result with
			// IsError = true (MCP tool error semantics) — exactly what the
			// §10 contract wants.
			return nil, searchOutput{}, err
		}
		return nil, out, nil
	})
	addLabTools(server, svc.Lab)
	addGraphTools(server, svc)
	addFeeTools(server, svc)
	return server
}

// Run starts the MCP server over the given stdin/stdout pair and blocks
// until the client closes the connection or ctx is cancelled. Use it with
// the process's real os.Stdin/os.Stdout for the CLI, and with os.Pipe ends
// in tests. W9-D: takes the shared *services.Services (app.New's single
// composition root) instead of a bare db.
func Run(ctx context.Context, svc *services.Services, stdin io.Reader, stdout io.Writer) error {
	server := NewServer(svc)
	transport := &mcp.IOTransport{
		Reader: io.NopCloser(stdin),
		Writer: nopWriteCloser{stdout},
	}
	return server.Run(ctx, transport)
}

// nopWriteCloser adapts an io.Writer to the io.WriteCloser the IOTransport
// wants without closing the underlying stream (the caller owns it).
type nopWriteCloser struct{ io.Writer }

func (w nopWriteCloser) Close() error { return nil }

// resourceSearch executes the search: the shared query service's
// SearchResourcesWithSource (W9-D) with the same filter semantics as before
// (repo.ResourceSearchFilter under the hood), with per-item source resolution
// batched into ONE source_refs query (the pre-W9-D implementation issued one
// ListByEntity per result row — an N+1). The input validation and the
// user-facing error texts below are the tool's wire contract and stay
// byte-identical; the output JSON shape is searchOutput, unchanged.
func resourceSearch(ctx context.Context, svc *query.Service, in searchInput) (searchOutput, error) {
	filter := query.SourceSearchFilter{Q: in.Q}
	if in.Type != "" {
		t, err := domain.ParseResourceType(in.Type)
		if err != nil {
			// W21 (v0.7.0, design §7.6): the type list auto-extends with the
			// registry (33 types now) — derived from domain.AllResourceTypes,
			// never hand-copied, so this error can't drift from the closed set.
			types := make([]string, 0, len(domain.AllResourceTypes()))
			for _, rt := range domain.AllResourceTypes() {
				types = append(types, rt.String())
			}
			return searchOutput{}, fmt.Errorf("type 必须是注册资源类型之一（%s），收到 %q", strings.Join(types, "、"), in.Type)
		}
		filter.Type = t
	}
	limit := defaultLimit
	if in.Limit != nil {
		if *in.Limit < 0 {
			return searchOutput{}, fmt.Errorf("limit 必须是不小于 0 的整数，收到 %d", *in.Limit)
		}
		if *in.Limit > maxLimit {
			limit = maxLimit
		} else {
			limit = *in.Limit
		}
	}
	offset := 0
	if in.Offset != nil {
		if *in.Offset < 0 {
			return searchOutput{}, fmt.Errorf("offset 必须是不小于 0 的整数，收到 %d", *in.Offset)
		}
		offset = *in.Offset
	}
	filter.Limit = limit
	filter.Offset = offset

	results, total, err := svc.SearchResourcesWithSource(ctx, filter)
	if err != nil {
		return searchOutput{}, fmt.Errorf("检索失败：%w", err)
	}

	items := make([]searchItem, 0, len(results))
	for _, r := range results {
		attrs := r.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		item := searchItem{
			ID:         r.ID,
			Type:       r.Type,
			NaturalKey: r.NaturalKey,
			Name:       r.Name,
			Status:     r.Status,
			Attributes: attrs,
		}
		if r.Source != nil {
			item.Source = &source{Workbook: r.Source.Workbook, Sheet: r.Source.Sheet, RowNumber: r.Source.RowNumber}
		}
		if item.Source == nil {
			item.ApplicationGenerated = true
		}
		items = append(items, item)
	}

	return searchOutput{Total: total, Items: items, ApplicationGenerated: false}, nil
}
