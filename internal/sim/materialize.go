package sim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// statusInNet is the imported device status that materializes as an up
// virtual device (PROJECT_BRIEF_P2 §6; the Phase 1 enum lives in the
// mapping package — sim keeps its own pinned constant to stay
// layer-clean). Everything else (停用/在建/退网/empty/unknown) maps to down.
//
// BOUNDARY (issue #24 / data-model-v1.md §12-B3, ADR-0011): the lab is a
// DEVICE-LEVEL connectivity sandbox — it materializes device + circuit
// only, from the LEDGER status (台账 status), deliberately NOT from the
// runtime state (run_state, W22). The unified graph (resources/relations,
// traverse/impact endpoints) is the full resource model for analysis.
// One-liner for AI agents: analysis (impact/root-cause/business) uses the
// unified graph; simulation operations (disconnect/restore drills) use
// the lab. This boundary is deliberate — do not "fix" the ledger-status
// mapping here when run_state lands.
const statusInNet = "在网"

// MaterializeResult is the outcome of a successful Materialize.
type MaterializeResult struct {
	WorkspaceID    string   `json:"workspaceId"`
	Name           string   `json:"name"`
	SourceBatchIDs []string `json:"sourceBatchIds"`
	Counts         struct {
		Devices    int `json:"devices"`
		Interfaces int `json:"interfaces"`
		Links      int `json:"links"`
	} `json:"counts"`
	WorkspaceHash string `json:"workspaceHash"`
}

