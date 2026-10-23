// Package app wires the NetOps Fees Lab application: configuration, the slog
// JSON logger, the SQLite repository, the service composition root (internal/
// services — the single assembly point) and the HTTP API server. Both
// `netopsfees serve` and the tests (httptest.NewServer(app.Routes())) mount
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

	"github.com/mmqz/netops-fees-lab/internal/api"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/metrics"
	"github.com/mmqz/netops-fees-lab/internal/query"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/report"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/validation"
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
	// workbooks to <DataDir>/uploads and the fee layer roots its snapshot
	// and Parquet trees under it. Empty means the caller manages
	// workbook paths itself (CLI analyze/import pass explicit --file paths).
	DataDir string
	// AuthToken is the opt-in write gate: non-empty requires
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
	// Services is the single service composition root: every field
	// below aliases an instance of it, and the same instance flows into
	// the HTTP API (a.API) and the MCP server (cli's cmdMCP) — one
	// assembly, shared everywhere, no dead second wiring.
	Services *services.Services
	// Importer is the import pipeline service (the resource universe's
	// entry channel).
	Importer *importer.Service
	// Validation is the set-level validation engine.
	Validation *validation.Engine
	// Query is the read-side query service.
	Query *query.Service
	// Fees is the fee analysis layer service: the CLI fee commands and
	// the HTTP fee routes share this instance (the composition root's
	// Fees, rooted at Config.DataDir).
	Fees *fees.Service
	// Report is the quality report generator.
	Report *report.Generator
	// Metrics is the timeseries metric ingest/query service (the
	// cross-check scenarios' other ledger).
	Metrics *metrics.Service
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

	// ONE composition root (internal/services) — the App fields alias its
	// instances and the SAME instance is handed to the HTTP API, so there
	// is no dead second assembly. The MCP server consumes it too (cmdMCP
	// passes a.Services to mcpserver.Run).
	svc := services.New(db, services.WithFees(cfg.DataDir))
	a := &App{
		Config:     cfg,
		Log:        log,
		DB:         db,
		Services:   svc,
		Importer:   svc.Importer,
		Validation: svc.Validation,
		Query:      svc.Query,
		Fees:       svc.Fees,
		Report:     svc.Report,
		Metrics:    svc.Metrics,
	}
	a.API = api.New(log, svc, cfg.DataDir)
	// The write gate rides the same server instance — set BEFORE Routes()
	// is ever called (the middleware reads it per request, so tests can
	// also flip it on an existing server).
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

// Run starts the HTTP server and blocks until SIGINT/SIGTERM arrives (or ctx
// is cancelled), then shuts down gracefully within 5 seconds.
func (a *App) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.Config.Addr,
		Handler:           a.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		// Absolute db path (issue #8): the serve log line is the
		// ground truth to diff against the CLI's --db when a resource
		// 404s — a relative path like ./data/netops.db resolves
		// differently per working directory.
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
