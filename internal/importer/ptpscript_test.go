package importer

// PTP configuration-script import channel tests (v0.5 W8-D): the script
// bytes flow through the SAME analyze → suggested-mapping → execute
// pipeline as xlsx workbooks (the workbook layer synthesizes the virtual
// sheet). Pinned: the golden import of the real-material shape (keys
// 6150|gei-1/1/0/7 and the 6130XG-S rows), the row accounting with script
// line numbers, the R7 skip-version rows on idempotent re-import, and the
// W006 rename proposal + apply journey for a renamed interface.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
)

// ptpScriptLines is the real-material shape (samples/ptp-config-script.txt,
// both model blocks with tab-suffix annotations and trailing tabs).
var ptpScriptLines = []string{
	"6150配置脚本\t",
	"config ter\t",
	"interface gei-1/1/0/7\t",
	"  ptp enable\t",
	"  ptp pdelay-req-interval 0\t",
	"  ptp unsymmetrical-offset 0 negative\t",
	"$\t",
	"ptp\t",
	"free-mode\t",
	"    port interface gei-1/1/0/7 portnum 6\t",
	"      enable\t",
	"      manual-port-state master\t",
	"    $\t",
	"end\t",
	"write\t",
	"\t",
	"show ptp port-state all\t查看接口状态，接BBU口master为正常。",
	"\t",
	"6130XG-S配置脚本\t",
	"config ter\t",
	"ptp clock-mode boundary\t",
	"ptp state-algorithm alternate-bmc\t",
	"ptp priority1 128\t",
	"ptp priority2 128\t",
	"ptp local-priority 128\t",
	"ptp clock-class 187\t",
	"ptp clock-accuracy 254\t",
	"ptp domain-delay-mechanism e2e\t",
	"ptp announce-interval -3\t",
	"ptp announce-timeout 6\t",
	"ptp domain-num 24\t",
	"ptp utc-trace auto holdover 24\t",
	"ssmclock usessm\t",
	"ssmclock ssm-mode exp-ssm\t",
	"ssmclock sec-quality 11\t",
	"ssmclock wtr-time 5\t",
	"ssmclock config\t",
	"  synchronization-clock line gei_1/1 2 unframe auto-ssm 11\t",
	"!\t",
	"!\t",
	"interface gei_1/7\t互联口",
	"ptp enable\t",
	"ptp pdelay-req-interval 0\t",
	"ptp packet-rcv-mode forward\t",
	"exit\t",
	"interface gei_1/7.31\t",
	"ptp enable\t",
	"  ptp sync-interval -1\t",
	"  ptp delay-req-interval -1\t",
	"  ptp network-protocol ipv4\t",
	"  ptp transfer-mode multicast\t",
	"  ptp vlan-priority 7\t",
	"exit\t",
	"interface gei_1/1\tBBU",
	"  switchport access vlan 4003\t",
	"  ptp enable\t",
	"  ptp pdelay-req-interval 0\t",
	"  ptp packet-rcv-mode forward\t",
	"!\t",
	"ptp add vlan4003\t",
	"!\t",
	"vlan 4003\t",
	"  l2_switch enable\t",
	"  ptp enable\t",
	"  ptp port-state master\t",
	"  ptp sync-interval -4\t",
	"  ptp delay-req-interval -4\t",
	"  ptp network-protocol ieee802\t",
	"  ptp transfer-mode multicast\t",
	"!\t",
	"end\t",
	"write\t",
	"\t",
	"show ptp port-state\t",
}

// writePTPScript writes the script (with the optional 6150 interface
// rename for the W006 journey) to dir/name and returns its path.
func writePTPScript(t *testing.T, dir, name string, rename bool) string {
	t.Helper()
	lines := append([]string(nil), ptpScriptLines...)
	if rename {
		for i, ln := range lines {
			if ln == "interface gei-1/1/0/7\t" {
				lines[i] = "interface gei-1/1/0/8\t"
			}
			if ln == "    port interface gei-1/1/0/7 portnum 6\t" {
				lines[i] = "    port interface gei-1/1/0/8 portnum 6\t"
			}
		}
	}
	data := []byte{}
	for _, ln := range lines {
		data = append(data, ln...)
		data = append(data, '\r', '\n')
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// importPTPScript analyzes + executes one script file through the SUGGESTED
// mapping (the zero-touch path a CLI/HTTP user gets after confirming the
// prefill) and returns the batch id + summary.
func importPTPScript(t *testing.T, svc *Service, path string) (string, Summary) {
	t.Helper()
	ctx := context.Background()
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze(%s): %v", path, err)
	}
	sum, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute(%s): %v", path, err)
	}
	return a.BatchID, sum
}

