package mcpserver

// ruletools_test.go — issue #2's MCP acceptance: the AI creation channel
// answers the six-tuple verbatim with enabled=false (观察态), the 'created'
// audit row names actor=mcp, a repeated call with the same name answers
// the UNIQUE refusal, and the --read-only variant EXCLUDES the tool from
// its registry (the human-channel doctrine: the read-only face cannot
// even see the write tool).

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// toolResultText extracts the first text part of a tool result (the SDK
// packs the error vocabulary there).
func toolResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if txt, ok := c.(*mcp.TextContent); ok {
			return txt.Text
		}
	}
	t.Fatalf("no text content in the result: %+v", res.Content)
	return ""
}

const ruleCreateArgs = `{"name":"电费单月超阈值","feeType":"electricity","metric":"amount",
"aggregation":"sum","dimension":"site_month","comparator":"gt",
"threshold":500000,"windowMonths":1,"severity":"high","description":"单站单月电费超 5000 元"}`

func TestMCPRuleCreateChannel(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	session := startSession(t, db)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: feeRulesCreateName,
		Arguments: map[string]any{
			"name": "电费单月超阈值", "feeType": "electricity", "metric": "amount",
			"aggregation": "sum", "dimension": "site_month", "comparator": "gt",
			"threshold": 500000, "windowMonths": 1, "severity": "high",
			"description": "单站单月电费超 5000 元",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", feeRulesCreateName, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) answered a tool error: %+v", feeRulesCreateName, res.Content)
	}
	out := toolResultJSON(t, res)
	if out["enabled"] != false {
		t.Fatalf("the created rule must land disabled (观察态): %v", out)
	}
	for _, field := range []string{"id", "name", "feeType", "metric", "aggregation", "dimension", "comparator", "severity"} {
		if v, _ := out[field].(string); v == "" {
			t.Errorf("%s missing from the answer: %v", field, out)
		}
	}
	if out["threshold"] != float64(500000) {
		t.Fatalf("threshold = %v, want 500000", out["threshold"])
	}

	// The audit row: actor=mcp, action=created — the 阈值变更留痕 red line
	// on the MCP channel.
	id, _ := out["id"].(string)
	rules, err := db.FeeRiskRules.AuditFor(ctx, id)
	if err != nil || len(rules) != 1 {
		t.Fatalf("audit rows = %d (%v), want 1", len(rules), err)
	}
	if rules[0].Action != "created" || rules[0].Actor != "mcp" {
		t.Fatalf("audit row = %+v, want created/mcp", rules[0])
	}

	// A repeated identical call: the UNIQUE-name refusal, carried as a tool
	// error with the honest text.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: feeRulesCreateName,
		Arguments: map[string]any{
			"name": "电费单月超阈值", "feeType": "electricity", "metric": "amount",
			"aggregation": "sum", "dimension": "site_month", "comparator": "gt",
			"threshold": 1, "severity": "low",
		},
	})
	if err != nil {
		t.Fatalf("duplicate call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a duplicate name must answer a tool error, got %+v", res.Content)
	}
	if !strings.Contains(toolResultText(t, res), "已存在") {
		t.Fatalf("duplicate refusal text = %v", res.Content)
	}

	// A closed-set violation: the refusal names every received enum.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: feeRulesCreateName,
		Arguments: map[string]any{
			"name": "坏枚举", "feeType": "electricity", "metric": "amount_cents",
			"aggregation": "sum", "dimension": "site_month", "comparator": "gt",
			"threshold": 1, "severity": "low",
		},
	})
	if err != nil {
		t.Fatalf("bad-enum call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a foreign metric must answer a tool error, got %+v", res.Content)
	}
}

// TestMCPReadOnlyRegistryExcludesRuleCreate pins the deployment boundary:
// the --read-only variant's registry is exactly the full server's
// readOnlyHint=true set — fee_rules_create (readOnlyHint=false) can never
// leak into it (the invariant beyond the count gates).
func TestMCPReadOnlyRegistryExcludesRuleCreate(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ro, err := startReadOnlySession(t, db).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("read-only ListTools: %v", err)
	}
	names := make([]string, 0, len(ro.Tools))
	for _, tool := range ro.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == feeRulesCreateName {
			t.Fatalf("the --read-only registry must never expose %s (write tool): %v", feeRulesCreateName, names)
		}
	}
	if len(names) != 4 {
		t.Fatalf("read-only registry = %v, want the four pure queries", names)
	}
}
