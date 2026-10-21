// Workspace (simulation workbench) routes of PROJECT_BRIEF_P2 §7. Every
// handler follows the Phase 1 conventions: context from the request, JSON
// responses via respondJSON, failures through the uniform error envelope
// {"error":{"code","message"}} — with English messages per §7 ("English
// messages"), unlike the Phase 1 routes whose envelope messages are Chinese.
//
// Error-code mapping (§6/§7, engine sentinels of internal/domain):
//
//      bad JSON / empty required field / malformed op  → 400 invalid_request
//      unknown workspace / run / changeset / batch     → 404 not_found
//      duplicate workspace name                        → 409 conflict
//      undo of a non-latest changeset                  → 409 not_latest
//      already-reverted / failed undo target           → 409 conflict
//      missing checkpoint snapshot file                → 409 checkpoint_missing_file
//      selection without device resources              → 422 no_source_resources
//      semantic op failure (rolled back, recorded)     → 422 op_failed
//
// Pagination follows the Phase 1 clamp (page ≥ 1, pageSize default 20,
// max 100) through parsePagination.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/sim"
)

// simErrorMessages maps the §7 error codes to their English messages. The
// two dynamic codes get meaningful fallbacks; respondSimError overrides
// them with the engine's own message (invalid_request) or the composed
// op_failed text so the envelope never ships an empty message (AUD2-3).
var simErrorMessages = map[string]string{
	"not_latest":              "changeset is not the latest applied changeset of the workspace (undo is LIFO)",
	"checkpoint_missing_file": "checkpoint snapshot file is missing on disk",
	"no_source_resources":     "the selected import batches contain no device resources to build a workspace from",
	"not_found":               "requested workspace or object not found",
	"conflict":                "operation conflicts with the current state",
	"internal":                "internal server error, see server log",
	"invalid_request":         "invalid request",
	"op_failed":               "operation failed",
}

// respondSimError maps a sim-engine error to (status, §7 code, English
// message) and writes the envelope. 500-class failures are logged with the
// error and stack, exactly like respondErrorFor. invalid_request and
// op_failed carry the engine's own (dynamic) message — the static map only
// provides the fallback — so apply-time validation 400s and semantic 422s
// never answer an empty message (AUD2-3). not_found messages carry the
// server's database binding (issue #8): a workspace that exists in the
// CLI's database but 404s here is a cross-database mismatch, and the hint
// turns "连错库 or 数据丢失？" into a one-look answer.
func (s *Server) respondSimError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := mapSimError(err)
	if status >= 500 && s.log != nil {
		s.log.Error("request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err.Error(),
			"stack", string(debug.Stack()),
		)
	}
	message := simErrorMessages[code]
	switch code {
	case "invalid_request":
		message = "invalid request: " + err.Error()
	case "op_failed":
		var opErr *sim.OpError
		if errors.As(err, &opErr) {
			message = opFailedMessage(opErr)
		} else {
			// Every op_failed maps through *OpError; keep a meaningful
			// fallback should a new path surface.
			message = "operation failed: " + err.Error()
		}
	case "not_found":
		message = fmt.Sprintf("%s; this server is bound to database %s — a cross-database mismatch (serve --db vs CLI --db) is the usual cause", message, s.absDBPath())
	}
	respondError(w, status, code, message)
}

// absDBPath is the absolute path of the bound database ("" when unknown).
func (s *Server) absDBPath() string {
	if s.db == nil {
		return ""
	}
	p := s.db.Path()
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return p
}

// mapSimError converts a sim/repo error into the §7 (status, code) pair.
// The op_failed message is composed from the failing *sim.OpError (index,
// code, reason and the recorded failed-changeset id) to match the pinned
// format "op 2 (set_link_status): link not found: ln-99 [changeset cs-12
// recorded as failed]".
func mapSimError(err error) (status int, code string) {
	var opErr *sim.OpError
	switch {
	case errors.As(err, &opErr):
		return http.StatusUnprocessableEntity, "op_failed"
	case errors.Is(err, domain.ErrCheckpointMissing): // wraps ErrConflict — test first
		return http.StatusConflict, "checkpoint_missing_file"
	case errors.Is(err, domain.ErrNotLatest):
		return http.StatusConflict, "not_latest"
	case errors.Is(err, domain.ErrNoSourceResources):
		return http.StatusUnprocessableEntity, "no_source_resources"
	case errors.Is(err, domain.ErrInvalidOp):
		return http.StatusBadRequest, "invalid_request"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	}
	return http.StatusInternalServerError, "internal"
}