// ptpPortSpecFields is the spec field count asserted below.
func ptpPortSpecLen() int {
	spec, _ := mapping.SpecByType(domain.ResourceTypePTPPort)
	return len(spec.Fields)
}

// TestImportPTPScriptGolden pins the full golden journey of the real
// material: suggestion wiring, row accounting, composite keys, name
// override, attribute flattening, script line numbers in the row results
// and the source-ref raw maps.
func TestImportPTPScriptGolden(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	path := writePTPScript(t, t.TempDir(), "ptp-config-script.txt", false)

	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// The analysis mirrors the xlsx contract on the virtual sheet.
	if a.WorkbookName != "ptp-config-script.txt" || len(a.Sheets) != 1 {
		t.Fatalf("analysis = %q with %d sheets, want the script name with 1 sheet", a.WorkbookName, len(a.Sheets))
	}
	sa := a.Sheets[0]
	if sa.Sheet != "PTP配置" || sa.RowCount != 4 {
		t.Fatalf("sheet = %q with %d rows, want PTP配置 with 4", sa.Sheet, sa.RowCount)
	}
	if sa.SuggestedType != domain.ResourceTypePTPPort {
		t.Fatalf("suggestedType = %q, want ptp_port", sa.SuggestedType)
	}
	if len(sa.Columns) != ptpPortSpecLen() {
		t.Fatalf("columns = %d, want %d (one per ptp_port field)", len(sa.Columns), ptpPortSpecLen())
	}
	for _, c := range sa.Columns {
		if c.SuggestedField == "" {
			t.Errorf("column %q has no suggestion, want a field suggestion via the synonym ladder", c.Header)
		}
	}
	// The suggested mapping is a complete import prefill.
	sm := a.SuggestedMapping.Sheets[0]
	if sm.Action != mapping.ActionImport || sm.ResourceType != domain.ResourceTypePTPPort {
		t.Fatalf("suggested mapping sheet = %+v, want import/ptp_port", sm)
	}
	if len(sm.KeyColumns) != 2 || sm.KeyColumns[0] != "device_model" || sm.KeyColumns[1] != "port_name" {
		t.Fatalf("keyColumns = %v, want [device_model port_name]", sm.KeyColumns)
	}
	if len(sm.ColumnMapping) != ptpPortSpecLen() {
		t.Fatalf("columnMapping = %d entries, want %d", len(sm.ColumnMapping), ptpPortSpecLen())
	}

	b1, sum := importPTPScript(t, svc, path)
	if sum.RowsImported != 4 || sum.RowsSkipped != 0 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want 4 imported / 0 skipped / 0 failed", sum)
	}
	if len(sum.IssuesBySeverity) != 0 || sum.IssuesBySeverity[domain.SeverityWarning] != 0 {
		t.Fatalf("issues = %v, want none (no ref/enum/fallback rule can fire on this material)", sum.IssuesBySeverity)
	}

	// Composite keys in the join order of the spec (device_model first).
	keys := map[string]bool{}
	for _, want := range []string{"6150|gei-1/1/0/7", "6130XG-S|gei_1/7", "6130XG-S|gei_1/7.31", "6130XG-S|gei_1/1"} {
		res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, want)
		if err != nil {
			t.Fatalf("GetByTypeKey(%s): %v", want, err)
		}
		keys[want] = true
		// NameFieldOverride: the port's own name, not the model scope.
		if res.Name != want[strings.Index(want, "|")+1:] {
			t.Errorf("resource %s Name = %q, want the port_name component", want, res.Name)
		}
	}
	if len(keys) != 4 {
		t.Fatalf("distinct keys = %d, want 4", len(keys))
	}

	// The 6150 row: interface block + free-mode port block merged into ONE
	// resource; key fields stay out of attributes (port precedent).
	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, "6150|gei-1/1/0/7")
	if err != nil {
		t.Fatalf("GetByTypeKey: %v", err)
	}
	for k, v := range map[string]string{
		"portnum": "6", "port_state": "master", "free_mode": "true",
		"pdelay_req_interval": "0", "unsymmetrical_offset": "0 negative",
	} {
		if res.Attributes[k] != v {
			t.Errorf("6150 attribute %s = %q, want %q", k, res.Attributes[k], v)
		}
	}
	for _, k := range []string{"device_model", "port_name"} {
		if _, ok := res.Attributes[k]; ok {
			t.Errorf("key component %s must not land in attributes", k)
		}
	}
	// The 6130XG-S rows carry the flattened block globals.
	res, err = db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, "6130XG-S|gei_1/7")
	if err != nil {
		t.Fatalf("GetByTypeKey: %v", err)
	}
	for k, v := range map[string]string{
		"clock_mode": "boundary", "state_algorithm": "alternate-bmc", "priority1": "128",
		"domain_num": "24", "utc_trace": "auto holdover 24", "ssm_mode": "exp-ssm",
		"ssm_sec_quality": "11", "ssm_wtr_time": "5", "pdelay_req_interval": "0",
	} {
		if res.Attributes[k] != v {
			t.Errorf("6130XG-S gei_1/7 attribute %s = %q, want %q", k, res.Attributes[k], v)
		}
	}
	if res.Attributes["annotation"] != "互联口" {
		t.Errorf("annotation = %q, want 互联口 (the tab-suffix comment)", res.Attributes["annotation"])
	}

	// Row results keep the real script line numbers (3 / 41 / 46 / 54 in
	// this fixture) — the evidence axis of the script channel.
	rows, _, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: b1})
	if err != nil {
		t.Fatalf("list row results: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("row results = %d, want 4", len(rows))
	}
	wantNumbers := map[int]string{3: "gei-1/1/0/7", 41: "gei_1/7", 46: "gei_1/7.31", 54: "gei_1/1"}
	for _, rr := range rows {
		want, ok := wantNumbers[rr.RowNumber]
		if !ok {
			t.Errorf("unexpected row number %d in row results", rr.RowNumber)
			continue
		}
		if rr.Status != domain.RowStatusImported {
			t.Errorf("row %d status = %q, want imported", rr.RowNumber, rr.Status)
		}
		_ = want // the key assertions above already pin the key composition
	}

	// Source refs record the synthesized row with the Chinese headers.
	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, res.ID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("source refs = %d (err %v), want 1", len(refs), err)
	}
	if refs[0].Sheet != "PTP配置" || refs[0].RowNumber != 41 || refs[0].Workbook != "ptp-config-script.txt" {
		t.Errorf("source ref = %+v, want sheet PTP配置 row 41 of the script", refs[0])
	}
	if refs[0].Raw["接口名称"] != "gei_1/7" || refs[0].Raw["注释"] != "互联口" {
		t.Errorf("source ref raw = %v, want the synthesized header-keyed values", refs[0].Raw)
	}
}

