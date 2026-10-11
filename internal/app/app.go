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
	"github.com/mmqz/netops-lab/internal/importer"
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
	// Lab is the simulation workbench engine (Phase 2: materialize, ops,
	// checks, undo, checkpoints).
	Lab *sim.Service
	// Report is the quality report generator.
	Report *report.Generator
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
		Lab:        svc.Lab,
		Report:     svc.Report,
	}
	a.API = api.New(log, svc, cfg.DataDir)
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

// Run starts the HTTP server and blocks until SIGINT/SIGTERM arrives (or
// ctx is cancelled), then shuts down gracefully within 5 seconds.
func (a *App) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.Config.Addr,
		Handler:           a.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("http server listening", "addr", a.Config.Addr, "db", a.Config.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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