// opFailedMessage renders the §7 op_failed message from an OpError: the
// engine's "op <i> (<code>): <reason>" plus the failed-changeset suffix when
// the record transaction completed (sim.OpError.ChangeSetID).
func opFailedMessage(opErr *sim.OpError) string {
	msg := opErr.Error()
	if opErr.ChangeSetID != "" {
		msg += fmt.Sprintf(" [changeset %s recorded as failed]", opErr.ChangeSetID)
	}
	return msg
}

// readJSONBody reads the request body (bounded to maxUploadBytes; ops
// payloads are far smaller) so error mapping can happen before any byte of
// the response is written.
func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes+1))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "cannot read request body: "+err.Error())
		return nil, false
	}
	if len(body) > maxUploadBytes {
		respondError(w, http.StatusRequestEntityTooLarge, "too_large", "request body exceeds the 10MB limit")
		return nil, false
	}
	return body, true
}

// ---- GET /api/workspaces · POST /api/workspaces ----

// workspaceListItem is one entry of GET /api/workspaces (§7).
type workspaceListItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	DeviceCount    int    `json:"deviceCount"`
	InterfaceCount int    `json:"interfaceCount"`
	LinkCount      int    `json:"linkCount"`
	UpDeviceCount  int    `json:"upDeviceCount"`
	// #51 图层覆盖层计数（工作台列表/详情摘要用）。
	LayerNodeCount  int       `json:"layerNodeCount"`
	LayerLinkCount  int       `json:"layerLinkCount"`
	ChangeSetCount  int       `json:"changesetCount"`
	CheckpointCount int       `json:"checkpointCount"`
	LastRunID       *string   `json:"lastRunId"`
	LastRunScore    *int      `json:"lastRunScore"`
	CreatedAt       time.Time `json:"createdAt"`
}

// workspacesPage is the list envelope of GET /api/workspaces.
type workspacesPage struct {
	Items    []workspaceListItem `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

// layerMembersPage is the envelope of the cluster drill-down (issue #51).
type layerMembersPage struct {
	Items    []sim.LayerMember `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Cluster  string            `json:"cluster"`
}

// handleWorkspacesList implements GET /api/workspaces?page=&pageSize=
// (items ordered by created_at desc — the repository list order).
func (s *Server) handleWorkspacesList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	workspaces, total, err := s.db.Workspaces.List(r.Context(), page, pageSize)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	// #102: the per-row assembly (7 count queries + Latest per row — 8
	// point queries per workspace, 800 for one page) is now TWO batched
	// calls whose cost is independent of the page size.
	ids := make([]string, len(workspaces))
	for i, ws := range workspaces {
		ids[i] = ws.ID
	}
	summaries, err := s.db.Workspaces.CountSummariesOfWorkspaces(r.Context(), ids)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	latestRuns, err := s.db.CheckRuns.LatestByWorkspaces(r.Context(), ids)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	items := make([]workspaceListItem, 0, len(workspaces))
	for _, ws := range workspaces {
		items = append(items, workspaceListItemOf(ws, summaries[ws.ID], latestRuns[ws.ID]))
	}
	respondJSON(w, http.StatusOK, workspacesPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// workspaceListItemOf assembles one list item from the workspace row, its
// batched count summary and its latest run (absent = zero counts / no run —
// the exact semantics the per-row queries had: CountByWorkspace of an
// empty workspace answers empty maps, Latest answers ErrNotFound which was
// treated as "no run yet").
func workspaceListItemOf(ws domain.Workspace, sum repo.WorkspaceCountSummary, latest domain.CheckRun) workspaceListItem {
	item := workspaceListItem{
		ID:              ws.ID,
		Name:            ws.Name,
		Description:     ws.Description,
		DeviceCount:     sum.DeviceStatus[domain.EntityStatusUp] + sum.DeviceStatus[domain.EntityStatusDown],
		UpDeviceCount:   sum.DeviceStatus[domain.EntityStatusUp],
		InterfaceCount:  sum.InterfaceStatus[domain.EntityStatusUp] + sum.InterfaceStatus[domain.EntityStatusDown],
		LinkCount:       sum.LinkStatus[domain.EntityStatusUp] + sum.LinkStatus[domain.EntityStatusDown],
		LayerNodeCount:  sum.LayerNodes,
		LayerLinkCount:  sum.LayerLinks,
		ChangeSetCount:  sum.ChangeSets,
		CheckpointCount: sum.Checkpoints,
		CreatedAt:       ws.CreatedAt,
	}
	if latest.ID != "" {
		id, score := latest.ID, latest.Score
		item.LastRunID, item.LastRunScore = &id, &score
	}
	return item
}

// createWorkspaceBody is the request of POST /api/workspaces (§6/§7).
type createWorkspaceBody struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	SourceBatchIDs []string `json:"sourceBatchIds"`
}

