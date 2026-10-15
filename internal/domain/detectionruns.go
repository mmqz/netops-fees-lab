package domain

import "fmt"

// RunSource is the closed origin set of a detection run record (v0.11-R11,
// migration v21): WHO triggered the sweep. The HTTP trigger
// (POST /api/detection/run → api), the CLI twin (`netopslab detect` → cli)
// and the serve-mode interval scheduler (→ scheduler). The MCP face is the
// read-only analysis red line and can NEVER appear here — v0.9.0 pins the
// forbidden tool names (detection_run / alerts_ack / alerts_clear) in
// TestMCPToolRegistry, and the migration v21 CHECK constraint is the
// storage-side twin of that pin: an unknown source value cannot even be
// persisted.
type RunSource string

// The closed run-source set.
const (
	RunSourceAPI       RunSource = "api"
	RunSourceCLI       RunSource = "cli"
	RunSourceScheduler RunSource = "scheduler"
)

// String implements fmt.Stringer.
func (s RunSource) String() string { return string(s) }

// IsValid reports whether s is one of the three closed-enum values.
func (s RunSource) IsValid() bool {
	switch s {
	case RunSourceAPI, RunSourceCLI, RunSourceScheduler:
		return true
	}
	return false
}

// ParseRunSource converts s into a RunSource; unknown values are an error.
func ParseRunSource(s string) (RunSource, error) {
	v := RunSource(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid run source %q (api/cli/scheduler)", s)
	}
	return v, nil
}

// DetectionRun is one row of detection_runs (v0.11-R11, migration v21): the
// persisted receipt of ONE detection sweep, written AFTER the fact whether
// the sweep succeeded or failed — the run history that makes scheduled
// inspection auditable. Until v0.10 the RunResult lived only in the HTTP
// response body and the webhook payload (gone when the connection closes);
// now every trigger origin leaves the same durable trail.
//
//   - RunID: run_<uuid> (domain.NewID) — one row per sweep, never reused;
//   - Source: the closed RunOrigin set (api | cli | scheduler);
//   - StartedAt/FinishedAt/DurationMS: the wall-clock bracket of the sweep
//     (bookkeeping — the DATA timestamps live in evaluated_at);
//   - From/To: the evaluation window bounds, "" = unbounded (echoes the
//     request's from/to, so a narrow-window run is recognizable in the
//     history);
//   - EvaluatedAt: RunResult.EvaluatedAt — the ts of the newest evaluated
//     data point ("" when the sweep failed before evaluation);
//   - the four counters mirror RunResult;
//   - Error: "" on success, the engine error otherwise (a failed scheduled
//     round is itself a recordable operational fact).
type DetectionRun struct {
	RunID       string
	Source      RunSource
	StartedAt   string // RFC3339 clock
	FinishedAt  string // RFC3339 clock
	DurationMS  int64
	From        string // RFC3339 data bound, "" = unbounded
	To          string // RFC3339 data bound, "" = unbounded
	EvaluatedAt string

	Findings         int
	AlertsCreated    int
	AlertsCleared    int
	RunStatesWritten int

	Error string // "" = success
}
