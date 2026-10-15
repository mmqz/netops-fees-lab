package sim

// DET-P2 — determinism: two fresh databases given the same import, the
// same materialization and the same op script (including one deliberately
// failing ApplyOps) produce identical ids, topology JSON, workspace hash,
// changesets and check runs modulo timestamps (and the random workspace
// uuid embedded in finding entity ids).

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// opCanon is the canonical (timestamp-free) form of one changeset op row.
type opCanon struct {
	Seq         int    `json:"seq"`
	OpCode      string `json:"opCode"`
	OpJSON      string `json:"opJson"`
	InverseJSON string `json:"inverseJson"`
	Result      string `json:"result"`
}

// csCanon is the canonical form of one changeset with its op rows.
type csCanon struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Trigger     string    `json:"trigger"`
	Status      string    `json:"status"`
	OpsApplied  int       `json:"opsApplied"`
	Ops         []opCanon `json:"ops"`
}

// resCanon is the canonical form of one check result row (ids kept — they
// are deterministic; the workspace uuid of entity ids is normalized by
// the caller).
type resCanon struct {
	Seq        int    `json:"seq"`
	CheckCode  string `json:"checkCode"`
	Severity   string `json:"severity"`
	Passed     bool   `json:"passed"`
	EntityKind string `json:"entityKind"`
	EntityID   string `json:"entityId"`
	Message    string `json:"message"`
	DetailJSON string `json:"detailJson"`
}

// runCanon is the canonical form of one check run.
type runCanon struct {
	ID           string     `json:"id"`
	ChangesetID  string     `json:"changesetId"`
	Trigger      string     `json:"trigger"`
	RuleVersion  string     `json:"ruleVersion"`
	Score        int        `json:"score"`
	TopologyJSON string     `json:"topologyJson"`
	Results      []resCanon `json:"results"`
}

// determinismArtifacts bundles everything two runs are compared on.
type determinismArtifacts struct {
	TopologyJSON string
	Hash         string
	Changesets   []csCanon
	Runs         []runCanon
	Path         PathResult
}

// runDeterminismScript drives one database through the fixed script:
// import → materialize → 4 changesets (one deliberately failing) →
// manual check run → path query.
func runDeterminismScript(t *testing.T) (*Service, determinismArtifacts) {
	t.Helper()
	svc, db := newEngine(t)
	batches := importWorkbooks(t, db)
	ctx := context.Background()
	res, err := svc.Materialize(ctx, "lab", "determinism script", batches)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	ws := res.WorkspaceID

	// cs-1: a status flip with auto-checks.
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"},
	}, "step 1", true); err != nil {
		t.Fatalf("ApplyOps(step 1): %v", err)
	}
	// cs-2: deliberately failing (records a failed changeset).
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "down"},
		{Op: domain.OpSetLinkStatus, LinkID: "ln-99", Status: "down"},
	}, "step 2 (fails)", true); err == nil {
		t.Fatalf("step 2 must fail")
	}
	// cs-3: address configs (including a duplicate) with auto-checks.
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		configureIface("if-1", "10.0.0.1", 24),
		configureIface("if-4", "10.0.0.1", 24),
	}, "step 3", true); err != nil {
		t.Fatalf("ApplyOps(step 3): %v", err)
	}
	// run-3: a manual check run.
	if _, err := svc.RunChecks(ctx, ws, domain.CheckTriggerManual); err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	// cs-4: creates + deletes (exercises the inverse machinery) without
	// checks.
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpCreateDevice, Code: "NEW-9", Name: "n"},
		{Op: domain.OpCreateInterface, DeviceID: "vd-28", Name: "eth0"},
		{Op: domain.OpDeleteInterface, InterfaceID: "if-41"},
		{Op: domain.OpDeleteDevice, DeviceID: "vd-28"},
	}, "step 4", false); err != nil {
		t.Fatalf("ApplyOps(step 4): %v", err)
	}

	topo := mustTopology(t, svc, ws)
	topoJSON, err := json.Marshal(topo)
	if err != nil {
		t.Fatalf("marshal topo: %v", err)
	}

	// Changesets canonical form (strip timestamps only; ids must match).
	var csCanons []csCanon
	changesets, _, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil {
		t.Fatalf("list changesets: %v", err)
	}
	for _, cs := range changesets {
		rows, err := db.ChangeSets.GetOps(ctx, cs.ID)
		if err != nil {
			t.Fatalf("GetOps(%s): %v", cs.ID, err)
		}
		c := csCanon{ID: cs.ID, Description: cs.Description, Trigger: cs.Trigger.String(),
			Status: cs.Status.String(), OpsApplied: cs.OpsApplied}
		for _, row := range rows {
			c.Ops = append(c.Ops, opCanon{Seq: row.Seq, OpCode: row.OpCode, OpJSON: row.OpJSON,
				InverseJSON: row.InverseJSON, Result: row.OpResult.String()})
		}
		csCanons = append(csCanons, c)
	}

	// Check runs canonical form (strip timestamps/duration; normalize the
	// workspace uuid out of finding entity ids).
	var runCanons []runCanon
	runs, _, err := db.CheckRuns.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	for _, run := range runs {
		results, err := db.CheckRuns.GetResults(ctx, run.ID)
		if err != nil {
			t.Fatalf("GetResults(%s): %v", run.ID, err)
		}
		c := runCanon{ID: run.ID, ChangesetID: run.ChangesetID, Trigger: run.Trigger.String(),
			RuleVersion: run.RuleVersion, Score: run.Score, TopologyJSON: run.TopologyJSON}
		for _, r := range results {
			c.Results = append(c.Results, resCanon{Seq: r.Seq, CheckCode: r.CheckCode,
				Severity: r.Severity.String(), Passed: r.Passed, EntityKind: r.EntityKind,
				EntityID: r.EntityID, Message: r.Message, DetailJSON: r.DetailJSON})
		}
		runCanons = append(runCanons, c)
	}

	path, err := svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	return svc, determinismArtifacts{
		TopologyJSON: string(topoJSON),
		Hash:         mustHash(t, svc, ws),
		Changesets:   csCanons,
		Runs:         runCanons,
		Path:         path,
	}
}

