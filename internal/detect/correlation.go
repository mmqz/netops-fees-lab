package detect

// correlation.go — the 根因/次生 correlation pass of the detection engine
// (v0.12-R16, migration v25). The problem it solves: when a physical
// resource breaks, EVERYTHING hanging from it crosses thresholds and the
// queue floods with symptoms; the NOC needs to know which alert to work
// first. The graph already knows the dependency direction — this pass
// projects that knowledge onto the alert rows.
//
// The verdict (recomputed every run for the ACTIVE error/warning alerts —
// observation state, NOT lifecycle state; the ADR-0011 D3 three-edge
// machine is untouched):
//
//   - SECONDARY (疑似次生): an alerting resource exists in this alert's
//     resource's DOWN-closure with a STRICTLY LOWER layer — the
//     physical-ward, cause direction (a device alert explains a circuit
//     alert, a cable alert explains a segment alert). The nearest cause
//     (BFS depth, then layer, then id — deterministic) is linked by id.
//   - PRIMARY (根因嫌疑): no alerting cause below, but an alerting
//     resource exists ABOVE with a STRICTLY HIGHER layer — a dependent
//     with symptoms; this alert is a candidate explanation of them.
//   - '' (unmarked): neither — a lone alert, or info severity (an FYI
//     never participates: it causes nothing and explains nothing).
//
// The STRICT layer delta is the anti-symmetry guard: lateral neighbors
// (same layer — two ports on one device, two circuits on one cable) never
// mark each other, so two siblings alerting together both stay unmarked
// (or both primary if a deeper cause exists) instead of the degenerate
// "each is the other's cause" loop. Layer(A) < Layer(B) and Layer(B) <
// Layer(A) cannot both hold — the verdict relation is a DAG by
// construction.
//
// Determinism: same database (graph + active alerts) → same verdicts, the
// same correlated-cause picks, byte-identical rows. The pass writes every
// active error/warning alert's verdict inside the run's transaction
// (idempotent content-wise); cleared alerts keep their last verdict — the
// audit truth of what the alert looked like while it lived.

import (
	"context"
	"fmt"
	"sort"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/topology"
)

// correlationDepth and correlationNodeBudget bound each resource's closure
// walk: the classic chain (device → board → port → circuit → service) is
// five layers deep; depth 6 reaches the whole ladder without wandering
// into the customer web. The node budget is the engine-side hard cap (the
// traverse engine's zero-value hardening applies below it).
const (
	correlationDepth      = 6
	correlationNodeBudget = 250
)

// txGraphStore adapts the run's own transaction to the topology engines'
// TraverseStore contract — the correlation pass sees the graph exactly as
// the alert sync does (one consistent snapshot, no interleaved writes).
func txGraphStore(tx *repo.Tx) topology.StoreFuncs {
	return topology.StoreFuncs{
		NodeFn: func(ctx context.Context, id string) (topology.NodeInfo, error) {
			r, err := tx.Resources.GetByID(ctx, id)
			if err != nil {
				return topology.NodeInfo{}, err
			}
			return topology.NodeInfoOf(r), nil
		},
		EdgeFn: func(ctx context.Context, id string) ([]domain.Relation, error) {
			return tx.Relations.ListByResource(ctx, id)
		},
	}
}

// correlationMark is one resource's verdict plus the linked cause.
type correlationMark struct {
	Role        domain.AlertCorrelationRole
	CauseAlert  string
	CauseResult string
}

