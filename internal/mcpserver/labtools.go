// The four Phase 2 lab tools of PROJECT_BRIEF_P2 §8: lab_apply_ops,
// lab_run_checks, lab_diff_undo and lab_checkpoint. Together with the Phase 1
// resource_search they form the EXACTLY FIVE tools of the MCP surface — the
// AI/TeleAgent gateway to the simulation workbench.
//
// Every tool binds the same engine path as the HTTP API (PROJECT_BRIEF_P2 §7
// "all bind the same engine path as HTTP"): handlers call internal/sim
// methods and NOTHING else — this package contains no SQL and never touches
// the simulation tables directly. The non-negotiables of §1 hold by
// construction:
//
//   - no tool creates or deletes workspaces (Materialize/DeleteWorkspace are
//     HTTP/CLI-only; the registry test pins the closed five-tool list);
//   - restore_* op codes are rejected inside lab_apply_ops: the typed ops
//     round-trip through sim.DecodeOps, which refuses every non-forward code
//     (engine-internal inverses never cross an external boundary);
//   - every mutation is a typed application operation on an explicit
//     workspace id, applied atomically by the engine.
//
// Tool errors use MCP tool-error semantics: the handler returns an error and
// the go-sdk packs it into the tool result with IsError = true. The message
// mirrors the HTTP error layer ("<code>: <message>", the §7 codes
// invalid_request / not_found / conflict / not_latest / op_failed /
// checkpoint_missing_file) so an agent sees the same failure vocabulary
// regardless of the boundary it used.
//
// jsonschema tags are plain prose descriptions (google/jsonschema-go
// contract; a "WORD=" prefix panics the schema generator — the Phase 1 lesson
// of docs/adr/0003).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/sim"
)

// Tool names of the four Phase 2 lab tools (§8). With resource_search these
// are the exactly five registered tools.
const (
	labApplyOpsName         = "lab_apply_ops"
	labRunChecksName        = "lab_run_checks"
	labDiffUndoName         = "lab_diff_undo"
	labCheckpointName       = "lab_checkpoint"
	diffMode                = "diff"
	undoMode                = "undo"
	checkpointActionCreate  = "create"
	checkpointActionList    = "list"
	checkpointActionRestore = "restore"
)

// addLabTools registers the four lab tools against the engine service. It is
// called by NewServer only — the tool surface stays closed.
func addLabTools(server *mcp.Server, svc *sim.Service) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        labApplyOpsName,
		Description: "向指定仿真工作台应用一组类型化前向操作（变更集，原子事务）：配置接口 IPv4、启停设备/接口/链路、创建/删除设备、接口、链路、注入链路故障等。restore_* 为引擎内部操作码，会被拒绝。成功返回 changesetId、trigger=user、opsApplied、workspaceHash 与自动校验摘要 checks（runChecks=false 时为 null）。语义失败（如链路不存在）回滚整个变更集并返回 op_failed 工具错误。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in applyOpsInput) (*mcp.CallToolResult, applyOpsOutput, error) {
		return labApplyOps(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        labRunChecksName,
		Description: "对指定仿真工作台的当前状态执行五项确定性校验（重复主机地址、子网重叠、孤立设备、部分分区、未配置接口），返回 runId、counts{error,warning,info}、score、workspaceHash 与仅含问题的紧凑结果列表 results（每行 checkCode/severity/passed/entityKind/entityId/message）。触发器为 mcp。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in runChecksInput) (*mcp.CallToolResult, runChecksOutput, error) {
		return labRunChecks(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        labDiffUndoName,
		Description: "mode=diff：对比同一工作台两次校验运行的前后差异（findingsAdded/findingsRemoved、connectivityLost/connectivityGained、scoreDelta），需 fromRunId 与 toRunId。mode=undo：撤销变更集（默认最新已应用者）——按逆序应用其记录的逆操作生成新的 undo 变更集，返回 reverted、opsApplied 与恢复后的 workspaceHash。两种模式的关联字段缺失或多余都会返回工具错误。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in diffUndoInput) (*mcp.CallToolResult, diffUndoOutput, error) {
		return labDiffUndo(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        labCheckpointName,
		Description: "工作台检查点管理：action=create（label 必填，返回 checkpointId/workspaceHash/createdAt）、action=list（返回 items 与 total）、action=restore（checkpointId 必填，将当前状态恢复为快照状态——作为一次普通前向操作变更集，返回 opsApplied/restoredToHash 并重新校验）。无法创建或删除工作台（仅 HTTP/CLI 可创建）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in checkpointInput) (*mcp.CallToolResult, checkpointOutput, error) {
		return labCheckpoint(ctx, svc, in)
	})
}

