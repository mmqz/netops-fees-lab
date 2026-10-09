// Package sim implements the simulation engine of NetOps Lab
// (PROJECT_BRIEF_P2 §4–§7): the typed operation catalog with changesets and
// computed inverses, workspace materialization from imported resources, the
// five deterministic checks, connectivity search, the canonical workspace
// state hash and the before/after run diff.
//
// The Service owns ALL writes to the simulation tables (workspaces, devices,
// interfaces, links, changesets, check runs). Every mutation flows through
// the changeset machinery — materialization excepted, which is one direct
// transactional write per PROJECT_BRIEF_P2 §6. The engine is deterministic:
// the same workspace state plus rule version always produces the same check
// results in the same canonical order, and two fresh databases given the
// same materialization and op script produce identical ids, state hashes and
// runs.
//
// Errors follow the layered convention of PROJECT_BRIEF §14: sentinel errors
// in internal/domain are wrapped with fmt.Errorf("…: %w", err) so errors.Is
// works through every layer. Op application failures carry the failing op
// as *OpError (wrapping domain.ErrOpFailed); request-decode and payload validation
// failures wrap domain.ErrInvalidOp.
package sim

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// Service is the simulation engine. It owns ALL writes to the sim tables;
// every mutation flows through changesets (materialization excepted — it is
// a direct transactional write per PROJECT_BRIEF_P2 §6).
type Service struct {
	db *repo.DB

	// Now is the clock hook; tests may replace it for deterministic
	// timestamps. New installs time.Now; a nil hook falls back to
	// time.Now.
	Now func() time.Time

	// afterWrite, when non-nil, is invoked after each write stage of
	// Materialize inside its transaction. It exists exclusively for
	// failure injection in the atomicity test (mirroring the importer's
	// afterSheet hook) and is nil in production; it is only settable from
	// within the package (tests live in package sim).
	afterWrite func(stage string) error
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db, Now: time.Now}
}

// clock returns the current UTC time, honoring the Now hook.
func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// mustWorkspace verifies that the workspace exists, returning an error
// wrapping domain.ErrNotFound otherwise. Every public entry point calls it
// before touching changeset or run machinery so unknown workspaces fail
// cleanly instead of on a foreign key violation mid-transaction.
func (s *Service) mustWorkspace(ctx context.Context, workspaceID string) (domain.Workspace, error) {
	w, err := s.db.Workspaces.GetByID(ctx, workspaceID)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("sim: workspace %s: %w", workspaceID, err)
	}
	return w, nil
}
