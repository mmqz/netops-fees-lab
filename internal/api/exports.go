package api

import (
	"net/http"

	"github.com/mmqz/netops-lab/internal/exporter"
)

// exportContentType / exportFileName pin the download response of the
// export route (W12-D): the standard OOXML spreadsheet media type and a
// FIXED file name (the types filter deliberately does NOT enter the file
// name — the CLI/web docs declare it).
const (
	exportContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	exportFileName    = "netops-resources.xlsx"
)

// handleExportResources implements GET /api/exports/resources?types=a,b
// (W12-D): stream the current resource inventory as an .xlsx workbook
// that is isomorphic to an import workbook — one sheet per resource type
// with resources (mapping.Specs order), headers = field Labels, one row
// per resource = its current attribute snapshot. See internal/exporter
// for the projection, the determinism guarantees and the round-trip
// (回源幂等) semantics.
//
// Query parameter: types — a comma-separated resource type list; empty (or
// absent) exports every type. An unknown type value answers 400
// bad_request (the uniform error envelope); the types order is
// normalized to the Specs order, duplicates are collapsed.
//
// Response: 200 with the xlsx bytes, Content-Type
// application/vnd.openxmlformats-officedocument.spreadsheetml.sheet and
// Content-Disposition attachment; filename="netops-resources.xlsx" (fixed
// name). An EMPTY database also exports successfully — the workbook then
// carries only the neutral placeholder sheet (no per-type sheets), which
// is the agreed empty-export shape (a re-import attempt of it reports
// empty_workbook, honestly reflecting that there is nothing to import).
func (s *Server) handleExportResources(w http.ResponseWriter, r *http.Request) {
	types, err := exporter.ParseTypes(r.URL.Query().Get("types"))
	if err != nil {
		// A bad types value is a client error, not a service failure: the
		// 400 bad_request envelope names the offending value.
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resources, err := s.query.AllByTypes(r.Context(), types)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	data, summary, err := exporter.BuildBytes(resources, types)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	w.Header().Set("Content-Type", exportContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFileName+`"`)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		// The header (and status) is already sent; the middleware's
		// statusRecorder keeps the log honest. Nothing to recover here.
		s.log.Debug("export write interrupted", "error", err.Error())
	}
	if summary.TotalRows > 0 {
		s.log.Info("export resources", "rows", summary.TotalRows, "types", len(summary.Types))
	}
}
