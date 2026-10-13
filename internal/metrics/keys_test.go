package metrics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/repo"
)

// TestRegistryShape pins the closed registry's own consistency: 14 keys,
// unique, valid enums, at least one synonym each, non-crossing bounds —
// and the exact key set (the catalog contract other tests rely on).
func TestRegistryShape(t *testing.T) {
	if err := VerifyRegistry(); err != nil {
		t.Fatalf("registry self check: %v", err)
	}
	if len(MetricKeys) != 14 {
		t.Errorf("registry size = %d, want 14 (the closed port-perf catalog)", len(MetricKeys))
	}
	// The core detection metrics of the design (§5.2 header list) must all
	// exist — the anomaly scan's acceptance data depends on them.
	for _, key := range []string{
		"optical_power_rx_avg_dbm", "optical_power_tx_avg_dbm",
		"packet_loss_rx_pct", "packet_loss_tx_pct",
		"bandwidth_util_rx_pct", "bandwidth_util_tx_pct",
		"rx_rate_mbps", "tx_rate_mbps",
		"errors_rx_count", "errors_tx_count",
		"module_temp_avg_c", "supply_voltage_v", "bias_current_ma",
	} {
		if _, ok := SpecForKey(key); !ok {
			t.Errorf("registry is missing core metric %q", key)
		}
	}
	// Synonym lookup: exact match, unknown header → false.
	if s, ok := SpecForHeader("接收光功率均值"); !ok || s.Key != "optical_power_rx_avg_dbm" {
		t.Errorf("SpecForHeader(接收光功率均值) = %v, %v; want optical_power_rx_avg_dbm", s, ok)
	}
	if _, ok := SpecForHeader(""); ok {
		t.Errorf("SpecForHeader(\"\") matched, want false")
	}
}

// TestCatalogSyncs is the #26-philosophy consistency gate: the
// metric_keys TABLE (seeded by migration v14, served by
// GET /api/metrics/keys) must equal the Go registry EXACTLY — same rows,
// same order, same values (bounds included). Any drift (a registry edit
// without the migration, or a manual table edit) fails here loudly.
func TestCatalogSyncs(t *testing.T) {
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()
	rows, err := db.Metrics.ListMetricKeys(context.Background())
	if err != nil {
		t.Fatalf("ListMetricKeys: %v", err)
	}
	if len(rows) != len(MetricKeys) {
		t.Fatalf("catalog rows = %d, want %d (registry size)", len(rows), len(MetricKeys))
	}
	for i, spec := range MetricKeys {
		got := rows[i]
		if got.Key != spec.Key {
			t.Errorf("catalog[%d].Key = %q, want %q (order is part of the contract)", i, got.Key, spec.Key)
			continue
		}
		if got.Label != spec.Label || got.Unit != spec.Unit {
			t.Errorf("catalog %q label/unit = %q/%q, want %q/%q", spec.Key, got.Label, got.Unit, spec.Label, spec.Unit)
		}
		if got.ValueKind != spec.ValueKind {
			t.Errorf("catalog %q value_kind = %q, want %q", spec.Key, got.ValueKind, spec.ValueKind)
		}
		if got.Direction != spec.Direction {
			t.Errorf("catalog %q direction = %q, want %q", spec.Key, got.Direction, spec.Direction)
		}
		if !sameFloatPtr(got.MinValue, spec.MinValue) {
			t.Errorf("catalog %q min_value = %v, want %v", spec.Key, got.MinValue, spec.MinValue)
		}
		if !sameFloatPtr(got.MaxValue, spec.MaxValue) {
			t.Errorf("catalog %q max_value = %v, want %v", spec.Key, got.MaxValue, spec.MaxValue)
		}
	}
}

// sameFloatPtr compares two *float64 (nil == nil).
func sameFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TestBoundsSanity exercises InBounds on the registry specs directly (the
// ingest path's quality=suspect decision reads the DB twin of these).
func TestBoundsSanity(t *testing.T) {
	rx, _ := SpecForKey("optical_power_rx_avg_dbm")
	if !rx.InBounds(-8.5) {
		t.Errorf("optical rx bounds %v..%v reject -8.5, want in-bounds", rx.MinValue, rx.MaxValue)
	}
	if rx.InBounds(-40) {
		t.Errorf("optical rx bounds accept -40 (outside), want false")
	}
	loss, _ := SpecForKey("packet_loss_rx_pct")
	if loss.InBounds(101) || loss.InBounds(-0.1) {
		t.Errorf("packet loss bounds accept out-of-range values, want false")
	}
}
