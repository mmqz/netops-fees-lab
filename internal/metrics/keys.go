// Package metrics implements the timeseries metric storage channel of
// NetOps Lab (W20, migration v14 — design data-model-v1.md §5, ADR-0011 D1,
// ADR-0014 decision 2):
//
//   - keys.go: the closed metric-key registry (the Go fact source of the
//     metric_keys catalog; migration v14 seeds the table from the same
//     values, a consistency test pins the sync);
//   - mapping.go: the ingest mapping model ({tsField, resourceType,
//     resolveBy, metrics}) with synonym-driven suggestion and validation;
//   - service.go: the ingest/rollback/batch-listing service (separate
//     metric_batches table family, value-semantics upsert);
//   - synth.go: the deterministic synthetic data generator (CLI
//     `netopslab synthdata`).
//
// The package never speaks SQL — everything goes through internal/repo —
// and the MCP tool surface stays untouched (HTTP + CLI only, ADR-0011 D5).
package metrics

import (
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// MetricKeySpec is one entry of the closed metric registry: the definition
// of a metric the ingest channel knows how to store (mirrored into
// metric_keys by migration v14, pinned by TestCatalogSyncs — the #26
// single-fact-source philosophy: the registry and the table must never
// drift). MinValue/MaxValue are ADVISORY plausibility bounds: a value
// outside them is still recorded, with quality='suspect' (bounds filter
// nothing on ingest — they feed detection later). Synonyms are the real
// export header spellings the suggestion engine matches columns against
// (design §5.2 header list).
type MetricKeySpec struct {
	// Key is the canonical metric key (metric_keys.metric_key).
	Key string
	// Label is the human-readable (Chinese) name.
	Label string
	// Unit is the physical unit (dBm / % / Mbps / count / ℃ / V / mA).
	Unit string
	// ValueKind is gauge | counter | ratio.
	ValueKind domain.MetricValueKind
	// MinValue / MaxValue are the advisory plausibility bounds (nil = no
	// bound on that side).
	MinValue *float64
	MaxValue *float64
	// Direction declares which way is better (the anomaly scan's degrading
	// sign logic).
	Direction domain.MetricDirection
	// Synonyms lists the header spellings of the real port-performance
	// exports (design §5.2) that map to this key.
	Synonyms []string
}

// InBounds reports whether v is inside the spec's advisory bounds.
func (s MetricKeySpec) InBounds(v float64) bool {
	if s.MinValue != nil && v < *s.MinValue {
		return false
	}
	if s.MaxValue != nil && v > *s.MaxValue {
		return false
	}
	return true
}

// bounds helpers keep the registry literals readable.
func f(v float64) *float64 { return &v }

// MetricKeys is the ordered closed metric registry — the port-performance
// metrics of the real 端口性能 exports (design §5.2: 接收/发送光功率均值、
// 丢包率、带宽利用率、速率、错误包数、光模块温度、供电电压、偏置电流).
// The order IS the contract: migration v14 seeds metric_keys in this order
// (rowid order) and GET /api/metrics/keys serves it; the sync test pins it
// element by element. Adding a metric = append here + extend the v14 seed
// (or a later migration) — the sync gate fails loudly until both move.
var MetricKeys = []MetricKeySpec{
	{
		Key:       "optical_power_rx_avg_dbm",
		Label:     "接收光功率均值",
		Unit:      "dBm",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(-30.0),                             // below -30 dBm: link effectively dead / bad reading
		MaxValue:  f(5.0),                               // above +5 dBm: saturated receiver
		Direction: domain.MetricDirectionHigherIsBetter, // 光功率走低 = 光衰劣化
		Synonyms:  []string{"接收光功率均值", "接收光功率", "Rx光功率均值", "接收光功率平均值"},
	},
	{
		Key:       "optical_power_tx_avg_dbm",
		Label:     "发送光功率均值",
		Unit:      "dBm",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(-10.0),
		MaxValue:  f(8.0),
		Direction: domain.MetricDirectionHigherIsBetter, // 发送功率走低 = 激光器老化
		Synonyms:  []string{"发送光功率均值", "发送光功率", "Tx光功率均值", "发送光功率平均值"},
	},
	{
		Key:       "packet_loss_rx_pct",
		Label:     "接收丢包率",
		Unit:      "%",
		ValueKind: domain.MetricValueKindRatio,
		MinValue:  f(0.0),
		MaxValue:  f(100.0),
		Direction: domain.MetricDirectionLowerIsBetter,
		Synonyms:  []string{"接收丢包率", "接收丢包率(%)", "Rx丢包率"},
	},
	{
		Key:       "packet_loss_tx_pct",
		Label:     "发送丢包率",
		Unit:      "%",
		ValueKind: domain.MetricValueKindRatio,
		MinValue:  f(0.0),
		MaxValue:  f(100.0),
		Direction: domain.MetricDirectionLowerIsBetter,
		Synonyms:  []string{"发送丢包率", "发送丢包率(%)", "Tx丢包率"},
	},
	{
		Key:       "bandwidth_util_rx_pct",
		Label:     "接收带宽利用率",
		Unit:      "%",
		ValueKind: domain.MetricValueKindRatio,
		MinValue:  f(0.0),
		MaxValue:  f(100.0),
		// Utilization is load, not health: high is capacity pressure, low is
		// idle — the trend means different things per capacity plan.
		Direction: domain.MetricDirectionNeutral,
		Synonyms:  []string{"接收带宽利用率", "接收带宽利用率(峰值)", "Rx带宽利用率"},
	},
	{
		Key:       "bandwidth_util_tx_pct",
		Label:     "发送带宽利用率",
		Unit:      "%",
		ValueKind: domain.MetricValueKindRatio,
		MinValue:  f(0.0),
		MaxValue:  f(100.0),
		Direction: domain.MetricDirectionNeutral,
		Synonyms:  []string{"发送带宽利用率", "发送带宽利用率(峰值)", "Tx带宽利用率", "总带宽利用率"},
	},
	{
		Key:       "rx_rate_mbps",
		Label:     "接收速率",
		Unit:      "Mbps",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(0.0),
		MaxValue:  f(100000.0), // 100 Gbps headroom
		Direction: domain.MetricDirectionNeutral,
		Synonyms:  []string{"接收速率", "接收速率(ipv4)", "接收速率(ipv6)", "接收带宽"},
	},
	{
		Key:       "tx_rate_mbps",
		Label:     "发送速率",
		Unit:      "Mbps",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(0.0),
		MaxValue:  f(100000.0),
		Direction: domain.MetricDirectionNeutral,
		Synonyms:  []string{"发送速率", "发送速率(ipv4)", "发送速率(ipv6)", "发送带宽"},
	},
	{
		Key:       "errors_rx_count",
		Label:     "接收错误包数",
		Unit:      "count",
		ValueKind: domain.MetricValueKindCounter,
		MinValue:  f(0.0),
		Direction: domain.MetricDirectionLowerIsBetter,
		Synonyms:  []string{"接收错误包数", "接收错误包率", "接收错误包"},
	},
	{
		Key:       "errors_tx_count",
		Label:     "发送错误包数",
		Unit:      "count",
		ValueKind: domain.MetricValueKindCounter,
		MinValue:  f(0.0),
		Direction: domain.MetricDirectionLowerIsBetter,
		Synonyms:  []string{"发送错误包数", "发送错误包率", "发送错误包"},
	},
	{
		Key:       "module_temp_avg_c",
		Label:     "光模块温度均值",
		Unit:      "℃",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(-10.0),
		MaxValue:  f(90.0),                             // above 90℃: module outside operating range
		Direction: domain.MetricDirectionLowerIsBetter, // 温升 = 模块劣化
		Synonyms:  []string{"光模块温度均值", "光模块温度", "模块温度均值", "光模块温度峰值"},
	},
	{
		Key:       "supply_voltage_v",
		Label:     "供电电压",
		Unit:      "V",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(2.7),                        // below 2.7 V: supply rail out of spec (3.3 V nominal)
		MaxValue:  f(5.3),                        // above 5.3 V: supply rail out of spec (5 V nominal)
		Direction: domain.MetricDirectionNeutral, // 双向越界都是故障，趋势无单侧语义
		Synonyms:  []string{"供电电压", "电源电压", "供电电压(V)"},
	},
	{
		Key:       "bias_current_ma",
		Label:     "偏置电流",
		Unit:      "mA",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(0.0),
		MaxValue:  f(200.0),
		Direction: domain.MetricDirectionLowerIsBetter, // 偏置电流爬升 = 激光器老化先兆
		Synonyms:  []string{"偏置电流", "偏置电流(mA)", "激光器偏置电流"},
	},
	{
		Key:       "optical_power_rx_min_dbm",
		Label:     "接收光功率谷值",
		Unit:      "dBm",
		ValueKind: domain.MetricValueKindGauge,
		MinValue:  f(-35.0), // the daily trough may dip below the average floor
		MaxValue:  f(5.0),
		Direction: domain.MetricDirectionHigherIsBetter,
		Synonyms:  []string{"接收光功率谷值", "接收光功率最小值"},
	},
}

// SpecForKey returns the registry entry of a metric key.
func SpecForKey(key string) (MetricKeySpec, bool) {
	for _, s := range MetricKeys {
		if s.Key == key {
			return s, true
		}
	}
	return MetricKeySpec{}, false
}

// SpecForHeader returns the registry entry whose synonyms contain header
// (exact string match — the same "exact value match, never fuzzy inference"
// discipline the resolution lookup uses). The empty key "" never matches.
func SpecForHeader(header string) (MetricKeySpec, bool) {
	if header == "" {
		return MetricKeySpec{}, false
	}
	for _, s := range MetricKeys {
		for _, syn := range s.Synonyms {
			if syn == header {
				return s, true
			}
		}
	}
	return MetricKeySpec{}, false
}

// KeySet returns the closed set of registry keys (for validation errors).
func KeySet() []string {
	keys := make([]string, 0, len(MetricKeys))
	for _, s := range MetricKeys {
		keys = append(keys, s.Key)
	}
	return keys
}

// VerifyRegistry is the registry self check used by tests: every spec
// carries a canonical key, a valid value kind and direction and at least
// one synonym, keys are unique and bounds (when both set) do not cross.
func VerifyRegistry() error {
	seen := map[string]bool{}
	for _, s := range MetricKeys {
		if s.Key == "" {
			return fmt.Errorf("registry entry with empty key: %+v", s)
		}
		if seen[s.Key] {
			return fmt.Errorf("duplicate registry key %q", s.Key)
		}
		seen[s.Key] = true
		if !s.ValueKind.IsValid() {
			return fmt.Errorf("metric %q: invalid value kind %q", s.Key, s.ValueKind)
		}
		if !s.Direction.IsValid() {
			return fmt.Errorf("metric %q: invalid direction %q", s.Key, s.Direction)
		}
		if len(s.Synonyms) == 0 {
			return fmt.Errorf("metric %q: no synonyms", s.Key)
		}
		if s.MinValue != nil && s.MaxValue != nil && *s.MinValue > *s.MaxValue {
			return fmt.Errorf("metric %q: min %v > max %v", s.Key, *s.MinValue, *s.MaxValue)
		}
	}
	return nil
}