// Materialize creates a workspace from imported resources
// (PROJECT_BRIEF_P2 §6): every device resource of the selected batches
// becomes a virtual device (status 在网 → up, anything else → down), and
// every circuit resource whose a_end/z_end relations both resolve to
// workspace devices becomes a link with deterministic eth<n> port
// allocation (circuits in ascending natural_key order, lowest free port
// index per endpoint device). Self-referencing circuits are skipped
// (mirrors the Phase 1 W003 row). Circuit status is ignored — links start
// up.
//
// batchIDs empty selects ALL completed batches (in import order). A
// duplicate name yields domain.ErrConflict, an unknown batch id
// domain.ErrNotFound, and a selection without any device resource
// domain.ErrNoSourceResources. Everything is written in ONE transaction —
// materialization is NOT a changeset; lineage lives in
// workspaces.source_json.
func (s *Service) Materialize(ctx context.Context, name, description string, batchIDs []string) (MaterializeResult, error) {
	if name == "" {
		return MaterializeResult{}, fmt.Errorf("sim: materialize: name must not be empty: %w", domain.ErrInvalidOp)
	}
	// Duplicate name pre-check; the UNIQUE constraint stays the final
	// authority inside the transaction.
	if _, err := s.db.Workspaces.GetByName(ctx, name); err == nil {
		return MaterializeResult{}, fmt.Errorf("sim: materialize %q: %w", name, domain.ErrConflict)
	} else if !isNotFound(err) {
		return MaterializeResult{}, fmt.Errorf("sim: materialize %q: %w", name, err)
	}

	var result MaterializeResult
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		// Resolve the batch scope. Explicit ids must all exist; the
		// default scope is every completed batch in import order
		// (Batches.List returns newest-first; reversed → ascending import
		// order, which is also the deterministic source_batch_ids order).
		scope := batchIDs
		if len(scope) == 0 {
			batches, _, err := tx.Batches.List(ctx, -1, 0)
			if err != nil {
				return fmt.Errorf("sim: materialize: list batches: %w", err)
			}
			scope = make([]string, 0, len(batches))
			for i := len(batches) - 1; i >= 0; i-- {
				if batches[i].Status == domain.BatchStatusCompleted {
					scope = append(scope, batches[i].ID)
				}
			}
		} else {
			for _, id := range scope {
				if _, err := tx.Batches.Get(ctx, id); err != nil {
					return fmt.Errorf("sim: materialize: batch %s: %w", id, err)
				}
			}
		}

		// Devices: every device resource whose first_batch_id is in scope
		// (Search orders by natural_key = device_code — the deterministic
		// creation order).
		devResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
		if err != nil {
			return fmt.Errorf("sim: materialize: list device resources: %w", err)
		}
		scopeSet := make(map[string]bool, len(scope))
		for _, id := range scope {
			scopeSet[id] = true
		}
		var devices []domain.Resource
		for _, res := range devResources {
			if scopeSet[res.FirstBatchID] {
				devices = append(devices, res)
			}
		}
		if len(devices) == 0 {
			return fmt.Errorf("sim: materialize %q: %w", name, domain.ErrNoSourceResources)
		}

		// Workspace row (uuid id per PROJECT_BRIEF_P2 §3).
		workspaceID := uuid.NewString()
		sourceJSON, err := json.Marshal(domain.WorkspaceSource{BatchIDs: scope})
		if err != nil {
			return fmt.Errorf("sim: materialize: marshal source: %w", err)
		}
		now := s.clock()
		if err := tx.Workspaces.Insert(ctx, domain.Workspace{
			ID: workspaceID, Name: name, Description: description,
			Status: domain.WorkspaceStatusActive, SourceJSON: string(sourceJSON),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("sim: materialize %q: %w", name, err)
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("workspace"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after workspace): %w", err)
			}
		}

		// Virtual devices: code = natural key (the importer stores the
		// device_code as the resource natural key — §6's
		// "attributes.device_code" maps to it), name = device_name
		// attribute, status 在网 → up else down (empty/unknown ⇒ down).
		deviceByResource := make(map[string]domain.VirtualDevice, len(devices))
		for _, res := range devices {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
			if err != nil {
				return fmt.Errorf("sim: materialize: allocate device id: %w", err)
			}
			dev := domain.VirtualDevice{
				ID:          domain.FormatSimID(domain.SeqDevice, n),
				WorkspaceID: workspaceID,
				ResourceID:  res.ID,
				Code:        res.NaturalKey,
				Name:        res.Attributes["device_name"],
				Status:      deviceStatusOfResource(res),
				CreatedAt:   now, UpdatedAt: now,
			}
			// Optional geographic position from the resource
			// attributes (plain decimal strings written by the
			// importer's geo split). Invalid values are ignored
			// — import already validated them; this is defensive
			// only. Geo is presentation metadata and never
			// enters the state hash.
			dev.GeoLat, dev.GeoLng = geoOfResource(res)
			if dev.Name == "" {
				dev.Name = res.NaturalKey // defensive: device_name is a required field
			}
			if err := tx.VirtualDevices.Insert(ctx, dev); err != nil {
				return fmt.Errorf("sim: materialize: insert device %s: %w", dev.Code, err)
			}
			deviceByResource[res.ID] = dev
			result.Counts.Devices++
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("devices"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after devices): %w", err)
			}
		}

		// Links: circuits in ascending natural_key order; the a_end/z_end
		// relations must both resolve to workspace devices; the lowest
		// free port index of each endpoint device becomes eth<idx>
		// (0-based, per device). Self-referencing circuits are skipped.
		circuitResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeCircuit})
		if err != nil {
			return fmt.Errorf("sim: materialize: list circuit resources: %w", err)
		}
		nextPort := make(map[string]int) // device id → next free port index
		for _, res := range circuitResources {
			if !scopeSet[res.FirstBatchID] {
				continue
			}
			aEnd, zEnd, ok, err := circuitEndpoints(ctx, tx, res.ID)
			if err != nil {
				// A storage error must not silently drop the circuit's
				// link (AUD1-5): materialize is atomic-complete or fails.
				return fmt.Errorf("sim: materialize: endpoints of circuit %s: %w", res.NaturalKey, err)
			}
			if !ok {
				continue // missing endpoint relation(s)
			}
			devA, okA := deviceByResource[aEnd]
			devZ, okZ := deviceByResource[zEnd]
			if !okA || !okZ {
				continue // endpoint not a workspace device (out of scope)
			}
			if devA.ID == devZ.ID {
				continue // self-referencing circuit (Phase 1 W003) — skipped
			}
			aIfaceID, err := s.materializePort(ctx, tx, workspaceID, devA.ID, nextPort, now)
			if err != nil {
				return err
			}
			zIfaceID, err := s.materializePort(ctx, tx, workspaceID, devZ.ID, nextPort, now)
			if err != nil {
				return err
			}
			n, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
			if err != nil {
				return fmt.Errorf("sim: materialize: allocate link id: %w", err)
			}
			if err := tx.SimLinks.Insert(ctx, domain.SimLink{
				ID: domain.FormatSimID(domain.SeqLink, n), WorkspaceID: workspaceID,
				AInterfaceID: aIfaceID, ZInterfaceID: zIfaceID,
				Status: domain.EntityStatusUp, Label: res.NaturalKey, ResourceID: res.ID,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("sim: materialize: insert link %s: %w", res.NaturalKey, err)
			}
			result.Counts.Links++
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("links"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after links): %w", err)
			}
		}

		result.WorkspaceID = workspaceID
		result.Name = name
		result.SourceBatchIDs = scope
		result.Counts.Interfaces = 2 * result.Counts.Links

		// Hash from the same typed state loader as every other read (one
		// source of truth), still inside the transaction.
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err := state.hash()
		if err != nil {
			return err
		}
		result.WorkspaceHash = hash
		return nil
	})
	if err != nil {
		return MaterializeResult{}, fmt.Errorf("sim: materialize: %w", err)
	}
	return result, nil
}

