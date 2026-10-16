// Package app wires the NetOps Lab application: configuration, the slog JSON
// logger, the SQLite repository, the service composition root (internal/
// services — the single assembly point, W9-D) and the HTTP API server. Both
// `netopslab serve` and the tests (httptest.NewServer(app.Routes())) mount
// the same wiring; the CLI subcommands (internal/app/cli.go) reuse App for
// their service access.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mmqz/netops-lab/internal/api"
	"github.com/mmqz/netops-lab/internal/changes"
	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/governance"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/notify"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/report"
	"github.com/mmqz/netops-lab/internal/services"
	"github.com/mmqz/netops-lab/internal/sim"
	"github.com/mmqz/netops-lab/internal/validation"
)

// shutdownTimeout bounds graceful HTTP shutdown (SIGINT/SIGTERM).
const shutdownTimeout = 5 * time.Second

// Config is the application configuration.
type Config struct {
	// Addr is the HTTP listen address of serve (default ":8080").
	Addr string
	// DBPath is the SQLite database path (default ./data/netops.db).
	DBPath string
	// DataDir is the upload storage root; the importer writes analyzed
	// workbooks to <DataDir>/uploads. Empty means the caller manages
	// workbook paths itself (CLI analyze/import pass explicit --file paths).
	DataDir string
	// WebhookURL is the outbound detection-run notification target
	// (v0.10-R8): after every completed detection run (HTTP POST
	// /api/detection/run or CLI `netopslab detect`), a JSON summary is
	// delivered to this URL. Empty means the channel is off. Delivery is
	// best-effort: a failure is logged at warn level and never fails the
	// run itself.
	WebhookURL string
	// DetectInterval (v0.11-R11) is the serve-mode scheduled inspection
	// interval: every tick fires ONE recorded detection sweep (source =
	// scheduler) and, when WebhookURL is set, the notification. Zero (the
	// default) keeps the v0.10 explicit-trigger-only behavior — the
	// patrol is strictly opt-in. The serve flag validates the 1s
	// minimum; values below it are a usage error, not a silent clamp.
	DetectInterval time.Duration
	// AuthToken (v0.11-R13) is the opt-in write gate: non-empty requires
	// Authorization: Bearer <token> on every mutating /api request.
	// Reads / health / OpenAPI / the SPA stay open (the demo and AI
	// analysis surface); the gate is where audit identity belongs.
	// Empty = off.
	AuthToken string
}

// defaults fills empty fields with the documented defaults.
func (c *Config) defaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(".", "data", "netops.db")
	}
}

// App is the composed application. All fields are wired by New and read-only
// afterwards; Close releases the database.
type App struct {
	// Config is the effective configuration (defaults applied).
	Config Config
	// Log is the JSON structured logger (stderr in production).
	Log *slog.Logger
	// DB is the SQLite repository.
	DB *repo.DB
	// Services is the single service composition root (W9-D): every field
	// below aliases an instance of it, and the same instance flows into the
	// HTTP API (a.API) and the MCP server (cli's cmdMCP) — one assembly,
	// shared everywhere, no dead second wiring.
	Services *services.Services
	// Importer is the import pipeline service.
	Importer *importer.Service
	// Validation is the set-level validation engine.
	Validation *validation.Engine
	// Query is the read-side query service.
	Query *query.Service
	// Changes is the attribute change confirmation service (W4-D).
	Changes *changes.Service
	// Renames is the rename confirmation service (W6-D).
	Renames *renames.Service
	// Governance is the governance overview aggregation service (W14-D).
	Governance *governance.Service
	// Lab is the simulation workbench engine (Phase 2: materialize, ops,
	// checks, undo, checkpoints).
	Lab *sim.Service
	// Report is the quality report generator.
	Report *report.Generator
	// Metrics is the timeseries metric ingest/query service (W20).
	Metrics *metrics.Service
	// Detect is the alert detection engine (W23, migration v18 —
	// ADR-0011 D2/D3): the same instance the HTTP API serves.
	Detect *detect.Engine
	// Webhook is the outbound detection-run notifier (v0.10-R8): nil
	// when Config.WebhookURL is empty (channel off). Shared by the HTTP
	// API (a.API.Webhook) and the CLI detect command — one notifier,
	// both trigger channels.
	Webhook *notify.Webhook
	// API is the HTTP API server.
	API *api.Server
}