// normalizeUUID replaces every workspace uuid occurrence with a
// placeholder so two databases can be compared (only the workspace id is
// random; all sim ids come from the global sequences).
func normalizeArtifacts(a determinismArtifacts, workspaceID string) determinismArtifacts {
	norm := func(s string) string {
		return replaceResourceUUIDs(replaceUUID(s, workspaceID))
	}
	a.TopologyJSON = norm(a.TopologyJSON)
	// a.Hash needs no normalization: the hash covers business fields only
	// — no uuid inside.
	for i := range a.Changesets {
		for j := range a.Changesets[i].Ops {
			a.Changesets[i].Ops[j].OpJSON = norm(a.Changesets[i].Ops[j].OpJSON)
			a.Changesets[i].Ops[j].InverseJSON = norm(a.Changesets[i].Ops[j].InverseJSON)
		}
	}
	for i := range a.Runs {
		// a.Runs[i].TopologyJSON needs no normalization: it carries device
		// codes only.
		for j := range a.Runs[i].Results {
			a.Runs[i].Results[j].EntityID = norm(a.Runs[i].Results[j].EntityID)
		}
	}
	return a
}

// replaceUUID replaces occurrences of id in s with "<ws>".
func replaceUUID(s, id string) string {
	if id == "" {
		return s
	}
	out := ""
	for {
		i := indexOfSubstring(s, id)
		if i < 0 {
			return out + s
		}
		out += s[:i] + "<ws>"
		s = s[i+len(id):]
	}
}

// replaceResourceUUIDs canonicalizes imported resource uuids to a fixed
// placeholder: the layer overlay payload (issue #51) carries the lineage
// resource ids, which are freshly generated uuids on every import —
// structural determinism compares shapes, not import identities. Two id
// shapes occur: the importer's res_<uuid> and the test fixtures' bare
// uuid inside a resourceId JSON field.
func replaceResourceUUIDs(s string) string {
	out := resourceUUIDPattern.ReplaceAllString(s, "res_<id>")
	out = resourceIdUUIDPattern.ReplaceAllString(out, `"resourceId":"<id>"`)
	return out
}

// resourceUUIDPattern matches res_ followed by a 36-char uuid.
var resourceUUIDPattern = regexp.MustCompile(`res_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// resourceIdUUIDPattern matches a bare uuid inside a resourceId field.
var resourceIdUUIDPattern = regexp.MustCompile(`"resourceId":"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"`)

// indexOfSubstring finds substr in s (testing helper).
func indexOfSubstring(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestSimulationDeterminism is DET-P2.
func TestSimulationDeterminism(t *testing.T) {
	svc1, art1 := runDeterminismScript(t)
	ws1 := mustWorkspaceID(t, svc1)
	svc2, art2 := runDeterminismScript(t)
	ws2 := mustWorkspaceID(t, svc2)

	if ws1 == ws2 {
		t.Fatalf("the two databases must have distinct workspace uuids")
	}
	norm1 := normalizeArtifacts(art1, ws1)
	norm2 := normalizeArtifacts(art2, ws2)

	if norm1.Hash != norm2.Hash {
		t.Fatalf("workspace hashes differ: %s vs %s", norm1.Hash, norm2.Hash)
	}
	if norm1.TopologyJSON != norm2.TopologyJSON {
		t.Fatalf("topology JSON differs across fresh databases (modulo workspace id)")
	}
	if len(norm1.Changesets) != 4 {
		t.Fatalf("script produced %d changesets, want 4", len(norm1.Changesets))
	}
	if len(norm1.Runs) != 3 {
		t.Fatalf("script produced %d check runs, want 3 (2 auto + 1 manual)", len(norm1.Runs))
	}
	if !jsonEqual(t, norm1.Changesets, norm2.Changesets) {
		t.Fatalf("changesets canonical form differs (ids must be identical)")
	}
	if !jsonEqual(t, norm1.Runs, norm2.Runs) {
		t.Fatalf("check-run canonical form differs (modulo ids/timestamps)")
	}
	if norm1.Path.Reachable != norm2.Path.Reachable || norm1.Path.PathLength != norm2.Path.PathLength {
		t.Fatalf("path results differ: %+v vs %+v", norm1.Path, norm2.Path)
	}
	if len(norm1.Path.Hops) != len(norm2.Path.Hops) {
		t.Fatalf("path hops differ")
	}
	for i := range norm1.Path.Hops {
		if norm1.Path.Hops[i] != norm2.Path.Hops[i] {
			t.Fatalf("path hop %d differs: %+v vs %+v", i, norm1.Path.Hops[i], norm2.Path.Hops[i])
		}
	}
}

// jsonEqual compares two values through their canonical JSON.
func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(ja) == string(jb)
}

// mustWorkspaceID returns the id of the "lab" workspace.
func mustWorkspaceID(t *testing.T, svc *Service) string {
	t.Helper()
	ws, err := svc.db.Workspaces.GetByName(context.Background(), "lab")
	if err != nil {
		t.Fatalf("GetByName(lab): %v", err)
	}
	return ws.ID
}
