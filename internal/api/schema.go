package api

import (
	"net/http"

	"github.com/mmqz/netops-lab/internal/mapping"
)

// handleSchema implements GET /api/schema (W5-D, v0.4.0): dynamic schema
// discovery. The body is the faithful projection of the mapping.Specs
// registry (field names, kinds, required/natural-key flags, enums, refs,
// synonyms, natural-key and conditional-fallback keys, display-name rules)
// plus the domain rule catalog (code, severity, description) — all generated
// from the Go specs, nothing hand-copied (BuildSchemaDocument walks Specs;
// the mapping package's projection tests pin the walk field by field).
//
// The endpoint is HTTP-only: the MCP tool surface stays frozen at the five
// read-only tools (schema discovery is not an MCP capability).
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, mapping.BuildSchemaDocument())
}