// ---- lab_apply_ops ----

// applyOpsInput is the typed input of lab_apply_ops. Ops uses the engine's
// typed sim.Op directly: the go-sdk derives the input schema from the struct
// (a plain struct with json tags — no generator panic, verified by the
// handshake tests; pointer fields simply allow JSON null). The schema gives
// the agent a closed, self-documenting op surface (unknown op fields are
// rejected), and the handler still round-trips the decoded ops through
// sim.DecodeOps so the ENGINE stays the single authority for the closed op
// code set and the restore_* rejection (§1.8) — the exact HTTP/CLI path.
type applyOpsInput struct {
	WorkspaceID string   `json:"workspaceId" jsonschema:"目标工作台 id（uuid，形如 3f0c…；不能通过 MCP 创建，需先经 HTTP/CLI 物化）"`
	Ops         []sim.Op `json:"ops" jsonschema:"前向操作数组，op 字段区分操作码：create_device{code,name}、set_device_status{deviceId,status}、delete_device{deviceId}、create_interface{deviceId,name,status?,ipv4Addr?,ipv4Prefix?}、set_interface_status{interfaceId,status}、config_interface_ipv4{interfaceId,ipv4Addr,ipv4Prefix}（null/null 表示清除）、delete_interface{interfaceId}、create_link{aInterfaceId,zInterfaceId,status?,label?}、set_link_status{linkId,status}、delete_link{linkId}；restore_* 为引擎内部操作码，会被拒绝"`
	Description string   `json:"description,omitempty" jsonschema:"变更集描述（可选）"`
	RunChecks   *bool    `json:"runChecks,omitempty" jsonschema:"应用后立即运行校验并附带 checks 摘要，缺省 true"`
}

// applyOpsOutput mirrors sim.ApplyResult (§7/§8 wire shape): checks is null
// when the caller set runChecks=false (§8 "checks:{…}|null").
type applyOpsOutput struct {
	ChangeSetID   string          `json:"changesetId"`
	Trigger       string          `json:"trigger"`
	OpsApplied    int             `json:"opsApplied"`
	WorkspaceHash string          `json:"workspaceHash"`
	Checks        *sim.RunSummary `json:"checks"`
}

// labApplyOps decodes the typed ops through the engine (closed op-code set,
// restore_* rejection) and applies them as ONE atomic changeset.
func labApplyOps(ctx context.Context, svc *sim.Service, in applyOpsInput) (*mcp.CallToolResult, applyOpsOutput, error) {
	// Re-encode the typed ops and run the engine's own strict decoder — the
	// same gate the HTTP/CLI ops bodies pass (§1.8: restore_* rejected at
	// every external boundary).
	raw, err := json.Marshal(struct {
		Ops []sim.Op `json:"ops"`
	}{Ops: in.Ops})
	if err != nil {
		return nil, applyOpsOutput{}, fmt.Errorf("invalid_request: marshal ops: %v", err)
	}
	ops, err := sim.DecodeOps(raw)
	if err != nil {
		return nil, applyOpsOutput{}, fmt.Errorf("invalid_request: %v", err)
	}
	runChecks := true
	if in.RunChecks != nil {
		runChecks = *in.RunChecks
	}
	res, err := svc.ApplyOps(ctx, in.WorkspaceID, ops, in.Description, runChecks)
	if err != nil {
		return nil, applyOpsOutput{}, simToolError(err)
	}
	return nil, applyOpsOutput{
		ChangeSetID:   res.ChangeSetID,
		Trigger:       res.Trigger,
		OpsApplied:    res.OpsApplied,
		WorkspaceHash: res.WorkspaceHash,
		Checks:        res.Checks,
	}, nil
}