// handleWorkspacesCreate implements POST /api/workspaces: materialize a
// workspace from the selected import batches (default: every completed
// batch) → 201 MaterializeResult.
func (s *Server) handleWorkspacesCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req createWorkspaceBody
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object: "+err.Error())
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "name must not be empty")
		return
	}
	res, err := s.sim.Materialize(r.Context(), req.Name, req.Description, req.SourceBatchIDs)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, res)
}

// ---- GET /api/workspaces/{id} · DELETE /api/workspaces/{id} ----

// workspaceDetailCounts is the counts block of GET /api/workspaces/{id} (§7).
type workspaceDetailCounts struct {
	Devices      int `json:"devices"`
	UpDevices    int `json:"upDevices"`
	DownDevices  int `json:"downDevices"`
	Interfaces   int `json:"interfaces"`
	UpInterfaces int `json:"upInterfaces"`
	Links        int `json:"links"`
	UpLinks      int `json:"upLinks"`
	ChangeSets   int `json:"changesets"`
	Checkpoints  int `json:"checkpoints"`
	// #51 图层覆盖层（只读投影，不计入状态哈希）。
	LayerNodes int `json:"layerNodes"`
	LayerLinks int `json:"layerLinks"`
}

// workspaceLatestChangeSet is the latestChangeset block of the detail view
// (null before the first changeset).
type workspaceLatestChangeSet struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Trigger     string    `json:"trigger"`
	Status      string    `json:"status"`
	OpsApplied  int       `json:"opsApplied"`
	CreatedAt   time.Time `json:"createdAt"`
}

// workspaceLatestRun is the latestRun block of the detail view (null before
// the first check run).
type workspaceLatestRun struct {
	RunID  string        `json:"runId"`
	Score  int           `json:"score"`
	Counts sim.SevCounts `json:"counts"`
}

