// Package mcpserver exposes the NetOps Lab MCP STDIO server
// (PROJECT_BRIEF §10). It registers EXACTLY ONE tool — resource_search
// (read-only search over imported resources) — and nothing else: no SQL,
// shell, SSH or file tools, no production write capability of any kind (the
// SEC non-negotiable of PROJECT_BRIEF §1).
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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// Server identity constants (§10).
const (
	ServerName    = "netops-lab"
	ServerVersion = domain.AppVersion
)

// Tool name — the single registered tool.
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
	Type   string `json:"type,omitempty" jsonschema:"资源类型过滤：device、ip_pool 或 circuit"`
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

// NewServer builds the MCP server with the single resource_search tool
// registered against db. Exported for tests and future embedding.
func NewServer(db *repo.DB) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: ServerName, Version: ServerVersion},
		&mcp.ServerOptions{
			// Logs go to stderr — stdout carries the MCP protocol.
			Logger: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolName,
		Description: "只读检索 NetOps Lab 中已导入的网络资源（设备 / 地址池 / 电路）。返回 total 与 items，每个条目含 id、type、naturalKey、name、status、attributes 及其来源（workbook/sheet/rowNumber）或 applicationGenerated 标记。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		out, err := resourceSearch(ctx, db, in)
		if err != nil {
			// A ToolHandlerFor error is packed into the tool result with
			// IsError = true (MCP tool error semantics) — exactly what the
			// §10 contract wants.
			return nil, searchOutput{}, err
		}
		return nil, out, nil
	})
	return server
}

// Run starts the MCP server over the given stdin/stdout pair and blocks
// until the client closes the connection or ctx is cancelled. Use it with
// the process's real os.Stdin/os.Stdout for the CLI, and with os.Pipe ends
// in tests.
func Run(ctx context.Context, db *repo.DB, stdin io.Reader, stdout io.Writer) error {
	server := NewServer(db)
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

// resourceSearch executes the search: repo.Resources.Search with the same
// filter semantics as the HTTP API, plus per-item source resolution through
// the source_refs of the resource's creating batch.
func resourceSearch(ctx context.Context, db *repo.DB, in searchInput) (searchOutput, error) {
	filter := repo.ResourceSearchFilter{Q: in.Q}
	if in.Type != "" {
		t, err := domain.ParseResourceType(in.Type)
		if err != nil {
			return searchOutput{}, fmt.Errorf("type 必须是 device、ip_pool 或 circuit，收到 %q", in.Type)
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

	resources, total, err := db.Resources.Search(ctx, filter)
	if err != nil {
		return searchOutput{}, fmt.Errorf("检索失败：%w", err)
	}

	items := make([]searchItem, 0, len(resources))
	for _, r := range resources {
		attrs := r.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		item := searchItem{
			ID:         r.ID,
			Type:       r.Type.String(),
			NaturalKey: r.NaturalKey,
			Name:       r.Name,
			Status:     r.Status,
			Attributes: attrs,
		}
		// Resolve the creating source ref (the ref whose batch created the
		// resource; re-imports skip rows and never add refs, so exactly one
		// ref exists for imported resources).
		refs, refErr := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, r.ID)
		if refErr != nil {
			return searchOutput{}, fmt.Errorf("解析来源失败：%w", refErr)
		}
		for _, ref := range refs {
			if ref.BatchID == r.FirstBatchID {
				item.Source = &source{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
				break
			}
		}
		if item.Source == nil && len(refs) > 0 {
			ref := refs[0]
			item.Source = &source{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
		}
		if item.Source == nil {
			item.ApplicationGenerated = true
		}
		items = append(items, item)
	}

	return searchOutput{Total: total, Items: items, ApplicationGenerated: false}, nil
}
