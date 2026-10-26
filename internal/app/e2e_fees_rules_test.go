package app

// e2e_fees_rules_test.go — issue #2's CLI end-to-end: the rule lifecycle
// create (observation default) → duplicate/enum refusals → list filters →
// the human enable flip → the audit ledger — plus the honest 501: the
// custom-rules SCENARIO needs the duckdb engine, but the rule TABLE
// management is fully functional on default builds (the same split every
// preset honors).

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFeesRulesLifecycle(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// create: the rule lands DISABLED with the rule_<uuid> id shape.
	created, code := runCLIJSON(t, "rules create", "fees", "--db", db, "--data", tmp,
		"rules", "create",
		"--name", "电费单月超阈值", "--type", "electricity", "--metric", "amount",
		"--agg", "sum", "--comparator", "gt", "--threshold", "500000",
		"--severity", "high", "--description", "单站单月电费超 5000 元")
	if code != 0 {
		t.Fatalf("create exit %d", code)
	}
	if created["enabled"] != false {
		t.Fatalf("a fresh rule must land disabled (观察态): %v", created)
	}
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "rule_") {
		t.Fatalf("id = %q, want the rule_<uuid> shape", id)
	}
	if created["windowMonths"] != float64(1) {
		t.Fatalf("window default = %v, want 1", created["windowMonths"])
	}

	// duplicate name: refused, non-zero.
	out, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"rules", "create",
		"--name", "电费单月超阈值", "--type", "electricity", "--metric", "amount",
		"--agg", "sum", "--comparator", "gt", "--threshold", "1", "--severity", "low")
	if code == 0 {
		t.Fatalf("duplicate name must refuse, stdout: %s", out)
	}

	// foreign enum: refused, non-zero, the text names the offending field.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"rules", "create",
		"--name", "坏枚举", "--type", "electricity", "--metric", "amount_cents",
		"--agg", "sum", "--comparator", "gt", "--threshold", "1", "--severity", "low")
	if code == 0 {
		t.Fatalf("foreign metric must refuse, stdout: %s", out)
	}
	if !strings.Contains(out, "枚举越界") {
		t.Fatalf("refusal text must carry the enum vocabulary: %s", out)
	}

	// missing required flags: usage error, exit 2.
	_, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"rules", "create", "--name", "缺参")
	if code != 2 {
		t.Fatalf("missing flags must answer exit 2, got %d", code)
	}

	// list: one disabled rule; enabled-only empty.
	listed, code := runCLIJSON(t, "rules list", "fees", "--db", db, "--data", tmp, "rules", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if listed["total"] != float64(1) {
		t.Fatalf("list total = %v, want 1", listed["total"])
	}
	enabledOnly, code := runCLIJSON(t, "rules list --enabled", "fees", "--db", db, "--data", tmp,
		"rules", "list", "--enabled")
	if code != 0 || enabledOnly["total"] != float64(0) {
		t.Fatalf("enabled-only list = %v (exit %d), want 0", enabledOnly["total"], code)
	}

	// the human flip: enable, verify the read-back, then the audit ledger.
	enabled, code := runCLIJSON(t, "rules enable", "fees", "--db", db, "--data", tmp,
		"rules", "enable", "--id", id, "--actor", "ops:alice")
	if code != 0 {
		t.Fatalf("enable exit %d", code)
	}
	if enabled["enabled"] != true {
		t.Fatalf("read-back after enable = %v", enabled)
	}
	audit, code := runCLIJSON(t, "rules audit", "fees", "--db", db, "--data", tmp,
		"rules", "audit", "--id", id)
	if code != 0 {
		t.Fatalf("audit exit %d", code)
	}
	if audit["total"] != float64(2) {
		t.Fatalf("audit total = %v, want created+enabled", audit["total"])
	}
	items := audit["items"].([]any)
	first := items[0].(map[string]any)
	if first["action"] != "enabled" || first["actor"] != "ops:alice" {
		t.Fatalf("audit newest = %v, want enabled/ops:alice", first)
	}

	// disable flips back; the third audit row lands.
	_, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"rules", "disable", "--id", id)
	if code != 0 {
		t.Fatalf("disable exit %d", code)
	}
	audit, code = runCLIJSON(t, "rules audit after disable", "fees", "--db", db, "--data", tmp,
		"rules", "audit", "--id", id)
	if code != 0 || audit["total"] != float64(3) {
		t.Fatalf("audit after disable = %v (exit %d), want 3 rows", audit["total"], code)
	}

	// unknown id: enable answers ErrNotFound (non-zero with the 404 vocabulary).
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"rules", "enable", "--id", "rule_missing")
	if code == 0 || !strings.Contains(out, "not found") {
		t.Fatalf("unknown id must refuse with the not-found text, got exit %d: %s", code, out)
	}

	// the custom-rules scenario on a default build answers the honest
	// engine-not-built refusal — the rule TABLE above was fully manageable.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"risks", "custom-rules", "--type", "electricity", "--year", "2024")
	if code == 0 {
		t.Fatalf("custom-rules on a default build must refuse, stdout: %s", out)
	}
	if !strings.Contains(out, "未编译分析查询引擎") {
		t.Fatalf("refusal must name the engine contract: %s", out)
	}
}
