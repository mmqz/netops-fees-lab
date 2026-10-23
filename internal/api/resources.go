package api

import (
	"net/http"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/query"
)

// handleResourcesList implements GET /api/resources?type=&q=&status=&page=&pageSize=
// (§9 field names via query.ResourceItem; pagination envelope).
func (s *Server) handleResourcesList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	filter := query.SearchFilter{
		Q:        r.URL.Query().Get("q"),
		Status:   r.URL.Query().Get("status"),
		Page:     page,
		PageSize: pageSize,
	}
	if raw := r.URL.Query().Get("type"); raw != "" {
		t, err := domain.ParseResourceType(raw)
		if err != nil {
			// #108: the closed set is DERIVED from the domain registry,
			// never hand-copied. The pre-W21 8-type spelling went stale the
			// day the registry extended to 33 types (W21) and has been lying
			// to every caller asking for a W21 type since; a registry-driven
			// join cannot drift (same doctrine as the CLI/MCP faces, #91).
			types := make([]string, 0, len(domain.AllResourceTypes()))
			for _, rt := range domain.AllResourceTypes() {
				types = append(types, rt.String())
			}
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"type 参数必须是注册资源类型之一（%s），收到 %q", strings.Join(types, "/"), raw)
			return
		}
		filter.Type = t
	}

	result, err := s.query.SearchResources(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleResourceDetail implements GET /api/resources/{id}: the resource plus
// sourceRefs[] and relationCount.
func (s *Server) handleResourceDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := s.query.ResourceDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, detail)
}