// workspaceDetail is the response of GET /api/workspaces/{id} (§7).
type workspaceDetail struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	Description     string                    `json:"description"`
	Status          string                    `json:"status"`
	SourceBatchIDs  []string                  `json:"sourceBatchIds"`
	Counts          workspaceDetailCounts     `json:"counts"`
	WorkspaceHash   string                    `json:"workspaceHash"`
	LatestChangeSet *workspaceLatestChangeSet `json:"latestChangeset"`
	LatestRun       *workspaceLatestRun       `json:"latestRun"`
	CreatedAt       time.Time                 `json:"createdAt"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
}

// handleWorkspaceDetail implements GET /api/workspaces/{id}: the full
// workspace snapshot with up/down splits, the canonical state hash, the
// latest applied changeset and the latest check run (both null-able).
func (s *Server) handleWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ws, err := s.db.Workspaces.GetByID(ctx, r.PathValue("id"))
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	devCounts, err := s.db.VirtualDevices.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	ifaceCounts, err := s.db.SimInterfaces.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	linkCounts, err := s.db.SimLinks.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	csCount, err := s.db.ChangeSets.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	cpCount, err := s.db.Checkpoints.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	layerNodeCount, err := s.db.LayerNodes.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	layerLinkCount, err := s.db.LayerLinks.CountByWorkspace(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	hash, err := s.sim.WorkspaceHash(ctx, ws.ID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}

	detail := workspaceDetail{
		ID:          ws.ID,
		Name:        ws.Name,
		Description: ws.Description,
		Status:      ws.Status.String(),
		Counts: workspaceDetailCounts{
			Devices:      devCounts[domain.EntityStatusUp] + devCounts[domain.EntityStatusDown],
			UpDevices:    devCounts[domain.EntityStatusUp],
			DownDevices:  devCounts[domain.EntityStatusDown],
			Interfaces:   ifaceCounts[domain.EntityStatusUp] + ifaceCounts[domain.EntityStatusDown],
			UpInterfaces: ifaceCounts[domain.EntityStatusUp],
			Links:        linkCounts[domain.EntityStatusUp] + linkCounts[domain.EntityStatusDown],
			UpLinks:      linkCounts[domain.EntityStatusUp],
			ChangeSets:   csCount,
			Checkpoints:  cpCount,
			LayerNodes:   layerNodeCount,
			LayerLinks:   layerLinkCount,
		},
		WorkspaceHash: hash,
		CreatedAt:     ws.CreatedAt,
		UpdatedAt:     ws.UpdatedAt,
	}
	if src, err := ws.Source(); err == nil && len(src.BatchIDs) > 0 {
		detail.SourceBatchIDs = src.BatchIDs
	} else if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	if cs, err := s.db.ChangeSets.LatestApplied(ctx, ws.ID); err == nil {
		detail.LatestChangeSet = &workspaceLatestChangeSet{
			ID:          cs.ID,
			Description: cs.Description,
			Trigger:     cs.Trigger.String(),
			Status:      cs.Status.String(),
			OpsApplied:  cs.OpsApplied,
			CreatedAt:   cs.CreatedAt,
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		s.respondSimError(w, r, err)
		return
	}
	if run, err := s.db.CheckRuns.Latest(ctx, ws.ID); err == nil {
		results, rerr := s.db.CheckRuns.GetResults(ctx, run.ID)
		if rerr != nil {
			s.respondSimError(w, r, rerr)
			return
		}
		detail.LatestRun = &workspaceLatestRun{
			RunID:  run.ID,
			Score:  run.Score,
			Counts: sevCountsOfResults(results),
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, detail)
}

// workspaceRemovedCounts is the removed block of the delete response: the
// per-table purge counts of the repo cascade, rendered with the §7 field
// names (repo keys virtual_devices/check_runs → devices/checkRuns).
type workspaceRemovedCounts struct {
	Devices     int64 `json:"devices"`
	Interfaces  int64 `json:"interfaces"`
	Links       int64 `json:"links"`
	ChangeSets  int64 `json:"changesets"`
	CheckRuns   int64 `json:"checkRuns"`
	Checkpoints int64 `json:"checkpoints"`
}

// workspaceDeleteResponse is the body of DELETE /api/workspaces/{id}.
type workspaceDeleteResponse struct {
	Deleted bool                   `json:"deleted"`
	Removed workspaceRemovedCounts `json:"removed"`
}

// handleWorkspaceDelete implements DELETE /api/workspaces/{id}: the
// transactional cascade purge plus the best-effort snapshot-directory
// removal of the engine.
func (s *Server) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request) {
	counts, err := s.sim.DeleteWorkspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, workspaceDeleteResponse{
		Deleted: true,
		Removed: workspaceRemovedCounts{
			Devices:     counts["virtual_devices"],
			Interfaces:  counts["interfaces"],
			Links:       counts["links"],
			ChangeSets:  counts["changesets"],
			CheckRuns:   counts["check_runs"],
			Checkpoints: counts["checkpoints"],
		},
	})
}

// ---- topology · path ----

// handleWorkspaceTopology implements GET /api/workspaces/{id}/topology:
// the sim.Topology payload (nodes sorted by code with name-sorted
// interfaces, edges by numeric id) — the §7 field names are pinned by the
// engine's json tags.
func (s *Server) handleWorkspaceTopology(w http.ResponseWriter, r *http.Request) {
	topo, err := s.sim.Topology(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, topo)
}

// pathBody is the request of POST /api/workspaces/{id}/topology/path.
type pathBody struct {
	FromDevice string `json:"fromDevice"`
	ToDevice   string `json:"toDevice"`
}

// handleWorkspacePath implements POST /api/workspaces/{id}/topology/path
// (device codes; identical codes are reachable with pathLength 0, unknown
// codes answer 404).
func (s *Server) handleWorkspacePath(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req pathBody
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object: "+err.Error())
		return
	}
	if req.FromDevice == "" || req.ToDevice == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "fromDevice and toDevice are required")
		return
	}
	result, err := s.sim.Path(r.Context(), r.PathValue("id"), req.FromDevice, req.ToDevice)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// ---- POST /api/workspaces/{id}/ops ----

// opsRequestBody is the §7 ops request: {ops:[…], description?, runChecks?
// (default true)}. The ops array itself is decoded by sim.DecodeOps (strict
// per-op decoding; restore_* ops are engine-internal and rejected here).
type opsRequestBody struct {
	Description string          `json:"description"`
	RunChecks   *bool           `json:"runChecks"`
	Ops         json.RawMessage `json:"ops"`
}

// handleWorkspaceOps implements POST /api/workspaces/{id}/ops: decode the
// raw ops array (malformed/unknown/restore op ⇒ 400 invalid_request), apply
// as one changeset (trigger "user") and answer the ApplyResult. A semantic
// failure rolls back atomically and answers 422 op_failed with the pinned
// message format; the state is untouched either way.
func (s *Server) handleWorkspaceOps(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req opsRequestBody
	if err := json.Unmarshal(raw, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object: "+err.Error())
		return
	}
	ops, err := sim.DecodeOps(raw)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	runChecks := true
	if req.RunChecks != nil {
		runChecks = *req.RunChecks
	}
	res, err := s.sim.ApplyOps(r.Context(), r.PathValue("id"), ops, req.Description, runChecks)
	if err != nil {
		var opErr *sim.OpError
		if errors.As(err, &opErr) {
			respondError(w, http.StatusUnprocessableEntity, "op_failed", opFailedMessage(opErr))
			return
		}
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// ---- checks: run · list · get · diff ----

// handleWorkspaceChecksRun implements POST /api/workspaces/{id}/checks/run:
// a manual check run (trigger "manual") over the current state.
func (s *Server) handleWorkspaceChecksRun(w http.ResponseWriter, r *http.Request) {
	res, err := s.sim.RunChecks(r.Context(), r.PathValue("id"), domain.CheckTriggerManual)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// checkRunItem is one entry of GET /api/workspaces/{id}/checks (§7): the
// run's finding counts are computed from its stored result rows.
type checkRunItem struct {
	RunID       string        `json:"runId"`
	ChangeSetID *string       `json:"changesetId"` // null when the run is not tied to a changeset
	Trigger     string        `json:"trigger"`
	StartedAt   time.Time     `json:"startedAt"`
	Counts      sim.SevCounts `json:"counts"`
	Score       int           `json:"score"`
}

// checksPage is the list envelope of GET /api/workspaces/{id}/checks.
type checksPage struct {
	Items    []checkRunItem `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

