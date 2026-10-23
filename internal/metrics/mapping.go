package metrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
)

// IngestMapping is the confirmed mapping of one performance workbook to
// the metric channel (the metric twin of mapping.Mapping of the resource
// channel; stored as JSON on the metric batch):
//
//	{
//	  "tsField":     "日期",                      // source column of the data time
//	  "resourceType": "port",                     // the only v1 target
//	  "resolveBy":   ["device_name","port_name"], // or ["device_ip","port_name"]
//	  "metrics":     {"optical_power_rx_avg_dbm": "接收光功率均值", ...}
//	}
//
// resolveBy names the two SEMANTIC resolvers; the concrete source columns
// are located by exact header/synonym match (mapping's port spec — the
// same synonym philosophy as the resource channel, never fuzzy inference).
// metrics maps the target metric key (must be in the closed registry) to
// its source column header.
type IngestMapping struct {
	TSField      string            `json:"tsField"`
	ResourceType string            `json:"resourceType"`
	ResolveBy    []string          `json:"resolveBy"`
	Metrics      map[string]string `json:"metrics"`
}

// The two allowed resolver arrays (v1: port performance only). Path (a)
// resolves the port natural key directly from 设备名称+端口名; path (b)
// resolves the device by exact mgmt_ip match, then its name into the port
// key. Both are exact VALUE matches — name-similarity inference stays out
// (red line).
var (
	ResolveByNameIP = []string{"device_name", "port_name"}
	ResolveByIPPort = []string{"device_ip", "port_name"}
)

// ParseMapping decodes an ingest mapping JSON document.
func ParseMapping(data []byte) (IngestMapping, error) {
	var m IngestMapping
	if err := json.Unmarshal(data, &m); err != nil {
		return IngestMapping{}, fmt.Errorf("metrics: parse ingest mapping: %w", err)
	}
	return m, nil
}

// MarshalMapping encodes m as ingest mapping JSON.
func MarshalMapping(m IngestMapping) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("metrics: marshal ingest mapping: %w", err)
	}
	return b, nil
}

// fieldError mirrors mapping.FieldError's shape so the HTTP layer maps it
// to 400 validation through the SAME errors.As branch (mapServiceError).
type FieldError = mapping.FieldError

// validResolveBy reports whether r is one of the two allowed arrays.
func validResolveBy(r []string) bool {
	if len(r) != 2 {
		return false
	}
	if r[0] == ResolveByNameIP[0] && r[1] == ResolveByNameIP[1] {
		return true
	}
	return r[0] == ResolveByIPPort[0] && r[1] == ResolveByIPPort[1]
}

// resolveSemantics lists the semantic columns the ingest channel locates
// on a sheet: the three port resolvers plus the data-time column.
var resolveSemantics = []string{"device_name", "port_name", "device_ip", "record_date"}

// ResolveColumns locates the concrete source columns for the semantic
// resolvers: exact match against the sheet headers, then the port spec's
// field synonyms/labels (mapping.SpecByType(port) — the shared synonym
// engine; the semantic's own English name also matches, for non-Chinese
// headers). Returns the semantic name → header map; a semantic with no
// column is absent (callers turn that into a FieldError or a warning).
func ResolveColumns(headers []string) map[string]string {
	out := map[string]string{}
	spec, ok := mapping.SpecByType(domain.ResourceTypePort)
	if !ok {
		return out
	}
	for _, semantic := range resolveSemantics {
		field, hasField := spec.Field(semantic)
		for _, h := range headers {
			if h == "" {
				continue
			}
			if h == semantic {
				out[semantic] = h
				break
			}
			if !hasField {
				continue
			}
			if h == field.Label {
				out[semantic] = h
				break
			}
			matched := false
			for _, syn := range field.Synonyms {
				if h == syn {
					matched = true
					break
				}
			}
			if matched {
				out[semantic] = h
				break
			}
		}
	}
	return out
}