// ---- lab_run_checks ----

// runChecksInput is the typed input of lab_run_checks.
type runChecksInput struct {
	WorkspaceID string `json:"workspaceId" jsonschema:"目标工作台 id（uuid）"`
}

// checkFindingRow is one compact findings-only result row (§8): no seq, no
// detail blob — the fields an agent needs to triage. passed is always false
// (findings only; the five passed baselines are omitted).
type checkFindingRow struct {
	CheckCode  string `json:"checkCode"`
	Severity   string `json:"severity"`
	Passed     bool   `json:"passed"`
	EntityKind string `json:"entityKind"`
	EntityID   string `json:"entityId"`
	Message    string `json:"message"`
}

// runChecksOutput is the compact run summary of §8.
type runChecksOutput struct {
	RunID         string            `json:"runId"`
	Counts        sim.SevCounts     `json:"counts"`
	Score         int               `json:"score"`
	WorkspaceHash string            `json:"workspaceHash"`
	Results       []checkFindingRow `json:"results"`
}

// labRunChecks runs the five deterministic checks (trigger "mcp") and returns
// the findings only.
func labRunChecks(ctx context.Context, svc *sim.Service, in runChecksInput) (*mcp.CallToolResult, runChecksOutput, error) {
	run, err := svc.RunChecks(ctx, in.WorkspaceID, domain.CheckTriggerMCP)
	if err != nil {
		return nil, runChecksOutput{}, simToolError(err)
	}
	rows := make([]checkFindingRow, 0, len(run.Results))
	for _, res := range run.Results {
		if res.Passed {
			continue // findings only — compact for AI consumption
		}
		rows = append(rows, checkFindingRow{
			CheckCode:  res.CheckCode,
			Severity:   res.Severity.String(),
			Passed:     res.Passed,
			EntityKind: res.EntityKind,
			EntityID:   res.EntityID,
			Message:    res.Message,
		})
	}
	return nil, runChecksOutput{
		RunID:         run.RunID,
		Counts:        run.Counts,
		Score:         run.Score,
		WorkspaceHash: run.WorkspaceHash,
		Results:       rows,
	}, nil
}

// ---- lab_diff_undo ----

// diffUndoInput is the typed input of lab_diff_undo: one discriminating mode
// field plus the correlating fields of each mode (§8: missing/extra
// correlating fields ⇒ tool error).
type diffUndoInput struct {
	WorkspaceID string `json:"workspaceId" jsonschema:"目标工作台 id（uuid）"`
	Mode        string `json:"mode" jsonschema:"diff 或 undo：diff 对比两次校验运行；undo 撤销一个变更集"`
	FromRunID   string `json:"fromRunId,omitempty" jsonschema:"对比基准运行 id（diff 模式必填，如 run-1）"`
	ToRunID     string `json:"toRunId,omitempty" jsonschema:"对比目标运行 id（diff 模式必填，如 run-2）"`
	ChangeSetID string `json:"changesetId,omitempty" jsonschema:"要撤销的变更集 id（undo 模式可选，如 cs-3），缺省为最新已应用变更集"`
}

