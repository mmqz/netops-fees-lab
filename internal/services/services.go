// Package services is the SINGLE composition root of NetOps Lab (W9-D): the
// one place that assembles the service instances from a *repo.DB — the
// importer, the validation engine, the query service, the two confirmation
// services (attribute changes, renames), the simulation engine, the report
// generator and the lab report generator.
//
// Before W9-D the assembly existed three times (app.New, api.NewServer and
// mcpserver.NewServer each ran their own validation.New/query.New/… set), so
// the App's Query/Changes/Renames fields were a dead assembly for the HTTP
// API — the handlers used their own second instances — and the MCP server
// built yet another sim.Service. Now app.New, api.NewServer and
// mcpserver.NewServer all consume the SAME *Services instance: one
// construction, one wiring, zero drift between the CLI, HTTP and MCP
// surfaces.
//
// The package deliberately depends only on the layers below it (repo and the
// service packages); it must never import app, api or mcpserver — those are
// consumers of the composition, not parts of it.
package services

import (
	"time"

	"github.com/mmqz/netops-lab/internal/changes"
	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/fees"
	"github.com/mmqz/netops-lab/internal/governance"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/report"
	"github.com/mmqz/netops-lab/internal/sim"
	"github.com/mmqz/netops-lab/internal/validation"
)

// Services is the assembled application core: every field is constructed
// exactly once by New and shared by all consumers (CLI subcommands, the HTTP
// API, the MCP server). The fields are read-only by convention — consumers
// alias them (App.Importer = svc.Importer), never rebuild them.
type Services struct {
	db *repo.DB

	// Importer is the workbook/script import pipeline.
	Importer *importer.Service
	// Validation is the set-level validation engine.
	Validation *validation.Engine
	// Query is the read-side query service.
	Query *query.Service
	// Changes is the attribute change confirmation service (W4-D).
	Changes *changes.Service
	// Renames is the rename confirmation service (W6-D).
	Renames *renames.Service
	// Governance is the governance overview aggregation service (W14-D):
	// the read-only summary of the pending queues, decision audit, issue
	// distribution and inventory shapes.
	Governance *governance.Service
	// Lab is the simulation workbench engine (materialize, ops, checks,
	// undo, checkpoints) — the App's Lab field and the MCP lab tools share
	// this one instance.
	Lab *sim.Service
	// Report is the quality report generator.
	Report *report.Generator
	// Metrics is the timeseries metric ingest/query service (W20,
	// migration v14 — ADR-0011 D1's separate channel).
	Metrics *metrics.Service
	// Fees is the fee-data analysis layer service (#58/#60/#61, migration
	// v29): snapshot + Parquet import pipeline + the query presets. Needs
	// the data dir (the snapshot and analysis roots live under it) — wired
	// via WithFees when the app knows it; nil DataDir roots at "./data".
	Fees *fees.Service

	// Detect is the alert detection engine (W23, migration v18 —
	// ADR-0011 D2/D3): the rules side whose query cousin is
	// Metrics.Anomalies. HTTP and CLI call the same instance, no bypass.
	Detect *detect.Engine
	// LabReport is the lab report generator, wired to the SAME Lab engine
	// instance as the field above (report.NewLab(db, Lab) — the api server's
	// lab report handlers consume it).
	LabReport *report.LabGenerator
}

// DB returns the repository every service of the composition is bound to.
// Handlers with thin repo-passthrough read paths (batch/issue listings) use
// it directly instead of forcing a service detour.
func (s *Services) DB() *repo.DB { return s.db }

// options carries the customizable knobs of New.
type options struct {
	clock   func() time.Time
	feesDir string
}

// Option customizes the assembly performed by New.
type Option func(*options)

// WithFees wires the fee analysis service with its data-dir root (the
// snapshot layer and the Parquet analysis tree live under it). The
// composition root calls this when the app configuration carries a
// DataDir; the CLI fee command builds its own instance the same way.
func WithFees(dataDir string) Option {
	return func(o *options) { o.feesDir = dataDir }
}

// WithClock sets the deterministic clock hook of the two confirmation
// services (changes.Service.Now / renames.Service.Now). Production wiring
// stays with time.Now (the default); tests inject a fixed clock so decided-at
// timestamps are deterministic across the whole composition.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.clock = now }
}

// New assembles the full service set from db — the ONLY assembly point of
// the application (see the package comment). The returned instance is meant
// to be constructed once per process (app.New) and passed through to every
// consumer; wiring tests assert the single-instance guarantee by pointer
// identity.
func New(db *repo.DB, opts ...Option) *Services {
	o := options{clock: time.Now}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	lab := sim.New(db)
	s := &Services{
		Fees:       fees.NewService(db, o.feesDir),
		db:         db,
		Importer:   importer.New(db),
		Validation: validation.New(db),
		Query:      query.New(db),
		Changes:    changes.New(db),
		Renames:    renames.New(db),
		Governance: governance.New(db),
		Lab:        lab,
		Report:     report.New(db),
		Metrics:    metrics.New(db),
		Detect:     detect.New(),
		LabReport:  report.NewLab(db, lab),
	}
	s.applyClock(o.clock)
	return s
}

// applyClock installs the effective clock hook on the two confirmation
// services (both fall back to time.Now when the hook is nil, so a nil clock
// needs no special casing here — the hook exists for deterministic tests).
func (s *Services) applyClock(clock func() time.Time) {
	if clock == nil {
		return
	}
	s.Changes.Now = clock
	s.Renames.Now = clock
}
