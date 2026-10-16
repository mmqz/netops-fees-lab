package detect

// flap.go — the v0.12-R17 re-fire hold-down (告警抖动治理): the alert
// churn of a threshold-hovering series. The engine's fire/clear contract
// is fire on window-consecutive violations, clear on the first
// non-violating tail (TestDetectFiresClearsAndRefires), and a re-fire
// after a clear creates a NEW alert row — the honest identity, but under
// the patrol scheduler an oscillating series turns that into one row +
// one webhook + one queue entry per cycle: noise, and noise trains
// operators to ignore alarms (the same NOC doctrine that drove R15's
// planned suppression and R16's symptom triage).
//
// The gate: when a finding would create a NEW fire for the (rule,
// series) pair, count the pair's completed fire→clear cycles whose
// ENGINE recovery clears (cleared_at carries the data ts) fall within
// the trailing domain.FlapSpan of EVIDENCE time counted back from the
// finding's own evidence ts (the data timeline, no wall clock — the
// SuppressedWindow doctrine of R15: replayable). Count >=
// domain.FlapThreshold → the fire is HELD: no alert row, no run-state
// write, RunResult.FlapHeld++, listed in the envelope with
// action="flap_held" and the DETERMINISTIC would-be alert id (when the
// oscillation subsides the next sustained fire lands that same identity
// — the NOC can correlate the held line with the later real alert).
//
// Why cleared_at-as-data-ts only: engine recovery clears stamp the last
// evaluated data ts; HUMAN clears stamp the server clock. Only the
// engine's clears are observations of the series itself oscillating —
// a manual clear is an operator decision, not a property of the metric,
// so it never counts toward the threshold. The (from, to] bounds keep
// the count anchored: a clear must precede the would-be fire on the
// data timeline to be part of its history.
//
// Aging: held fires add no rows, so no new transitions appear; the old
// cleared rows exit the trailing span as data advances and the count
// drops below the threshold — the NEXT sustained violation fires for
// real. The hold is bounded by FlapSpan of data time, never forever.
//
// The hold is a gate BEFORE the three-edge machine (ADR-0011 D3),
// exactly like maintenance suppression; recovery clears are never held
// (good news is never suppressed — pinned in flap_test.go).

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// flapCutoff renders the exclusive lower bound of the trailing flap span
// for a would-be fire whose newest evidence point is at anchorTS:
// (anchor - domain.FlapSpan, anchor]. An unparseable anchor is a hard
// error — the finding's LastTS comes from the data channel, so a bad
// value means a corrupted timeline; failing the sweep (and recording the
// failure in the receipt) is the honest outcome, the loadSuppressionWindows
// doctrine.
func flapCutoff(anchorTS string) (string, error) {
	anchor, err := time.Parse(time.RFC3339, anchorTS)
	if err != nil {
		return "", fmt.Errorf("detect: flap anchor %q: %w", anchorTS, err)
	}
	return anchor.Add(-domain.FlapSpan).UTC().Format(time.RFC3339), nil
}

// flapHeld reports whether a NEW fire of the (rule, series) pair must be
// held back as oscillation: the pair's ENGINE-cleared transitions within
// (cutoff, anchor] have reached domain.FlapThreshold. The repo query
// runs inside the sweep transaction — the count and the alert writes see
// one consistent history.
func flapHeld(ctx context.Context, tx *repo.Tx, ruleID, seriesID, anchorTS string) (bool, error) {
	cutoff, err := flapCutoff(anchorTS)
	if err != nil {
		return false, err
	}
	n, err := tx.Alerts.CountClearedInSpan(ctx, ruleID, seriesID, cutoff, anchorTS)
	if err != nil {
		return false, err
	}
	return n >= domain.FlapThreshold, nil
}