// correlateAlerts computes and persists the correlation verdicts of the
// active error/warning alerts. Called at the END of the detect
// transaction (after the alert sync — the verdict reflects the
// post-sync alert set) — it returns the per-alert verdict map so the run
// envelope can annotate its created lines.
func correlateAlerts(ctx context.Context, tx *repo.Tx) (map[string]domain.AlertCorrelationRole, error) {
	refs, err := tx.Alerts.ListActiveAlertRefs(ctx)
	if err != nil {
		return nil, err
	}

	// The alerting resource set (error|warning only — info never
	// participates) and, per resource, the alert list in the refs'
	// deterministic (rule, series, alert) order.
	alerting := make(map[string][]domain.Alert)
	for _, ref := range refs {
		if ref.Severity != domain.SeverityError && ref.Severity != domain.SeverityWarning {
			continue
		}
		alerting[ref.ResourceID] = append(alerting[ref.ResourceID], ref.Alert)
	}
	if len(alerting) == 0 {
		return nil, nil
	}

	// Deterministic resource iteration (resource id asc).
	resIDs := make([]string, 0, len(alerting))
	for id := range alerting {
		resIDs = append(resIDs, id)
	}
	sort.Strings(resIDs)

	store := txGraphStore(tx)
	roles := make(map[string]domain.AlertCorrelationRole, len(refs))
	for _, resID := range resIDs {
		mark, err := verdictOf(ctx, store, resID, alerting)
		if err != nil {
			return nil, err
		}
		for _, a := range alerting[resID] {
			role := mark.Role
			correlatedAlert, correlatedResource := "", ""
			if role == domain.AlertCorrelationSecondary {
				correlatedAlert, correlatedResource = mark.CauseAlert, mark.CauseResult
			}
			if err := tx.Alerts.UpdateAlertCorrelation(ctx, a.AlertID, string(role), correlatedAlert, correlatedResource); err != nil {
				return nil, err
			}
			roles[a.AlertID] = role
		}
	}
	return roles, nil
}

// layerOfResource resolves a resource type's layer (the registry table —
// code, not documentation, §12-B5). An unknown type is a hard error: the
// correlation verdict depends on the layer DELTA, guessing is dishonest.
func layerOfResource(rt domain.ResourceType) (int, error) {
	return mapping.LayerOf(rt)
}

// verdictOf computes one resource's verdict: a cause alerting in the
// down-closure (strictly lower layer) → secondary (linked to the nearest
// cause's FIRST alert in (rule, series, alert) order); else an alerting
// dependent above (strictly higher layer) → primary; else unmarked.
func verdictOf(ctx context.Context, store topology.TraverseStore, resID string, alerting map[string][]domain.Alert) (correlationMark, error) {
	// The resource's own layer anchors both deltas.
	root, err := store.NodeInfo(ctx, resID)
	if err != nil {
		return correlationMark{}, err
	}
	rootLayer, err := layerOfResource(root.Type)
	if err != nil {
		return correlationMark{}, fmt.Errorf("detect: correlation layer of %s (%s): %w", resID, root.Type, err)
	}

	// Down-closure: the cause direction. A cause must sit strictly lower.
	down, err := topology.Traverse(ctx, store, resID, topology.TraverseParams{
		Direction: topology.DirectionDown,
		MaxDepth:  correlationDepth,
		MaxNodes:  correlationNodeBudget,
	})
	if err != nil {
		return correlationMark{}, err
	}
	var causeID string
	causeDepth, causeLayer := 1<<30, 1<<30
	for _, n := range down.Nodes {
		if n.ID == resID || n.Layer >= rootLayer {
			continue // self + lateral/higher neighbors are never causes
		}
		if _, ok := alerting[n.ID]; !ok {
			continue
		}
		if n.Depth < causeDepth || (n.Depth == causeDepth && n.Layer < causeLayer) ||
			(n.Depth == causeDepth && n.Layer == causeLayer && n.ID < causeID) {
			causeID, causeDepth, causeLayer = n.ID, n.Depth, n.Layer
		}
	}
	if causeID != "" {
		// The linked alert: the cause resource's first alert in the
		// refs' deterministic (rule, series, alert) order.
		cause := alerting[causeID][0]
		return correlationMark{
			Role:        domain.AlertCorrelationSecondary,
			CauseAlert:  cause.AlertID,
			CauseResult: causeID,
		}, nil
	}

	// Up-closure: the dependent direction. A dependent must sit strictly
	// higher. Existence is enough for the primary verdict — no link
	// (a primary points DOWN to its symptoms implicitly: they are the
	// secondary rows whose correlated ids point back here).
	up, err := topology.Traverse(ctx, store, resID, topology.TraverseParams{
		Direction: topology.DirectionUp,
		MaxDepth:  correlationDepth,
		MaxNodes:  correlationNodeBudget,
	})
	if err != nil {
		return correlationMark{}, err
	}
	for _, n := range up.Nodes {
		if n.ID == resID || n.Layer <= rootLayer {
			continue
		}
		if _, ok := alerting[n.ID]; ok {
			return correlationMark{Role: domain.AlertCorrelationPrimary}, nil
		}
	}
	return correlationMark{}, nil
}