// Validate checks m against the actual workbook headers. Rules:
//
//   - resourceType must be "port" (the only v1 target of the metric
//     channel);
//   - resolveBy must be one of the two allowed arrays and every semantic
//     resolver must resolve to a real column of the sheet;
//   - tsField must be a real column of the sheet;
//   - every metrics key must be in the closed registry (unknown metric →
//     error naming the key and the valid set) and its source column must
//     exist in the sheet; the metrics map must not be empty;
//   - no column may be double-booked (ts/resolver/metric sources are
//     distinct columns).
//
// The first problem in deterministic order (resolveBy, tsField, metrics
// keys sorted) is returned as *FieldError; nil means executable.
func (m IngestMapping) Validate(headers []string) error {
	if domain.ResourceType(m.ResourceType) != domain.ResourceTypePort {
		return &FieldError{Field: "resourceType",
			Msg: fmt.Sprintf("resourceType %q 不是受支持的目标（v1 仅支持 port）", m.ResourceType)}
	}
	if !validResolveBy(m.ResolveBy) {
		return &FieldError{Field: "resolveBy",
			Msg: fmt.Sprintf("resolveBy %v 必须是 [device_name port_name] 或 [device_ip port_name]", m.ResolveBy)}
	}
	headerSet := make(map[string]bool, len(headers))
	for _, h := range headers {
		headerSet[h] = true
	}
	cols := ResolveColumns(headers)
	for _, semantic := range m.ResolveBy {
		header, ok := cols[semantic]
		if !ok || !headerSet[header] {
			return &FieldError{Field: "resolveBy",
				Msg: fmt.Sprintf("解析列 %s 在表头中找不到（需要与端口 schema 同义词精确匹配的列）", semantic)}
		}
	}
	if m.TSField == "" {
		return &FieldError{Field: "tsField", Msg: "tsField 不能为空（数据时间列）"}
	}
	if !headerSet[m.TSField] {
		return &FieldError{Field: "tsField", Msg: fmt.Sprintf("时间列 %q 不在表头中", m.TSField)}
	}
	if len(m.Metrics) == 0 {
		return &FieldError{Field: "metrics", Msg: "metrics 映射不能为空（至少一个指标列）"}
	}
	keys := make([]string, 0, len(m.Metrics))
	for k := range m.Metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, known := SpecForKey(k); !known {
			return &FieldError{Field: "metrics",
				Msg: fmt.Sprintf("未知指标键 %q（封闭目录：%s）", k, strings.Join(KeySet(), ", "))}
		}
		src := m.Metrics[k]
		if src == "" {
			return &FieldError{Field: "metrics",
				Msg: fmt.Sprintf("指标 %q 的源列名为空", k)}
		}
		if !headerSet[src] {
			return &FieldError{Field: "metrics",
				Msg: fmt.Sprintf("指标 %q 的源列 %q 不在表头中", k, src)}
		}
	}
	// No double-booking: ts + the two resolvers + metric sources must be
	// distinct columns (a column carrying two meanings is a mapping bug).
	used := map[string]string{m.TSField: "tsField"}
	for _, semantic := range m.ResolveBy {
		header := cols[semantic]
		if prev, dup := used[header]; dup {
			return &FieldError{Field: "resolveBy",
				Msg: fmt.Sprintf("列 %q 同时被 %s 与 %s 使用", header, prev, semantic)}
		}
		used[header] = semantic
	}
	for _, k := range keys {
		src := m.Metrics[k]
		if prev, dup := used[src]; dup {
			return &FieldError{Field: "metrics",
				Msg: fmt.Sprintf("列 %q 同时被 %s 与指标 %q 使用", src, prev, k)}
		}
		used[src] = "metrics." + k
	}
	return nil
}

// Suggest builds the suggested ingest mapping of one sheet: the tsField is
// the first port-spec date synonym (or the first header containing 日期),
// the resolvers prefer 设备名称+端口名 and fall back to 设备IP+端口名,
// and every remaining header that exactly matches a catalog synonym maps
// to its metric key. Unmatched headers produce warnings (suggestions are
// advisory — the user confirms).
func Suggest(sheet string, headers []string) (IngestMapping, []string) {
	warnings := []string{}
	cols := ResolveColumns(headers)

	tsField := ""
	if header, ok := cols["record_date"]; ok {
		tsField = header
	} else {
		for _, h := range headers {
			if strings.Contains(h, "日期") {
				tsField = h
				warnings = append(warnings, fmt.Sprintf("时间列 %q 不是端口 schema 的日期同义词（模糊包含匹配，请人工确认）", h))
				break
			}
		}
	}

	var resolveBy []string
	_, hasName := cols["device_name"]
	_, hasPort := cols["port_name"]
	_, hasIP := cols["device_ip"]
	if hasName && hasPort {
		resolveBy = ResolveByNameIP
	} else if hasIP && hasPort {
		resolveBy = ResolveByIPPort
	}

	// Every located semantic column is consumed (even the ones the chosen
	// resolveBy does not use — 设备IP alongside the name path is identity
	// context, never a metric candidate).
	consumed := map[string]bool{tsField: true}
	for _, semantic := range resolveSemantics {
		if h, ok := cols[semantic]; ok {
			consumed[h] = true
		}
	}
	metricsMap := map[string]string{}
	for _, h := range headers {
		if h == "" || consumed[h] {
			continue
		}
		if spec, ok := SpecForHeader(h); ok {
			if prev, dup := metricsMap[spec.Key]; dup {
				warnings = append(warnings, fmt.Sprintf("列 %q 与 %q 都指向指标 %q（保留首个，请人工确认）", h, prev, spec.Key))
				continue
			}
			metricsMap[spec.Key] = h
		} else {
			warnings = append(warnings, fmt.Sprintf("列 %q 未匹配到封闭指标目录（不导入该列）", h))
		}
	}
	if tsField == "" {
		warnings = append(warnings, "未找到时间列（建议映射的 tsField 为空，导入前必须人工指定）")
	}
	if resolveBy == nil {
		warnings = append(warnings, "未找到可用的端口解析列组合（设备名称+端口名 或 设备IP+端口名）")
	}
	if len(metricsMap) == 0 {
		warnings = append(warnings, "没有列匹配到指标目录（建议映射的 metrics 为空）")
	}
	return IngestMapping{
		TSField:      tsField,
		ResourceType: domain.ResourceTypePort.String(),
		ResolveBy:    resolveBy,
		Metrics:      metricsMap,
	}, warnings
}