// New opens the database, applies migrations, ensures the data directory
// exists (when configured) and wires all services. The logger writes JSON
// to stderr — stdout stays reserved for the CLI's single-JSON-object
// contract.
func New(cfg Config) (*App, error) {
	cfg.defaults()

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("app: create db directory %s: %w", dir, err)
		}
	}
	if cfg.DataDir != "" {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return nil, fmt.Errorf("app: create data directory %s: %w", cfg.DataDir, err)
		}
	}

	db, err := repo.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("app: open db %s: %w", cfg.DBPath, err)
	}

	// W9-D: ONE composition root (internal/services) — the App fields alias
	// its instances and the SAME instance is handed to the HTTP API, so the
	// pre-W9-D dead assembly (the API building its own second service set
	// from the bare db) is gone. The MCP server consumes it too (cmdMCP
	// passes a.Services to mcpserver.Run).
	svc := services.New(db)
	a := &App{
		Config:     cfg,
		Log:        log,
		DB:         db,
		Services:   svc,
		Importer:   svc.Importer,
		Validation: svc.Validation,
		Query:      svc.Query,
		Changes:    svc.Changes,
		Renames:    svc.Renames,
		Governance: svc.Governance,
		Lab:        svc.Lab,
		Report:     svc.Report,
		Metrics:    svc.Metrics,
		Detect:     svc.Detect,
		Webhook:    notify.New(cfg.WebhookURL, 0, db, log),
	}
	a.API = api.New(log, svc, cfg.DataDir)
	a.API.Webhook = a.Webhook
	// v0.11-R13: the write gate rides the same server instance — set
	// BEFORE Routes() is ever called (the middleware reads it per
	// request, so tests can also flip it on an existing server).
	a.API.AuthToken = cfg.AuthToken
	return a, nil
}

// Routes returns the full HTTP handler (API + embedded SPA). The tests mount
// it with httptest.NewServer.
func (a *App) Routes() http.Handler {
	return a.API.Routes()
}

// Close releases the application's resources (the database).
func (a *App) Close() error {
	if err := a.DB.Close(); err != nil {
		return fmt.Errorf("app: close db: %w", err)
	}
	return nil
}

// Run starts the HTTP server (and, when Config.DetectInterval > 0, the
// scheduled-inspection patrol) and blocks until SIGINT/SIGTERM arrives (or
// ctx is cancelled), then shuts down gracefully within 5 seconds — one
// signal stops both the listener and the scheduler.
func (a *App) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.Config.Addr,
		Handler:           a.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// The signal context is registered BEFORE the listener so the
	// scheduler (v0.11-R11) can share the SAME cancellation: one Ctrl-C
	// stops the patrol mid-wait, and a round in flight keeps its ctx.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if a.Config.DetectInterval > 0 {
		// The patrol reuses the composition root's engine + webhook
		// method value (no second notifier, no second engine); Run
		// blocks until ctx.Done, so it lives in its own goroutine.
		var notifyFn detect.NotifyFunc
		if a.Webhook != nil {
			notifyFn = a.Webhook.NotifyDetectionRun
		}
		sched := detect.NewScheduler(a.Config.DetectInterval, a.DB, a.Detect, notifyFn, a.Log)
		a.API.Schedule = sched
		go sched.Run(ctx)
	}

	errCh := make(chan error, 1)
	go func() {
		// Absolute db path (issue #8): the serve log line is the
		// ground truth to diff against the CLI's --db when a
		// workspace 404s — a relative path like ./data/netops.db
		// resolves differently per working directory.
		dbAbs := a.Config.DBPath
		if abs, err := filepath.Abs(dbAbs); err == nil {
			dbAbs = abs
		}
		a.Log.Info("http server listening", "addr", a.Config.Addr, "db", dbAbs)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("app: http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: graceful shutdown: %w", err)
	}
	// Drain the listener goroutine.
	if err := <-errCh; err != nil {
		return fmt.Errorf("app: http server: %w", err)
	}
	a.Log.Info("http server stopped")
	return nil
}
