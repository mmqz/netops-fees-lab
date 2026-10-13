package domain

import (
	"fmt"
	"time"
)

// RunState is the OPERATIONAL state of a resource (§12-B1 方案 C, issue #22,
// W22): the runtime view kept STRICTLY separate from the ledger status
// (resources.status — 台账状态, free text imported from workbooks). run_state
// is written by humans (source=manual) and, from W23 on, by the alert
// detection engine (source=alert); it is the root-cause verdict input of
// §10.5 (impact candidates/impacted classification).
//
// The closed enum mirrors the migration v17 CHECK constraint:
// up / down / degraded / maintenance / unknown.
type RunState string

const (
	// RunStateUp means the resource is operational.
	RunStateUp RunState = "up"
	// RunStateDown means the resource is out of service.
	RunStateDown RunState = "down"
	// RunStateDegraded means the resource works but performs below par.
	RunStateDegraded RunState = "degraded"
	// RunStateMaintenance means the resource is under planned maintenance.
	RunStateMaintenance RunState = "maintenance"
	// RunStateUnknown means explicitly recorded as unknown.
	RunStateUnknown RunState = "unknown"
)

// String implements fmt.Stringer.
func (r RunState) String() string { return string(r) }

// IsValid reports whether r is one of the five closed-enum values.
func (r RunState) IsValid() bool {
	switch r {
	case RunStateUp, RunStateDown, RunStateDegraded, RunStateMaintenance, RunStateUnknown:
		return true
	}
	return false
}

// ParseRunState converts s into a RunState; unknown values are an error.
func ParseRunState(s string) (RunState, error) {
	v := RunState(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid run_state %q (up/down/degraded/maintenance/unknown)", s)
	}
	return v, nil
}

// StatusSource is who produced one run-state observation (§12-B1). The
// closed enum mirrors the migration v17 CHECK constraint:
// alert / poll / manual. W22 only writes "manual" through the HTTP endpoint —
// "alert" is reserved for the W23 detection engine (the API rejects any
// non-manual source with 400 until then); "poll" is reserved for a future
// scheduled-probe writer.
type StatusSource string

const (
	// StatusSourceAlert is the W23 alert detection engine (reserved).
	StatusSourceAlert StatusSource = "alert"
	// StatusSourcePoll is a future scheduled poller (reserved).
	StatusSourcePoll StatusSource = "poll"
	// StatusSourceManual is a human action (the only W22 writer).
	StatusSourceManual StatusSource = "manual"
)

// String implements fmt.Stringer.
func (s StatusSource) String() string { return string(s) }

// IsValid reports whether s is one of the three closed-enum values.
func (s StatusSource) IsValid() bool {
	switch s {
	case StatusSourceAlert, StatusSourcePoll, StatusSourceManual:
		return true
	}
	return false
}

// ParseStatusSource converts s into a StatusSource; unknown values are an
// error.
func ParseStatusSource(s string) (StatusSource, error) {
	v := StatusSource(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid status source %q (alert/poll/manual)", s)
	}
	return v, nil
}

// ResourceStatus is one run-state OBSERVATION (the history row of
// resource_status, migration v17): value semantics — the same
// (resource_id, ts) upserts (the last writer wins), history accumulates for
// flap analysis. The resources.run_state / run_state_at columns are the
// SNAPSHOT projection of the newest observation (written in the same
// transaction), so whole-DB statistics never join the history table.
type ResourceStatus struct {
	ResourceID string
	TS         time.Time
	RunState   RunState
	Source     StatusSource
}