// materializePort allocates eth<next free index> on the device (the
// deterministic lowest-free-port rule of §6: ports are handed out in
// circuit order, so the map simply counts up per device) and inserts the
// interface (up, unconfigured).
func (s *Service) materializePort(ctx context.Context, tx *repo.Tx, workspaceID, deviceID string, nextPort map[string]int, now time.Time) (string, error) {
	idx := nextPort[deviceID]
	nextPort[deviceID] = idx + 1
	n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
	if err != nil {
		return "", fmt.Errorf("sim: materialize: allocate interface id: %w", err)
	}
	ifaceID := domain.FormatSimID(domain.SeqInterface, n)
	if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
		ID: ifaceID, WorkspaceID: workspaceID, DeviceID: deviceID,
		Name: fmt.Sprintf("eth%d", idx), Status: domain.EntityStatusUp,
		IPv4Addr: "", IPv4Prefix: -1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("sim: materialize: insert interface %s: %w", ifaceID, err)
	}
	return ifaceID, nil
}

// circuitEndpoints resolves a circuit resource's a_end and z_end relations
// to device resource ids. ok is false when an endpoint relation is missing;
// err is non-nil when the relation read itself failed (propagated — never
// masked as a missing endpoint, AUD1-5).
func circuitEndpoints(ctx context.Context, tx *repo.Tx, circuitID string) (aEnd, zEnd string, ok bool, err error) {
	rels, err := tx.Relations.ListByResource(ctx, circuitID)
	if err != nil {
		return "", "", false, fmt.Errorf("list relations of circuit %s: %w", circuitID, err)
	}
	for _, rel := range rels {
		switch rel.Type {
		case domain.RelationTypeAEnd:
			aEnd = rel.ToResourceID
		case domain.RelationTypeZEnd:
			zEnd = rel.ToResourceID
		}
	}
	if aEnd == "" || zEnd == "" {
		return "", "", false, nil
	}
	return aEnd, zEnd, true, nil
}

// deviceStatusOfResource maps the resource status onto the entity status:
// 在网 → up; empty/退网/停用/在建/anything else → down (§6).
func deviceStatusOfResource(res domain.Resource) domain.EntityStatus {
	if res.Status == statusInNet {
		return domain.EntityStatusUp
	}
	return domain.EntityStatusDown
}

// geoOfResource parses the optional geo_lat/geo_lng device attributes into
// the virtual device position pointers. Each coordinate is adopted only
// when it parses AND is in range (lat [-90,90], lng [-180,180]); absent,
// unparsable or out-of-range values yield nil (import already validated the
// columns — ignoring here is defensive, never a hard failure). The two
// coordinates are independent: a partial pair still carries the valid half.
func geoOfResource(res domain.Resource) (lat, lng *float64) {
	lat = parseGeoAttr(res.Attributes["geo_lat"], -90, 90)
	lng = parseGeoAttr(res.Attributes["geo_lng"], -180, 180)
	return lat, lng
}

// parseGeoAttr parses one geo attribute as a float within [min, max]; nil
// when absent, unparsable (NaN/Inf included) or out of range.
func parseGeoAttr(v string, min, max float64) *float64 {
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
		return nil
	}
	return &f
}

// isNotFound reports whether err wraps domain.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound)
}
