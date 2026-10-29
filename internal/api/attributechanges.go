package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mmqz/netops-fees-lab/internal/changes"
	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// handleAttributeChangesList implements
// GET /api/attribute-changes?status=pending|applied|dismissed|all&resource_id=&batch_id=&limit=&offset=
// → {"items":[{id,resourceId,resourceType,naturalKey,batchId,field,oldValue,newValue,status,sheet,rowNumber,createdAt,appliedAt,relationRetargeted}],"total":n}.
// relationRetargeted on a pending item is the PREVIEW of the same predicate
// the apply reports: a ref field whose resource owns the derived edge will
// retarget it on confirm (the web hint). The pagination bounds follow the
// §9 list-endpoint conventions (limit defaults to 20, clamps at 100; offset
// defaults to 0, must be >= 0). status=all (or absent) lists every status;
// an unknown status is a 400.
func (s *Server) handleAttributeChangesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := changes.Filter{
		ResourceID: q.Get("resource_id"),
		BatchID:    q.Get("batch_id"),
		Limit:      changes.DefaultPageSize,
		Offset:     0,
	}
	if raw := q.Get("status"); raw != "" && raw != "all" {
		st, err := domain.ParseChangeStatus(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "status 参数 %q 不是 pending/applied/dismissed/all", raw)
			return
		}
		filter.Status = st
	}
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "limit 参数 %q 不是正整数", raw)
			return
		}
		if v > changes.MaxPageSize {
			v = changes.MaxPageSize // clamp, never fail (§9: max 100)
		}
		filter.Limit = v
	}
	if raw := q.Get("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "offset 参数 %q 不是非负整数", raw)
			return
		}
		filter.Offset = v
	}
	result, err := s.changes.List(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleAttributeChangeApply implements POST /api/attribute-changes/{id}/apply:
// 200 with the change-after-apply object on success; 409 conflict when the
// proposal is no longer pending; 409 attribute_conflict (StaleError — the
// message names the field and both values) when the resource's current
// value has drifted from the recorded old value; 409 ref_target_missing
// (RefTargetMissingError, W13-D — the changed field is a ref field whose
// new value no longer resolves to a resource of the referenced type; the
// message names the field, the target key and the referenced type) when
// the retarget guard refuses; 404 for an unknown id or a vanished resource.
// The 200 body carries relationRetargeted: true when a ref-field apply
// retargeted the derived relation to the new target.
func (s *Server) handleAttributeChangeApply(w http.ResponseWriter, r *http.Request) {
	item, err := s.changes.Apply(r.Context(), r.PathValue("id"))
	if err != nil {
		var stale *changes.StaleError
		if errors.As(err, &stale) {
			// 409 with the dedicated code; the message carries the field
			// name and both values (StaleError.Error is the user-facing copy).
			respondError(w, http.StatusConflict, "attribute_conflict", stale.Error())
			return
		}
		var missing *changes.RefTargetMissingError
		if errors.As(err, &missing) {
			// 409 with the dedicated code (W13-D); the message carries the
			// field, the target key and the referenced type, and the zero-
			// change promise (RefTargetMissingError.Error is the user copy).
			respondError(w, http.StatusConflict, "ref_target_missing", missing.Error())
			return
		}
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}

// handleAttributeChangeDismiss implements POST
// /api/attribute-changes/{id}/dismiss: 200 with the change-after-dismiss
// object; 409 conflict when the proposal is no longer pending; 404 for an
// unknown id.
func (s *Server) handleAttributeChangeDismiss(w http.ResponseWriter, r *http.Request) {
	item, err := s.changes.Dismiss(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}
