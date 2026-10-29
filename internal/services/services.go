// Package services is the SINGLE composition root of NetOps Fees Lab: the
// one place that assembles the service instances from a *repo.DB — the
// importer, the validation engine, the query service, the report generator,
// the timeseries metric service and the fee analysis service.
//
// This is the fee-domain fork of netops-lab (v0.16.1, 2223101): the
// composition deliberately carries ONLY the fee line — resources (the
// association universe), metrics (the cross-check ledger's other half) and
// fees (the analysis core). The IPRAN governance/simulation/detection
// surfaces stayed in the parent repository.
//
// The package deliberately depends only on the layers below it (repo and the
// service packages); it must never import app, api or mcpserver — those are
// consumers of the composition, not parts of it.
package services

import (
	"github.com/mmqz/netops-fees-lab/internal/changes"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/metrics"
	"github.com/mmqz/netops-fees-lab/internal/query"
	"github.com/mmqz/netops-fees-lab/internal/renames"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/report"
	"github.com/mmqz/netops-fees-lab/internal/validation"
)

// Services is the assembled application core: every field is constructed
// exactly once by New and shared by all consumers (CLI subcommands, the HTTP
// API, the MCP server). The fields are read-only by convention — consumers
// alias them (App.Importer = svc.Importer), never rebuild them.
type Services struct {
	db *repo.DB

	// Importer is the workbook import pipeline (the resource universe's
	// entry channel — the fee scenarios' association target).
	Importer *importer.Service
	// Validation is the set-level validation engine.
	Validation *validation.Engine
	// Query is the read-side query service.
	Query *query.Service
	// Report is the quality report generator.
	Report *report.Generator
	// Metrics is the timeseries metric ingest/query service (the
	// cross-check scenarios' other ledger: 有费无量 / 有量无费).
	Metrics *metrics.Service
	// Fees is the fee-data analysis layer service (#58/#60/#61, migration
	// v29): snapshot + Parquet import pipeline + the query presets. Needs
	// the data dir (the snapshot and analysis roots live under it) —
	// wired via WithFees. An EMPTY data dir roots the analysis tree at
	// the process CWD ("analysis"); no production entry point may do
	// that — serve, the fees CLI and the MCP server all pass their
	// --data/--data-dir explicitly (#97).
	Fees *fees.Service
	// Changes is the attribute change confirmation service (W005, migration
	// v11): the human channel that applies or dismisses the attribute diffs
	// the importer records on duplicate-key rows.
	Changes *changes.Service
	// Renames is the rename confirmation service (W006, migration v12): the
	// human channel that merges or rejects the suspected-rename proposals of
	// name-keyed types.
	Renames *renames.Service
}

// DB returns the repository every service of the composition is bound to.
// Handlers with thin repo-passthrough read paths (batch/issue listings) use
// it directly instead of forcing a service detour.
func (s *Services) DB() *repo.DB { return s.db }

// options carries the customizable knobs of New.
type options struct {
	feesDir string
}

// Option customizes the assembly performed by New.
type Option func(*options)

// WithFees wires the fee analysis service with its data-dir root (the
// snapshot layer and the Parquet analysis tree live under it). Every
// production entry point calls this with its --data/--data-dir value
// (serve, the fees CLI, the MCP server); passing "" roots the analysis
// tree at the CWD-relative "analysis" — tests only, never production
// (#97).
func WithFees(dataDir string) Option {
	return func(o *options) { o.feesDir = dataDir }
}

// New assembles the full service set from db — the ONLY assembly point of
// the application (see the package comment). The returned instance is meant
// to be constructed once per process (app.New) and passed through to every
// consumer; wiring tests assert the single-instance guarantee by pointer
// identity.
func New(db *repo.DB, opts ...Option) *Services {
	o := options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	s := &Services{
		Fees:       fees.NewService(db, o.feesDir),
		db:         db,
		Importer:   importer.New(db),
		Validation: validation.New(db),
		Query:      query.New(db),
		Report:     report.New(db),
		Metrics:    metrics.New(db),
		Changes:    changes.New(db),
		Renames:    renames.New(db),
	}
	return s
}
