package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/query"
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

// handleResourceRelations implements GET /api/resources/{id}/relations?depth=1|2
// (default 1, max 2): {"center", "nodes", "edges"} via internal/topology.
func (s *Server) handleResourceRelations(w http.ResponseWriter, r *http.Request) {
	depth := 1
	if raw := r.URL.Query().Get("depth"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > query.MaxRelationDepth {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"depth 参数 %q 必须是 1 或 2", raw)
			return
		}
		depth = v
	}
	result, err := s.query.Relations(r.Context(), r.PathValue("id"), depth)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}
