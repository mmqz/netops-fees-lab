// W14-D: the governance overview endpoint. One thin GET handler — all
// aggregation lives in internal/governance (the composition root's
// Governance service), every count is a SQL COUNT aggregate.

package api

import "net/http"

// handleGovernanceSummary implements GET /api/governance/summary: the
// read-only governance aggregate — pending confirmation queues, decision
// audit counters, issue distribution (by severity / by rule) and the
// resource / relation / batch inventory shapes. No parameters; 200 with the
// all-zero structure on an empty database (the endpoint is a live database
// view, never 204/404; Cache-Control: no-store comes from the middleware
// like every /api response).
func (s *Server) handleGovernanceSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.governance.Summary(r.Context())
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, summary)
}