// handleWorkspaceChecksList implements GET /api/workspaces/{id}/checks
// (desc by run id — the repository numeric-id order).
func (s *Server) handleWorkspaceChecksList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	runs, total, err := s.sim.ListRuns(ctx, r.PathValue("id"), page, pageSize)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	// #102: the per-run GetResults loaded every result row of every run
	// just to count severities — one GROUP BY over the page's run ids
	// answers the same counts (passed rows never counted, so the
	// passed = 0 filter reproduces sevCountsOfResults exactly).
	runIDs := make([]string, len(runs))
	for i, run := range runs {
		runIDs[i] = run.ID
	}
	sevCounts, err := s.db.CheckRuns.SeverityCountsByRuns(ctx, runIDs)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	items := make([]checkRunItem, 0, len(runs))
	for _, run := range runs {
		item := checkRunItem{
			RunID:     run.ID,
			Trigger:   run.Trigger.String(),
			StartedAt: run.StartedAt,
			Score:     run.Score,
		}
		if run.ChangesetID != "" {
			id := run.ChangesetID
			item.ChangeSetID = &id
		}
		item.Counts = sim.SevCounts{
			Error:   sevCounts[run.ID][domain.SeverityError],
			Warning: sevCounts[run.ID][domain.SeverityWarning],
			Info:    sevCounts[run.ID][domain.SeverityInfo],
		}
		items = append(items, item)
	}
	respondJSON(w, http.StatusOK, checksPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// checkResultItem is one row of the run detail's results (§7): detail is the
// parsed per-check detail JSON object, not a string.
type checkResultItem struct {
	Seq        int             `json:"seq"`
	CheckCode  string          `json:"checkCode"`
	Severity   string          `json:"severity"`
	Passed     bool            `json:"passed"`
	EntityKind string          `json:"entityKind"`
	EntityID   string          `json:"entityId"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail"`
}

// checkRunDetail is the response of GET /api/workspaces/{id}/checks/{runId}
// (§7): the run with its full result rows in stored order — findings first
// (canonical check order), then the five passed baselines.
type checkRunDetail struct {
	RunID       string            `json:"runId"`
	WorkspaceID string            `json:"workspaceId"`
	ChangeSetID *string           `json:"changesetId"`
	Trigger     string            `json:"trigger"`
	RuleVersion string            `json:"ruleVersion"`
	StartedAt   time.Time         `json:"startedAt"`
	DurationMs  int               `json:"durationMs"`
	Score       int               `json:"score"`
	Counts      sim.SevCounts     `json:"counts"`
	Results     []checkResultItem `json:"results"`
}

// handleWorkspaceCheckRun implements GET /api/workspaces/{id}/checks/{runId}
// (a run of another workspace answers 404 like an unknown run).
func (s *Server) handleWorkspaceCheckRun(w http.ResponseWriter, r *http.Request) {
	run, results, err := s.sim.GetRun(r.Context(), r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	detail := checkRunDetail{
		RunID:       run.ID,
		WorkspaceID: run.WorkspaceID,
		Trigger:     run.Trigger.String(),
		RuleVersion: run.RuleVersion,
		StartedAt:   run.StartedAt,
		DurationMs:  run.DurationMs,
		Score:       run.Score,
		Counts:      sevCountsOfResults(results),
		Results:     make([]checkResultItem, 0, len(results)),
	}
	if run.ChangesetID != "" {
		id := run.ChangesetID
		detail.ChangeSetID = &id
	}
	for _, res := range results {
		detail.Results = append(detail.Results, checkResultItem{
			Seq:        res.Seq,
			CheckCode:  res.CheckCode,
			Severity:   res.Severity.String(),
			Passed:     res.Passed,
			EntityKind: res.EntityKind,
			EntityID:   res.EntityID,
			Message:    res.Message,
			Detail:     json.RawMessage(res.DetailJSON),
		})
	}
	respondJSON(w, http.StatusOK, detail)
}

// diffFinding is one findingsAdded/Removed entry of the diff (§7 shape).
type diffFinding struct {
	CheckCode string          `json:"checkCode"`
	Severity  string          `json:"severity"`
	Message   string          `json:"message"`
	Detail    json.RawMessage `json:"detail"`
}

// diffResponse is the §7 diff payload: from/to refs, matched-key finding
// deltas, connectivity pair deltas and the score delta. The from/to refs and
// pair entries reuse the engine's json-tagged types.
type diffResponse struct {
	From               sim.RunRef    `json:"from"`
	To                 sim.RunRef    `json:"to"`
	FindingsAdded      []diffFinding `json:"findingsAdded"`
	FindingsRemoved    []diffFinding `json:"findingsRemoved"`
	ConnectivityLost   []sim.PairRef `json:"connectivityLost"`
	ConnectivityGained []sim.PairRef `json:"connectivityGained"`
	ScoreDelta         int           `json:"scoreDelta"`
}

// handleWorkspaceRunDiff implements GET
// /api/workspaces/{id}/checks/{runId}/diff?compareTo=<runId2>. Missing
// compareTo ⇒ 400; the same run twice ⇒ 400 (PROJECT_BRIEF_P2 §7 — the
// engine signals domain.ErrConflict); unknown or cross-workspace runs ⇒ 404.
func (s *Server) handleWorkspaceRunDiff(w http.ResponseWriter, r *http.Request) {
	compareTo := r.URL.Query().Get("compareTo")
	if compareTo == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "compareTo query parameter is required")
		return
	}
	diff, err := s.sim.Diff(r.Context(), r.PathValue("id"), r.PathValue("runId"), compareTo)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// The only conflict Diff reports: comparing a run with itself.
			respondError(w, http.StatusBadRequest, "invalid_request",
				"cannot diff a run against itself; pass a different compareTo run")
			return
		}
		s.respondSimError(w, r, err)
		return
	}
	// The engine leaves empty pair slices nil; the §7 shape renders them as
	// [] (never null) so list consumers can map over them.
	lost := diff.ConnectivityLost
	if lost == nil {
		lost = []sim.PairRef{}
	}
	gained := diff.ConnectivityGained
	if gained == nil {
		gained = []sim.PairRef{}
	}
	respondJSON(w, http.StatusOK, diffResponse{
		From:               diff.From,
		To:                 diff.To,
		FindingsAdded:      diffFindings(diff.FindingsAdded),
		FindingsRemoved:    diffFindings(diff.FindingsRemoved),
		ConnectivityLost:   lost,
		ConnectivityGained: gained,
		ScoreDelta:         diff.ScoreDelta,
	})
}

