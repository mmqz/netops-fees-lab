package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// RunRef references one run of a diff.
type RunRef struct {
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
	Score     int       `json:"score"`
	Counts    SevCounts `json:"counts"`
}

// PairRef is one unordered device pair of a connectivity diff (rendered
// with the lexicographically smaller code as FromDevice).
type PairRef struct {
	FromDevice string `json:"fromDevice"`
	ToDevice   string `json:"toDevice"`
}

// DiffResult is the before/after comparison of two check runs of one
// workspace. Findings carry the full stored rows (DetailJSON holds the
// matching detail data; the API layer marshals it).
type DiffResult struct {
	From               RunRef               `json:"from"`
	To                 RunRef               `json:"to"`
	FindingsAdded      []domain.CheckResult `json:"findingsAdded"`
	FindingsRemoved    []domain.CheckResult `json:"findingsRemoved"`
	ConnectivityLost   []PairRef            `json:"connectivityLost"`
	ConnectivityGained []PairRef            `json:"connectivityGained"`
	ScoreDelta         int                  `json:"scoreDelta"`
}

// Diff compares two check runs of the workspace: findings are matched by
// the per-check business keys (CHK-001 addr; CHK-002 the sorted
// aDevice/name pair; CHK-003 device code; CHK-004 sorted member codes;
// CHK-005 deviceCode/interfaceName — id-churn-proof), and connectivity is
// recomputed from each run's stored topology_json edge snapshot over the
// up links only. scoreDelta = to.score − from.score.
//
// The same run twice yields domain.ErrConflict (the API maps it to 400);
// unknown runs and runs of another workspace yield domain.ErrNotFound.
func (s *Service) Diff(ctx context.Context, workspaceID, fromRunID, toRunID string) (DiffResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return DiffResult{}, err
	}
	if fromRunID == toRunID {
		return DiffResult{}, fmt.Errorf("sim: diff needs two distinct runs, got %s twice: %w",
			fromRunID, domain.ErrConflict)
	}

	fromRun, fromResults, err := s.GetRun(ctx, workspaceID, fromRunID)
	if err != nil {
		return DiffResult{}, err
	}
	toRun, toResults, err := s.GetRun(ctx, workspaceID, toRunID)
	if err != nil {
		return DiffResult{}, err
	}

	fromFindings := findingsOf(fromResults)
	toFindings := findingsOf(toResults)

	fromKeys := findingKeys(fromFindings)
	toKeys := findingKeys(toFindings)

	added := make([]domain.CheckResult, 0)
	for _, f := range toFindings {
		if !fromKeys[keyOfFinding(f)] {
			added = append(added, f)
		}
	}
	removed := make([]domain.CheckResult, 0)
	for _, f := range fromFindings {
		if !toKeys[keyOfFinding(f)] {
			removed = append(removed, f)
		}
	}

	fromPairs, err := snapshotReachablePairs(fromRun.TopologyJSON)
	if err != nil {
		return DiffResult{}, fmt.Errorf("sim: diff from-run %s: %w", fromRunID, err)
	}
	toPairs, err := snapshotReachablePairs(toRun.TopologyJSON)
	if err != nil {
		return DiffResult{}, fmt.Errorf("sim: diff to-run %s: %w", toRunID, err)
	}

	var lost, gained []PairRef
	for key := range fromPairs {
		if !toPairs[key] {
			lost = append(lost, pairOfKey(key))
		}
	}
	for key := range toPairs {
		if !fromPairs[key] {
			gained = append(gained, pairOfKey(key))
		}
	}
	sort.Slice(lost, func(i, j int) bool { return lessPair(lost[i], lost[j]) })
	sort.Slice(gained, func(i, j int) bool { return lessPair(gained[i], gained[j]) })

	return DiffResult{
		From:               runRefOf(fromRun, fromResults),
		To:                 runRefOf(toRun, toResults),
		FindingsAdded:      added,
		FindingsRemoved:    removed,
		ConnectivityLost:   lost,
		ConnectivityGained: gained,
		ScoreDelta:         toRun.Score - fromRun.Score,
	}, nil
}

// findingsOf filters a run's result rows down to the findings (passed=0),
// preserving the stored canonical order.
func findingsOf(results []domain.CheckResult) []domain.CheckResult {
	out := make([]domain.CheckResult, 0, len(results))
	for _, r := range results {
		if !r.Passed {
			out = append(out, r)
		}
	}
	return out
}

// findingKeys maps the matching key of every finding to true.
func findingKeys(findings []domain.CheckResult) map[string]bool {
	keys := make(map[string]bool, len(findings))
	for _, f := range findings {
		keys[keyOfFinding(f)] = true
	}
	return keys
}

// keyOfFinding builds the per-check matching key of one finding: the
// check code plus the business key parsed from its detail JSON.
func keyOfFinding(f domain.CheckResult) string {
	return f.CheckCode + "\x00" + detailKey(f.CheckCode, f.DetailJSON)
}

