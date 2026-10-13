package api

// status.go — the W22 run-state HTTP surface (design data-model-v1.md
// §12-B1 方案 C, issue #22): the manual observation channel of the
// operational state and its history. The ledger status (resources.status,
// the import channel) is untouched — this file is the ONLY writer of
// resource_status / resources.run_state next to the (W23, reserved) alert
// engine.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// runStateRequest is the body of POST /api/resources/{id}/run-state.
//
// runState is the closed enum up/down/degraded/maintenance/unknown. source
// defaults to "manual" and, in W22, MUST be "manual": 'alert' is reserved
// for the W23 detection engine (the run-state write of ADR-0011 D2's
// ③ 告警触发以 source='alert' 写 run_state) and 'poll' for a future
// scheduled probe writer — both answer 400 here.
//
// ts is optional RFC3339; when absent the SERVER CLOCK stamps the
// observation. That is a deliberate, documented boundary: a manual
// run-state write is a human bookkeeping action, not a data-path import —
// unlike the metric channel (D2: ts 必须来自数据), determinism of the DATA
// pipeline is not at stake; callers who need reproducible history pass ts
// explicitly (tests do).
type runStateRequest struct {
	RunState string  `json:"runState"`
	Source   string  `json:"source"`
	TS       *string `json:"ts"`
}

// runStateSnapshot is the POST response: the updated resources snapshot.
type runStateSnapshot struct {
	RunState   string `json:"runState"`
	RunStateAt string `json:"runStateAt"`
	Source     string `json:"source"`
}

// runStateHistoryItem mirrors one resource_status history row.
type runStateHistoryItem struct {
	TS       string `json:"ts"`
	RunState string `json:"runState"`
	Source   string `json:"source"`
}

// handleRunStateSet implements POST /api/resources/{id}/run-state.
//
// 200 with the updated snapshot; 400 for an invalid run_state / source / ts;
// 404 for an unknown resource. The history row and the resources snapshot
// are written in ONE transaction (DB.Do → ResourceStatusRepo.UpsertStatus);
// resources.status / attributes / updated_at never move (§12-B1 分立).
func (s *Server) handleRunStateSet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req runStateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	runState, err := domain.ParseRunState(req.RunState)
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"runState 参数 %q 不是 up/down/degraded/maintenance/unknown", req.RunState)
		return
	}
	source := domain.StatusSourceManual
	if req.Source != "" {
		// W22: only the MANUAL channel is open. The enum-valid but reserved
		// values get their own honest message (not "invalid enum").
		if req.Source == string(domain.StatusSourceAlert) || req.Source == string(domain.StatusSourcePoll) {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"source %q 为保留来源：alert 属于 W23 告警引擎、poll 属于未来轮询通道；当前仅接受 manual", req.Source)
			return
		}
		src, err := domain.ParseStatusSource(req.Source)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "source 参数 %q 不是 manual", req.Source)
			return
		}
		source = src
	}
	ts := time.Now().UTC()
	if req.TS != nil && *req.TS != "" {
		parsed, err := time.Parse(time.RFC3339, *req.TS)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "ts 参数 %q 不是 RFC3339 时间戳", *req.TS)
			return
		}
		ts = parsed.UTC()
	}

	// Existence check first so an unknown resource answers 404 even when the
	// body is valid; the write itself also guards inside the transaction.
	if _, err := s.db.Resources.GetByID(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	err = s.db.Do(r.Context(), func(tx *repo.Tx) error {
		return tx.ResourceStatus.UpsertStatus(r.Context(), domain.ResourceStatus{
			ResourceID: id, TS: ts, RunState: runState, Source: source,
		})
	})
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, runStateSnapshot{
		RunState:   runState.String(),
		RunStateAt: ts.UTC().Format(time.RFC3339),
		Source:     source.String(),
	})
}

// handleRunStateHistory implements GET /api/resources/{id}/run-state/
// history?from=&to= — the flap-analysis read: observations of ONE resource
// in [from, to] (inclusive, RFC3339, optional), ts ASCENDING. 404 for an
// unknown resource, 400 for malformed bounds.
func (s *Server) handleRunStateHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	for name, raw := range map[string]string{"from": from, "to": to} {
		if raw == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "%s 参数 %q 不是 RFC3339 时间戳", name, raw)
			return
		}
	}
	if _, err := s.db.Resources.GetByID(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items, err := s.db.ResourceStatus.ListHistory(r.Context(), id, from, to)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	out := make([]runStateHistoryItem, 0, len(items))
	for _, st := range items {
		out = append(out, runStateHistoryItem{
			TS:       st.TS.UTC().Format(time.RFC3339),
			RunState: st.RunState.String(),
			Source:   st.Source.String(),
		})
	}
	respondJSON(w, http.StatusOK, struct {
		Items []runStateHistoryItem `json:"items"`
		Total int                   `json:"total"`
	}{Items: out, Total: len(out)})
}
