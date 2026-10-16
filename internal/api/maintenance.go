package api

// maintenance.go — the maintenance-window HTTP surface (v0.12-R15,
// migration v24): the CRUD of planned alert-suppression windows. POST and
// DELETE are writes — they sit behind the token gate (authMiddleware) with
// every other mutating route, no special wiring needed. The ENGINE reads
// the same rows inside its sweep transaction (internal/detect); this file
// never evaluates suppression.
//
// Contract honesty:
//   - no PATCH: a window is a plan; editing one is delete + recreate (the
//     id changes, the audit story stays linear);
//   - DELETE of a window that influenced past runs does NOT rewrite those
//     runs' receipts — the history keeps what actually happened;
//   - GET is the full list (newest-started first), not paginated: the set
//     is open plans, small by nature, cleaned up as they end.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// maintenanceWindowItem is one window of the list/create responses.
type maintenanceWindowItem struct {
	WindowID    string   `json:"windowId"`
	Label       string   `json:"label"`
	StartsAt    string   `json:"startsAt"`
	EndsAt      string   `json:"endsAt"`
	ResourceIDs []string `json:"resourceIds"` // empty = the global scope
	Note        string   `json:"note"`
	CreatedAt   string   `json:"createdAt"`
}

// windowItemOf projects a domain window (nil ids → [] for the JSON shape —
// an explicit empty array, not null, so the browser never branches).
func windowItemOf(w domain.MaintenanceWindow) maintenanceWindowItem {
	ids := w.ResourceIDs
	if ids == nil {
		ids = []string{}
	}
	return maintenanceWindowItem{
		WindowID:    w.WindowID,
		Label:       w.Label,
		StartsAt:    w.StartsAt,
		EndsAt:      w.EndsAt,
		ResourceIDs: ids,
		Note:        w.Note,
		CreatedAt:   w.CreatedAt,
	}
}

// maintenanceWindowRequest is the POST /api/maintenance/windows body.
type maintenanceWindowRequest struct {
	Label       string   `json:"label"`
	StartsAt    string   `json:"startsAt"`
	EndsAt      string   `json:"endsAt"`
	ResourceIDs []string `json:"resourceIds"` // nil/empty = the global scope
	Note        string   `json:"note"`
}

// maxWindowLabelLen bounds the label (a name for humans, not a document).
const maxWindowLabelLen = 120

// maxWindowNoteLen bounds the note (the plan's one-line purpose).
const maxWindowNoteLen = 500

// handleMaintenanceWindowsList implements GET /api/maintenance/windows:
// every window, newest-started first (id ASC tie-break). No filtering —
// the client-side status chip (计划中/进行中/已结束) is computed against the
// browser clock, the server stays clock-free.
func (s *Server) handleMaintenanceWindowsList(w http.ResponseWriter, r *http.Request) {
	windows, err := s.db.Maintenance.ListWindows(r.Context())
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]maintenanceWindowItem, 0, len(windows))
	for _, win := range windows {
		items = append(items, windowItemOf(win))
	}
	respondJSON(w, http.StatusOK, struct {
		Items []maintenanceWindowItem `json:"items"`
		Total int                     `json:"total"`
	}{Items: items, Total: len(items)})
}

// handleMaintenanceWindowsCreate implements POST /api/maintenance/windows:
// validate (label, RFC3339 bounds, ends > starts, scope size), normalize
// the bounds to UTC, auto-generate mw_<uuid>, stamp created_at from the
// server clock (bookkeeping — the detection_rules precedent) and insert.
func (s *Server) handleMaintenanceWindowsCreate(w http.ResponseWriter, r *http.Request) {
	var req maintenanceWindowRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	if req.Label == "" {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "label 必填（维护事由，如「哈大齐干线割接」）")
		return
	}
	if len([]rune(req.Label)) > maxWindowLabelLen {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "label 长度超过 %d 字符", maxWindowLabelLen)
		return
	}
	if len([]rune(req.Note)) > maxWindowNoteLen {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "note 长度超过 %d 字符", maxWindowNoteLen)
		return
	}
	start, end, err := domain.ParseMaintenanceWindowTimes(req.StartsAt, req.EndsAt)
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "%s", err.Error())
		return
	}
	if len(req.ResourceIDs) > domain.MaxMaintenanceScopeResources {
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"resourceIds 显式范围最多 %d 个（更大范围请用全局 scope）", domain.MaxMaintenanceScopeResources)
		return
	}
	// Scope ids must reference existing resources — a typo'd id would
	// silently never match anything, and the operator would believe a
	// resource was covered when it was not. Honest failure beats a
	// silent no-op window.
	for _, id := range req.ResourceIDs {
		if _, err := s.db.Resources.GetByID(r.Context(), id); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				respondErrorf(w, http.StatusBadRequest, "bad_request", "resourceIds 含不存在的资源 %q", id)
				return
			}
			s.respondErrorFor(w, r, err, "")
			return
		}
	}
	win := domain.MaintenanceWindow{
		WindowID:    domain.NewID("mw"),
		Label:       req.Label,
		StartsAt:    start.UTC().Format(time.RFC3339),
		EndsAt:      end.UTC().Format(time.RFC3339),
		ResourceIDs: domain.NormalizeMaintenanceScope(req.ResourceIDs),
		Note:        req.Note,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.db.Maintenance.InsertWindow(r.Context(), win); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusCreated, windowItemOf(win))
}

// handleMaintenanceWindowsDelete implements DELETE
// /api/maintenance/windows/{id}: 404 for an unknown id (the honest miss),
// 200 {deleted: true} otherwise. Past runs keep their receipts.
func (s *Server) handleMaintenanceWindowsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.db.Maintenance.DeleteWindow(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, struct {
		WindowID string `json:"windowId"`
		Deleted  bool   `json:"deleted"`
	}{WindowID: id, Deleted: true})
}