// detailKey extracts the business key of a finding's detail per check
// code; unparsable details fall back to the raw JSON (deterministic).
func detailKey(checkCode, detailJSON string) string {
	var d struct {
		Addr       string `json:"addr"`
		AInterface struct {
			DeviceCode string `json:"deviceCode"`
			Name       string `json:"name"`
		} `json:"aInterface"`
		BInterface struct {
			DeviceCode string `json:"deviceCode"`
			Name       string `json:"name"`
		} `json:"bInterface"`
		Code       string   `json:"code"`
		Members    []string `json:"members"`
		DeviceCode string   `json:"deviceCode"`
		Name       string   `json:"name"`
	}
	if err := json.Unmarshal([]byte(detailJSON), &d); err != nil {
		return detailJSON
	}
	switch checkCode {
	case domain.CheckDuplicateHostAddress:
		return d.Addr
	case domain.CheckSubnetOverlap:
		a := d.AInterface.DeviceCode + "/" + d.AInterface.Name
		z := d.BInterface.DeviceCode + "/" + d.BInterface.Name
		if z < a {
			a, z = z, a
		}
		return a + "\x00" + z
	case domain.CheckIsolatedDevice:
		return d.Code
	case domain.CheckPartialPartition:
		members := append([]string(nil), d.Members...)
		sort.Strings(members)
		return strings.Join(members, "\x00")
	case domain.CheckUnconfiguredInterface:
		return d.DeviceCode + "/" + d.Name
	default:
		return detailJSON
	}
}

// snapshotReachablePairs recomputes the reachable device pairs of one run
// from its stored topology_json edge snapshot: the device universe is
// every code appearing as a link endpoint (any link, up or down); two
// devices form a pair when BFS over the snapshot's LIVE edges connects
// them. Pair keys are "a\x00b" with a < b.
//
// Live is the FULL §5 connectivity predicate (AUD1-2): link up AND both
// interfaces up AND both devices up — the same predicate Path/CHK-003/CHK-004
// apply, so a set_device_status/set_interface_status fault now surfaces in
// connectivityLost exactly like a link fault. Legacy snapshot rows (v0.2.0
// six-field format) carry no endpoint statuses; a missing status reads as
// up, reproducing the historical link-only behavior — diffs between legacy
// runs are unchanged, only new runs get the full predicate.
func snapshotReachablePairs(topoJSON string) (map[string]bool, error) {
	links, err := parseTopoLinks(topoJSON)
	if err != nil {
		return nil, err
	}
	universe := map[string]bool{}
	adj := map[string]map[string]bool{}
	for _, l := range links {
		universe[l.ADevice] = true
		universe[l.ZDevice] = true
		if !l.live() {
			continue
		}
		if adj[l.ADevice] == nil {
			adj[l.ADevice] = map[string]bool{}
		}
		if adj[l.ZDevice] == nil {
			adj[l.ZDevice] = map[string]bool{}
		}
		adj[l.ADevice][l.ZDevice] = true
		adj[l.ZDevice][l.ADevice] = true
	}

	// Connected components over live edges (deterministic; the pair set is
	// order-independent anyway).
	component := map[string]int{}
	cid := 0
	for _, code := range sortedKeysOf(universe) {
		if _, done := component[code]; done {
			continue
		}
		cid++
		queue := []string{code}
		component[code] = cid
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for nb := range adj[cur] {
				if _, done := component[nb]; !done {
					component[nb] = cid
					queue = append(queue, nb)
				}
			}
		}
	}

	byComponent := map[int][]string{}
	for code, c := range component {
		byComponent[c] = append(byComponent[c], code)
	}
	pairs := map[string]bool{}
	for _, members := range byComponent {
		sort.Strings(members)
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				pairs[members[i]+"\x00"+members[j]] = true
			}
		}
	}
	return pairs, nil
}

// live reports whether a snapshot edge counts as a live connection under
// the full §5 connectivity predicate: link up AND both interfaces up AND
// both devices up. The four endpoint statuses are nil on legacy (v0.2.0)
// snapshot rows — a missing value is treated as up.
func (l topoLink) live() bool {
	return l.Status == "up" &&
		upOrMissing(l.ADeviceStatus) && upOrMissing(l.AInterfaceStatus) &&
		upOrMissing(l.ZDeviceStatus) && upOrMissing(l.ZInterfaceStatus)
}

// upOrMissing reports whether a nullable endpoint status is up (nil = legacy
// row / unresolvable ⇒ up).
func upOrMissing(s *string) bool {
	return s == nil || *s == "up"
}

// pairOfKey renders a "a\x00b" pair key as a PairRef.
func pairOfKey(key string) PairRef {
	a, z, _ := strings.Cut(key, "\x00")
	return PairRef{FromDevice: a, ToDevice: z}
}

// lessPair orders two PairRefs by (fromDevice, toDevice).
func lessPair(a, b PairRef) bool {
	if a.FromDevice != b.FromDevice {
		return a.FromDevice < b.FromDevice
	}
	return a.ToDevice < b.ToDevice
}

// runRefOf builds the RunRef of one run: score plus finding counts.
func runRefOf(run domain.CheckRun, results []domain.CheckResult) RunRef {
	ref := RunRef{RunID: run.ID, StartedAt: run.StartedAt, Score: run.Score}
	for _, r := range results {
		if r.Passed {
			continue
		}
		switch r.Severity {
		case domain.SeverityError:
			ref.Counts.Error++
		case domain.SeverityWarning:
			ref.Counts.Warning++
		case domain.SeverityInfo:
			ref.Counts.Info++
		}
	}
	return ref
}
