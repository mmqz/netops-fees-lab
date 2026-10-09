package sim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mmqz/netops-lab/internal/repo"
)

// DeleteWorkspace removes a workspace and everything it owns
// (PROJECT_BRIEF_P2 §4 last paragraph): the repo's transactional cascade
// purge returns the per-table removed counts (virtual_devices, interfaces,
// links, changesets, check_runs, checkpoints — keyed by table name), and
// the workspace's checkpoint SNAPSHOT DIRECTORY on disk is best-effort
// removed afterwards (<dbdir>/checkpoints/<workspaceID>/). Directory
// removal failures (a file locked by another handle, a missing directory)
// do NOT fail the deletion — the database rows are already gone; a
// directory that could not be removed is an orphan on disk, not
// inconsistent state. An unknown workspace yields domain.ErrNotFound.
func (s *Service) DeleteWorkspace(ctx context.Context, workspaceID string) (map[string]int64, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}

	var counts map[string]int64
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		var err error
		counts, err = tx.Workspaces.Delete(ctx, workspaceID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sim: delete workspace %s: %w", workspaceID, err)
	}

	// Best-effort snapshot cleanup AFTER the row purge: os.RemoveAll is nil
	// on a missing path, so ENOENT needs no special case; every other error
	// is deliberately swallowed (see the doc comment).
	dir := filepath.Join(filepath.Dir(s.db.Path()), checkpointDir, workspaceID)
	_ = os.RemoveAll(dir)
	return counts, nil
}