// diffFindings projects stored check-result rows to the §7 finding shape.
func diffFindings(rows []domain.CheckResult) []diffFinding {
	out := make([]diffFinding, 0, len(rows))
	for _, f := range rows {
		out = append(out, diffFinding{
			CheckCode: f.CheckCode,
			Severity:  f.Severity.String(),
			Message:   f.Message,
			Detail:    json.RawMessage(f.DetailJSON),
		})
	}
	return out
}

// sevCountsOfResults counts the findings (passed=0) of a result row set per
// severity — the {error,warning,info} block of run summaries.
func sevCountsOfResults(results []domain.CheckResult) sim.SevCounts {
	var counts sim.SevCounts
	for _, res := range results {
		if res.Passed {
			continue
		}
		switch res.Severity {
		case domain.SeverityError:
			counts.Error++
		case domain.SeverityWarning:
			counts.Warning++
		case domain.SeverityInfo:
			counts.Info++
		}
	}
	return counts
}

// ---- GET /api/workspaces/{id}/changesets ----

// changeSetItem is one entry of GET /api/workspaces/{id}/changesets (§7);
// revertedAt is null while the changeset was not reverted.
type changeSetItem struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Trigger     string     `json:"trigger"`
	Status      string     `json:"status"`
	OpsApplied  int        `json:"opsApplied"`
	CreatedAt   time.Time  `json:"createdAt"`
	RevertedAt  *time.Time `json:"revertedAt"`
}

