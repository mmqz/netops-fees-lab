package importer

import (
	"context"
	"fmt"
)

// interruptedReason is recorded on every batch healed by
// RecoverInterrupted. The leading "interrupted" token is contractual:
// the healed state must be recognizable as "the pipeline never reached a
// terminal state", not as an ordinary import failure.
const interruptedReason = "interrupted: import pipeline stopped before reaching a terminal state (process crash or restart)"

// RecoverInterrupted heals crash-window batches (R1-D6). A process death
// between UpdateStatus(importing) and the import transaction's terminal
// write (execute.go), or between batch creation (analyzing) and the
// analysis completion, leaves a batch stuck in a transient state forever —
// no sweeper existed. This resets every analyzing/importing batch to
// failed(interrupted), stamps finished_at once and returns the number of
// healed batches.
//
// Safety rules:
//
//   - awaiting_mapping batches are NOT touched: that state is the stable
//     "waiting for the user's mapping confirmation" state, not a crash
//     window — a batch uploaded before a restart must survive it.
//   - Terminal batches (completed / failed / rolled_back) are untouchable
//     by the UPDATE's WHERE clause.
//   - A false positive (another process's import genuinely in flight while
//     this runs) is tolerated by design: the importer writes its terminal
//     status INSIDE the import transaction (execute.go: UpdateStatus at the
//     end of db.Do), which overwrites the heal afterwards.
//   - Idempotent: a second run finds no transient batch and returns 0.
//   - Call sites: only where no import of THIS process is in flight —
//     serve startup and the CLI import entry (internal/app/cli.go). Never
//     in repo.Open or the MCP path: an MCP process starting alongside an
//     HTTP process could kill a genuinely in-flight import (the tolerated
//     false positive above is still avoided where cheaply possible).
func (s *Service) RecoverInterrupted(ctx context.Context) (int64, error) {
	n, err := s.db.Batches.MarkInterruptedImportsFailed(ctx, interruptedReason)
	if err != nil {
		return 0, fmt.Errorf("importer: recover interrupted imports: %w", err)
	}
	return n, nil
}