// TestImportPTPScriptIdempotentVersionRows pins the R7 interplay: a pure
// re-import is 0/4/0 with E001 row semantics, and every resource gains its
// second source-ref version row (skip = the source confirmed the key).
func TestImportPTPScriptIdempotentVersionRows(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	path := writePTPScript(t, t.TempDir(), "ptp-config-script.txt", false)

	if _, sum1 := importPTPScript(t, svc, path); sum1.RowsImported != 4 {
		t.Fatalf("run 1 = %+v, want 4 imported", sum1)
	}
	_, sum2 := importPTPScript(t, svc, path)
	if sum2.RowsImported != 0 || sum2.RowsSkipped != 4 || sum2.RowsFailed != 0 {
		t.Fatalf("re-import summary = %+v, want 0/4/0", sum2)
	}

	for _, key := range []string{"6150|gei-1/1/0/7", "6130XG-S|gei_1/7", "6130XG-S|gei_1/7.31", "6130XG-S|gei_1/1"} {
		res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, key)
		if err != nil {
			t.Fatalf("GetByTypeKey(%s): %v", key, err)
		}
		refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, res.ID)
		if err != nil {
			t.Fatalf("ListByEntity(%s): %v", key, err)
		}
		if len(refs) != 2 {
			t.Fatalf("resource %s has %d source refs, want 2 (R7 skip versions)", key, len(refs))
		}
	}
	// Exactly 4 resources — no identity splits, no relation side effects.
	list, err := db.Resources.ListByType(ctx, domain.ResourceTypePTPPort)
	if err != nil || len(list) != 4 {
		t.Fatalf("ptp_port resources = %d (err %v), want 4", len(list), err)
	}
}