// changeSetsPage is the list envelope of GET /api/workspaces/{id}/changesets.
type changeSetsPage struct {
	Items    []changeSetItem `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

// handleWorkspaceChangeSets implements GET /api/workspaces/{id}/changesets
// (desc by numeric changeset id — the repository order).
func (s *Server) handleWorkspaceChangeSets(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	workspaceID := r.PathValue("id")
	if _, err := s.db.Workspaces.GetByID(ctx, workspaceID); err != nil {
		s.respondSimError(w, r, err)
		return
	}
	css, total, err := s.db.ChangeSets.ListByWorkspace(ctx, workspaceID, page, pageSize)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	items := make([]changeSetItem, 0, len(css))
	for _, cs := range css {
		item := changeSetItem{
			ID:          cs.ID,
			Description: cs.Description,
			Trigger:     cs.Trigger.String(),
			Status:      cs.Status.String(),
			OpsApplied:  cs.OpsApplied,
			CreatedAt:   cs.CreatedAt,
		}
		if cs.Reverted() {
			at := cs.RevertedAt
			item.RevertedAt = &at
		}
		items = append(items, item)
	}
	respondJSON(w, http.StatusOK, changeSetsPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// ---- POST /api/workspaces/{id}/undo ----

// undoBody is the §7 undo request; an empty changesetId targets the latest
// applied changeset of the workspace.
type undoBody struct {
	ChangeSetID string `json:"changesetId"`
}

// handleWorkspaceUndo implements POST /api/workspaces/{id}/undo: apply the
// target's recorded inverses as a new changeset (trigger "undo") with the
// auto-checks, then mark the target reverted. not-latest ⇒ 409 not_latest;
// already-reverted/failed target ⇒ 409 conflict; unknown ⇒ 404.
func (s *Server) handleWorkspaceUndo(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req undoBody
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			respondError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object: "+err.Error())
			return
		}
	}
	res, err := s.sim.Undo(r.Context(), r.PathValue("id"), req.ChangeSetID, true)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// ---- checkpoints: create · list · restore ----

// checkpointBody is the request of POST /api/workspaces/{id}/checkpoints.
type checkpointBody struct {
	Label string `json:"label"`
}

// handleCheckpointCreate implements POST /api/workspaces/{id}/checkpoints:
// an Online-Backup snapshot of the whole database plus the marker row → 201.
func (s *Server) handleCheckpointCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var req checkpointBody
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object: "+err.Error())
		return
	}
	if req.Label == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "label must not be empty")
		return
	}
	res, err := s.sim.CreateCheckpoint(r.Context(), r.PathValue("id"), req.Label)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, res)
}

// checkpointItem is one entry of GET /api/workspaces/{id}/checkpoints (§7).
type checkpointItem struct {
	CheckpointID  string    `json:"checkpointId"`
	Label         string    `json:"label"`
	WorkspaceHash string    `json:"workspaceHash"`
	CreatedAt     time.Time `json:"createdAt"`
}

// checkpointsResponse is the list response of GET
// /api/workspaces/{id}/checkpoints (desc, no pagination per §7).
type checkpointsResponse struct {
	Items []checkpointItem `json:"items"`
	Total int              `json:"total"`
}

// handleCheckpointsList implements GET /api/workspaces/{id}/checkpoints.
func (s *Server) handleCheckpointsList(w http.ResponseWriter, r *http.Request) {
	cps, err := s.sim.ListCheckpoints(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	items := make([]checkpointItem, 0, len(cps))
	for _, cp := range cps {
		items = append(items, checkpointItem{
			CheckpointID:  cp.ID,
			Label:         cp.Label,
			WorkspaceHash: cp.WorkspaceHash,
			CreatedAt:     cp.CreatedAt,
		})
	}
	respondJSON(w, http.StatusOK, checkpointsResponse{Items: items, Total: len(items)})
}

// checkpointRestoreResponse is the §7 restore payload. The engine's no-op
// case (already at the checkpoint state) reports changesetId "" — rendered
// here as null with opsApplied 0 and the note, per §7/§4.
type checkpointRestoreResponse struct {
	CheckpointID   string          `json:"checkpointId"`
	Label          string          `json:"label"`
	ChangeSetID    *string         `json:"changesetId"`
	Note           string          `json:"note,omitempty"`
	OpsApplied     int             `json:"opsApplied"`
	RestoredToHash string          `json:"restoredToHash"`
	WorkspaceHash  string          `json:"workspaceHash"`
	Checks         *sim.RunSummary `json:"checks"`
}

// handleCheckpointRestore implements POST
// /api/workspaces/{id}/checkpoints/{cpId}/restore: transform the current
// state into the snapshot state as one forward-op changeset. A missing
// snapshot file answers 409 checkpoint_missing_file.
func (s *Server) handleCheckpointRestore(w http.ResponseWriter, r *http.Request) {
	res, err := s.sim.RestoreCheckpoint(r.Context(), r.PathValue("id"), r.PathValue("cpId"), true)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	resp := checkpointRestoreResponse{
		CheckpointID:   res.CheckpointID,
		Label:          res.Label,
		Note:           res.Note,
		OpsApplied:     res.OpsApplied,
		RestoredToHash: res.RestoredToHash,
		WorkspaceHash:  res.WorkspaceHash,
		Checks:         res.Checks,
	}
	if res.ChangeSetID != "" {
		id := res.ChangeSetID
		resp.ChangeSetID = &id
	}
	respondJSON(w, http.StatusOK, resp)
}

// ---- GET /api/lab/report ----

// handleLabReport implements GET /api/lab/report?workspaceId=&format=html|json
// (default html): the standalone HTML lab report (text/html; charset=utf-8)
// or the JSON LabReport structure. Unknown workspace ⇒ 404, a bad format ⇒
// 400 (both decided before any byte is written).
func (s *Server) handleLabReport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}
	if format != "html" && format != "json" {
		respondErrorf(w, http.StatusBadRequest, "invalid_request",
			"format %q must be html or json", format)
		return
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "workspaceId query parameter is required")
		return
	}
	if format == "html" {
		html, err := s.lab.RenderHTML(r.Context(), workspaceID)
		if err != nil {
			s.respondSimError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
		return
	}
	rep, err := s.lab.JSON(r.Context(), workspaceID)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, rep)
}

// handleWorkspaceLayerMembers implements GET
// /api/workspaces/{id}/layer-members?cluster=&page=&pageSize= (issue #51):
// the paged per-station drill-down behind a canvas cluster node. cluster
// is the anchor virtual device id (the aggregate node's anchorDeviceId).
func (s *Server) handleWorkspaceLayerMembers(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	cluster := r.URL.Query().Get("cluster")
	if cluster == "" {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "cluster 查询参数必填（聚合节点的 anchorDeviceId）")
		return
	}
	members, total, err := s.sim.LayerMembers(r.Context(), r.PathValue("id"), cluster, page, pageSize)
	if err != nil {
		s.respondSimError(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, layerMembersPage{
		Items: members, Total: total, Page: page, PageSize: pageSize, Cluster: cluster,
	})
}
