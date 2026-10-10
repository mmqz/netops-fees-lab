package sim

import (
	"context"
	"fmt"
	"sort"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// PathHop is one device hop of a connectivity path.
type PathHop struct {
	DeviceID string `json:"deviceId"`
	Code     string `json:"code"`
}

// PathResult is the outcome of a connectivity query: the BFS path over
// live edges (link up + both interfaces up + both devices up), neighbors
// expanded in ascending device-code order, first-found path returned —
// deterministic for a given state.
type PathResult struct {
	Reachable  bool      `json:"reachable"`
	Hops       []PathHop `json:"hops"`
	PathLength int       `json:"pathLength"`
}

// Path answers whether device to is reachable from device from (device
// codes) over the live-edge graph and returns the deterministic BFS path.
// Unknown codes yield an error wrapping domain.ErrNotFound; identical codes
// are reachable with a single-node path (length 0). A non-reachable pair
// carries an EMPTY (never null) hops array and length 0 — the §7
// []-never-null normalization (AUD1-12; the frontend types declare hops as
// a non-nullable array).
func (s *Service) Path(ctx context.Context, workspaceID, fromDeviceCode, toDeviceCode string) (PathResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return PathResult{}, err
	}
	var result PathResult
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hops, err := bfsPath(state, fromDeviceCode, toDeviceCode)
		if err != nil {
			return err
		}
		result = hops
		return nil
	})
	if err != nil {
		return PathResult{}, err
	}
	return result, nil
}

// bfsPath runs the deterministic BFS over the state's live edges. Unknown
// device codes yield an error wrapping domain.ErrNotFound.
func bfsPath(state *simState, fromCode, toCode string) (PathResult, error) {
	from, okFrom := state.deviceByCode(fromCode)
	if !okFrom {
		return PathResult{}, fmt.Errorf("sim: path: unknown device code %q: %w", fromCode, domain.ErrNotFound)
	}
	to, okTo := state.deviceByCode(toCode)
	if !okTo {
		return PathResult{}, fmt.Errorf("sim: path: unknown device code %q: %w", toCode, domain.ErrNotFound)
	}
	if from.ID == to.ID {
		return PathResult{Reachable: true, Hops: []PathHop{{DeviceID: from.ID, Code: from.Code}}, PathLength: 0}, nil
	}

	// Build the live adjacency with code-sorted neighbor lists.
	adj := map[string][]string{}
	for _, l := range state.links {
		if !state.liveLink(l) {
			continue
		}
		a := state.ifaceDevice[l.AInterfaceID]
		z := state.ifaceDevice[l.ZInterfaceID]
		adj[a] = append(adj[a], z)
		adj[z] = append(adj[z], a)
	}
	for id := range adj {
		nbs := adj[id]
		sort.Slice(nbs, func(i, j int) bool {
			return state.deviceByID[nbs[i]].Code < state.deviceByID[nbs[j]].Code
		})
		adj[id] = nbs
	}

	parent := map[string]string{from.ID: ""}
	queue := []string{from.ID}
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if _, seen := parent[nb]; seen {
				continue
			}
			parent[nb] = cur
			if nb == to.ID {
				found = true
				break
			}
			queue = append(queue, nb)
		}
	}
	if !found {
		// hops renders as [] on the wire (never null) — see the Path doc.
		return PathResult{Reachable: false, Hops: []PathHop{}}, nil
	}

	// Walk the parent chain back from to, then reverse.
	path := make([]PathHop, 0, 8)
	for cur := to.ID; ; cur = parent[cur] {
		path = append(path, PathHop{DeviceID: cur, Code: state.deviceByID[cur].Code})
		if cur == from.ID {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return PathResult{Reachable: true, Hops: path, PathLength: len(path) - 1}, nil
}

// deviceByCode looks up a device by its (workspace-unique) code.
func (s *simState) deviceByCode(code string) (domain.VirtualDevice, bool) {
	for _, d := range s.devices {
		if d.Dev.Code == code {
			return d.Dev, true
		}
	}
	return domain.VirtualDevice{}, false
}