// diffFindingRow is one findingsAdded/findingsRemoved entry (the §7 finding
// shape; detail is the parsed per-check detail JSON object, absent when the
// stored finding carries none).
type diffFindingRow struct {
	CheckCode string `json:"checkCode"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Detail    any    `json:"detail,omitempty"`
}

// diffUndoOutput is ONE typed output struct carrying both mode sections
// (documented shape): the mode echo field is always present; the diff section
// (from/to/findingsAdded/findingsRemoved/connectivityLost/connectivityGained/
// scoreDelta) is set only for mode=diff, the undo section (changesetId/
// trigger/reverted/opsApplied/workspaceHash/checks) only for mode=undo — the
// unused section's fields are omitted on the wire (omitempty).
//
// The four diff list fields are POINTERS TO SLICES so both §7/§8 requirements
// hold at once: they are omitted entirely in undo mode (nil pointer +
// omitempty), while in diff mode they always render as JSON arrays — []
// rather than null when empty, the same normalization the HTTP layer applies
// (§7) so list consumers can map over them.
type diffUndoOutput struct {
	Mode string `json:"mode"`

	// diff section (mode=diff).
	From               *sim.RunRef       `json:"from,omitempty"`
	To                 *sim.RunRef       `json:"to,omitempty"`
	FindingsAdded      *[]diffFindingRow `json:"findingsAdded,omitempty"`
	FindingsRemoved    *[]diffFindingRow `json:"findingsRemoved,omitempty"`
	ConnectivityLost   *[]sim.PairRef    `json:"connectivityLost,omitempty"`
	ConnectivityGained *[]sim.PairRef    `json:"connectivityGained,omitempty"`
	ScoreDelta         *int              `json:"scoreDelta,omitempty"` // pointer: a delta of 0 is meaningful

	// undo section (mode=undo).
	ChangeSetID   string          `json:"changesetId,omitempty"`
	Trigger       string          `json:"trigger,omitempty"`
	Reverted      string          `json:"reverted,omitempty"`
	OpsApplied    int             `json:"opsApplied,omitempty"`
	WorkspaceHash string          `json:"workspaceHash,omitempty"`
	Checks        *sim.RunSummary `json:"checks,omitempty"`
}

// labDiffUndo routes to the engine diff or undo path after validating the
// mode's correlating fields.
func labDiffUndo(ctx context.Context, svc *sim.Service, in diffUndoInput) (*mcp.CallToolResult, diffUndoOutput, error) {
	switch in.Mode {
	case diffMode:
		if in.FromRunID == "" || in.ToRunID == "" {
			return nil, diffUndoOutput{}, fmt.Errorf("invalid_request: fromRunId and toRunId are required for mode=diff")
		}
		if in.ChangeSetID != "" {
			return nil, diffUndoOutput{}, fmt.Errorf("invalid_request: changesetId is not valid for mode=diff")
		}
		return labDiff(ctx, svc, in)
	case undoMode:
		if in.FromRunID != "" || in.ToRunID != "" {
			return nil, diffUndoOutput{}, fmt.Errorf("invalid_request: fromRunId and toRunId are not valid for mode=undo")
		}
		res, err := svc.Undo(ctx, in.WorkspaceID, in.ChangeSetID, true)
		if err != nil {
			return nil, diffUndoOutput{}, simToolError(err)
		}
		return nil, diffUndoOutput{
			Mode:          undoMode,
			ChangeSetID:   res.ChangeSetID,
			Trigger:       res.Trigger,
			Reverted:      res.Reverted,
			OpsApplied:    res.OpsApplied,
			WorkspaceHash: res.WorkspaceHash,
			Checks:        res.Checks,
		}, nil
	default:
		return nil, diffUndoOutput{}, fmt.Errorf("invalid_request: mode must be %q or %q, got %q", diffMode, undoMode, in.Mode)
	}
}

// labDiff projects the engine's DiffResult into the §8/§7 wire shape. sim's
// RunRef/PairRef json tags fit the contract verbatim; the finding rows are
// re-projected (domain.CheckResult has no wire tags) with the detail parsed
// into a JSON object.
func labDiff(ctx context.Context, svc *sim.Service, in diffUndoInput) (*mcp.CallToolResult, diffUndoOutput, error) {
	res, err := svc.Diff(ctx, in.WorkspaceID, in.FromRunID, in.ToRunID)
	if err != nil {
		return nil, diffUndoOutput{}, simToolError(err)
	}
	delta := res.ScoreDelta
	from, to := res.From, res.To
	added, removed := diffFindingRows(res.FindingsAdded), diffFindingRows(res.FindingsRemoved)
	lost, gained := res.ConnectivityLost, res.ConnectivityGained
	if lost == nil {
		lost = []sim.PairRef{}
	}
	if gained == nil {
		gained = []sim.PairRef{}
	}
	return nil, diffUndoOutput{
		Mode:               diffMode,
		From:               &from,
		To:                 &to,
		FindingsAdded:      &added,
		FindingsRemoved:    &removed,
		ConnectivityLost:   &lost,
		ConnectivityGained: &gained,
		ScoreDelta:         &delta,
	}, nil
}

// ---- lab_checkpoint ----

// checkpointInput is the typed input of lab_checkpoint.
type checkpointInput struct {
	WorkspaceID  string `json:"workspaceId" jsonschema:"目标工作台 id（uuid）"`
	Action       string `json:"action" jsonschema:"create、list 或 restore"`
	Label        string `json:"label,omitempty" jsonschema:"检查点标签（create 动作必填，如 baseline）"`
	CheckpointID string `json:"checkpointId,omitempty" jsonschema:"检查点 id（restore 动作必填，如 cp-1）"`
}

// checkpointItemRow is one list entry (§7 shape).
type checkpointItemRow struct {
	CheckpointID  string `json:"checkpointId"`
	Label         string `json:"label"`
	WorkspaceHash string `json:"workspaceHash"`
	CreatedAt     string `json:"createdAt"`
}

// checkpointOutput is ONE typed output struct with per-action fields omitted
// on the wire (documented shape): create → {action, checkpointId, label,
// workspaceHash, createdAt}; list → {action, items, total}; restore →
// {action, checkpointId, label, changesetId (omitted on the no-op restore),
// note, opsApplied, restoredToHash, workspaceHash, checks}.
//
// Items/Total are POINTERS so the §8 "list → {items:[…],total}" contract
// holds for the EMPTY list too (AUD1-11): nil ⇒ omitted (create/restore),
// non-nil ⇒ rendered — an empty list renders items: [] and total: 0, never
// absent fields (plain omitempty would drop both when a workspace has no
// checkpoints).
type checkpointOutput struct {
	Action string `json:"action"`

	// create / list / restore sections.
	CheckpointID  string               `json:"checkpointId,omitempty"`
	Label         string               `json:"label,omitempty"`
	WorkspaceHash string               `json:"workspaceHash,omitempty"`
	CreatedAt     string               `json:"createdAt,omitempty"`
	Items         *[]checkpointItemRow `json:"items,omitempty"`
	Total         *int                 `json:"total,omitempty"`

	// restore section.
	ChangeSetID    string          `json:"changesetId,omitempty"` // "" (no-op restore) ⇒ omitted
	Note           string          `json:"note,omitempty"`
	OpsApplied     int             `json:"opsApplied,omitempty"`
	RestoredToHash string          `json:"restoredToHash,omitempty"`
	Checks         *sim.RunSummary `json:"checks,omitempty"`
}

// labCheckpoint routes to the engine checkpoint paths after validating the
// action's correlating fields.
func labCheckpoint(ctx context.Context, svc *sim.Service, in checkpointInput) (*mcp.CallToolResult, checkpointOutput, error) {
	switch in.Action {
	case checkpointActionCreate:
		if in.Label == "" {
			return nil, checkpointOutput{}, fmt.Errorf("invalid_request: label is required for action=create")
		}
		if in.CheckpointID != "" {
			return nil, checkpointOutput{}, fmt.Errorf("invalid_request: checkpointId is not valid for action=create")
		}
		res, err := svc.CreateCheckpoint(ctx, in.WorkspaceID, in.Label)
		if err != nil {
			return nil, checkpointOutput{}, simToolError(err)
		}
		return nil, checkpointOutput{
			Action:        checkpointActionCreate,
			CheckpointID:  res.CheckpointID,
			Label:         res.Label,
			WorkspaceHash: res.WorkspaceHash,
			CreatedAt:     res.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}, nil
	case checkpointActionList:
		if in.Label != "" || in.CheckpointID != "" {
			return nil, checkpointOutput{}, fmt.Errorf("invalid_request: label and checkpointId are not valid for action=list")
		}
		cps, err := svc.ListCheckpoints(ctx, in.WorkspaceID)
		if err != nil {
			return nil, checkpointOutput{}, simToolError(err)
		}
		items := make([]checkpointItemRow, 0, len(cps))
		for _, cp := range cps {
			items = append(items, checkpointItemRow{
				CheckpointID:  cp.ID,
				Label:         cp.Label,
				WorkspaceHash: cp.WorkspaceHash,
				CreatedAt:     cp.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		// Pointer fields: an empty list still renders items: [] and
		// total: 0 (§8 — never absent).
		total := len(items)
		return nil, checkpointOutput{
			Action: checkpointActionList,
			Items:  &items,
			Total:  &total,
		}, nil
	case checkpointActionRestore:
		if in.CheckpointID == "" {
			return nil, checkpointOutput{}, fmt.Errorf("invalid_request: checkpointId is required for action=restore")
		}
		if in.Label != "" {
			return nil, checkpointOutput{}, fmt.Errorf("invalid_request: label is not valid for action=restore")
		}
		res, err := svc.RestoreCheckpoint(ctx, in.WorkspaceID, in.CheckpointID, true)
		if err != nil {
			return nil, checkpointOutput{}, simToolError(err)
		}
		return nil, checkpointOutput{
			Action:         checkpointActionRestore,
			CheckpointID:   res.CheckpointID,
			Label:          res.Label,
			ChangeSetID:    res.ChangeSetID, // "" on the no-op restore ⇒ omitted
			Note:           res.Note,
			OpsApplied:     res.OpsApplied,
			RestoredToHash: res.RestoredToHash,
			WorkspaceHash:  res.WorkspaceHash,
			Checks:         res.Checks,
		}, nil
	default:
		return nil, checkpointOutput{}, fmt.Errorf("invalid_request: action must be %q, %q or %q, got %q",
			checkpointActionCreate, checkpointActionList, checkpointActionRestore, in.Action)
	}
}

// ---- shared helpers ----

// simToolError converts an engine error into the MCP tool error carrying the
// same code and message vocabulary as the HTTP layer (§7): "<code>: <message>"
// where the message is the op_failed composition, the engine's own decode
// message (invalid_request) or the generic English §7 message of the mapped
// code. The returned error is packed into the tool result with IsError = true
// by the go-sdk.
func simToolError(err error) error {
	var opErr *sim.OpError
	switch {
	case errors.As(err, &opErr):
		msg := opErr.Error()
		if opErr.ChangeSetID != "" {
			msg = fmt.Sprintf("%s [changeset %s recorded as failed]", msg, opErr.ChangeSetID)
		}
		return fmt.Errorf("op_failed: %s", msg)
	case errors.Is(err, domain.ErrCheckpointMissing): // wraps ErrConflict — test first
		return fmt.Errorf("checkpoint_missing_file: %s", simToolMessages["checkpoint_missing_file"])
	case errors.Is(err, domain.ErrNotLatest):
		return fmt.Errorf("not_latest: %s", simToolMessages["not_latest"])
	case errors.Is(err, domain.ErrNoSourceResources):
		return fmt.Errorf("no_source_resources: %s", simToolMessages["no_source_resources"])
	case errors.Is(err, domain.ErrInvalidOp):
		return fmt.Errorf("invalid_request: %v", err)
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("not_found: %s", simToolMessages["not_found"])
	case errors.Is(err, domain.ErrConflict):
		return fmt.Errorf("conflict: %s", simToolMessages["conflict"])
	}
	return fmt.Errorf("internal: %v", err)
}

// simToolMessages mirrors the English messages of the HTTP §7 error envelope
// (api.simErrorMessages) so both boundaries answer the same vocabulary.
var simToolMessages = map[string]string{
	"not_latest":              "changeset is not the latest applied changeset of the workspace (undo is LIFO)",
	"checkpoint_missing_file": "checkpoint snapshot file is missing on disk",
	"no_source_resources":     "the selected import batches contain no device resources to build a workspace from",
	"not_found":               "requested workspace or object not found",
	"conflict":                "operation conflicts with the current state",
}

// diffFindingRows projects stored finding rows into the §7 wire shape; detail
// is parsed from the stored canonical JSON (absent when empty).
func diffFindingRows(rows []domain.CheckResult) []diffFindingRow {
	out := make([]diffFindingRow, 0, len(rows))
	for _, f := range rows {
		row := diffFindingRow{
			CheckCode: f.CheckCode,
			Severity:  f.Severity.String(),
			Message:   f.Message,
		}
		if f.DetailJSON != "" {
			var detail any
			if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err == nil {
				row.Detail = detail
			}
		}
		out = append(out, row)
	}
	return out
}