// TestImportPTPScriptRenameJourney pins the W006 interplay: a script
// revision renaming the 6150 interface imports the new key, fires W006 +
// one pending proposal (matched fields = the non-key equal attributes),
// and the renames apply merges the identity (old ID keeps the new key).
// The post-apply re-import is a pure skip — the closure of the journey.
func TestImportPTPScriptRenameJourney(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	dir := t.TempDir()
	original := writePTPScript(t, dir, "ptp-v1.txt", false)
	renamed := writePTPScript(t, dir, "ptp-v2.txt", true)

	if _, sum1 := importPTPScript(t, svc, original); sum1.RowsImported != 4 {
		t.Fatalf("run 1 = %+v, want 4 imported", sum1)
	}
	b2, sum2 := importPTPScript(t, svc, renamed)
	// The renamed 6150 interface imports as the new key; the three
	// 6130XG-S rows skip unchanged.
	if sum2.RowsImported != 1 || sum2.RowsSkipped != 3 || sum2.RowsFailed != 0 {
		t.Fatalf("run 2 = %+v, want 1 imported / 3 skipped / 0 failed", sum2)
	}
	if sum2.IssuesBySeverity[domain.SeverityWarning] != 1 {
		t.Fatalf("run 2 warnings = %d, want 1 (W006)", sum2.IssuesBySeverity[domain.SeverityWarning])
	}

	// One pending proposal: 6150|gei-1/1/0/7 → 6150|gei-1/1/0/8.
	rps, _, err := db.RenameProposals.List(ctx, repo.RenameFilter{Status: domain.RenameStatusPending})
	if err != nil {
		t.Fatalf("list pending renames: %v", err)
	}
	if len(rps) != 1 {
		t.Fatalf("pending renames = %d, want 1", len(rps))
	}
	rp := rps[0]
	if rp.OldKey != "6150|gei-1/1/0/7" || rp.NewKey != "6150|gei-1/1/0/8" || rp.BatchID != b2 {
		t.Fatalf("proposal = %+v, want 6150|gei-1/1/0/7 → 6150|gei-1/1/0/8 of batch %s", rp, b2)
	}
	if rp.Sheet != "PTP配置" || rp.RowNumber != 3 {
		t.Errorf("proposal sheet/row = %q/%d, want PTP配置/3 (the script line)", rp.Sheet, rp.RowNumber)
	}
	for _, f := range rp.MatchedFields {
		if f == "device_model" || f == "port_name" {
			t.Errorf("matched fields must exclude the key components, got %v", rp.MatchedFields)
		}
	}
	wantMatched := map[string]bool{"portnum": false, "port_state": false, "free_mode": false,
		"pdelay_req_interval": false, "unsymmetrical_offset": false}
	for _, f := range rp.MatchedFields {
		if _, ok := wantMatched[f]; ok {
			wantMatched[f] = true
		}
	}
	for f, seen := range wantMatched {
		if !seen {
			t.Errorf("matched fields miss %s (the equal non-key attributes), got %v", f, rp.MatchedFields)
		}
	}

	// Apply: the OLD resource keeps its identity and takes the new key;
	// the short-lived new resource is deleted.
	old, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, "6150|gei-1/1/0/7")
	if err != nil {
		t.Fatalf("GetByTypeKey(old): %v", err)
	}
	svcRenames := renames.New(db)
	if _, err := svcRenames.Apply(ctx, rp.ID); err != nil {
		t.Fatalf("renames apply: %v", err)
	}
	merged, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePTPPort, "6150|gei-1/1/0/8")
	if err != nil {
		t.Fatalf("GetByTypeKey(new): %v", err)
	}
	if merged.ID != old.ID {
		t.Errorf("apply kept id %s, want the old resource id %s (identity merge)", merged.ID, old.ID)
	}
	if merged.Attributes["portnum"] != "6" || merged.Attributes["port_state"] != "master" {
		t.Errorf("merged attributes = %v, want the 6150 values intact", merged.Attributes)
	}
	list, err := db.Resources.ListByType(ctx, domain.ResourceTypePTPPort)
	if err != nil || len(list) != 4 {
		t.Fatalf("ptp_port resources after apply = %d (err %v), want 4 (merge, not split)", len(list), err)
	}

	// Closure: re-importing the renamed script is a pure skip with zero
	// warnings (the key-component sync prevents a phantom W005).
	_, sum3 := importPTPScript(t, svc, renamed)
	if sum3.RowsImported != 0 || sum3.RowsSkipped != 4 || sum3.RowsFailed != 0 {
		t.Fatalf("run 3 = %+v, want 0/4/0", sum3)
	}
	if sum3.IssuesBySeverity[domain.SeverityWarning] != 0 {
		t.Fatalf("run 3 warnings = %d, want 0", sum3.IssuesBySeverity[domain.SeverityWarning])
	}
}
