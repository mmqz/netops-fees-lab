package mapping

import (
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// Confidence ladder of the suggestion engine (PROJECT_BRIEF §6): exact
// field-name match 1.0, normalized (trimmed, lower-cased) equality 0.9,
// synonym match 0.75, no suggestion 0.
const (
	ConfidenceExact      = 1.0
	ConfidenceNormalized = 0.9
	ConfidenceSynonym    = 0.75
)

// ColumnSuggestion is the suggestion for one workbook column.
type ColumnSuggestion struct {
	// Header is the workbook column header the suggestion was made for.
	Header string
	// Field is the suggested target field name; "" when there is no
	// suggestion.
	Field string
	// Confidence is the match confidence; 0 when there is no suggestion.
	Confidence float64
}

// Suggest returns one suggestion per header, in header order.
//
// When sheetName identifies a resource type (SuggestType), or the sheet
// name carries no hint but the header ROW does (SuggestTypeFromHeaders,
// issue #4: Sheet1/导出结果-style exports), only that type's fields are
// candidates — a column of an ip_pool sheet is never suggested as a device
// field, so suggestions are always valid targets for the sheet. For sheets
// with no type signal at all the candidates are all fields of all types (in
// Specs order). The best confidence wins; ties resolve to the earlier
// candidate, so suggestions are deterministic.
func Suggest(sheetName string, headers []string) []ColumnSuggestion {
	t, ok := SuggestType(sheetName)
	if !ok {
		t, ok = SuggestTypeFromHeaders(headers)
	}
	var candidates []FieldSpec
	if ok {
		if spec, found := SpecByType(t); found {
			candidates = spec.Fields
		}
	} else {
		for _, spec := range Specs {
			candidates = append(candidates, spec.Fields...)
		}
	}
	out := make([]ColumnSuggestion, len(headers))
	for i, h := range headers {
		field, conf := bestMatch(candidates, h)
		out[i] = ColumnSuggestion{Header: h, Field: field, Confidence: conf}
	}
	return out
}

// bestMatch finds the best suggestion for header among candidates.
func bestMatch(candidates []FieldSpec, header string) (string, float64) {
	trimmed := strings.TrimSpace(header)
	lower := strings.ToLower(trimmed)
	bestField := ""
	bestConf := 0.0
	for _, f := range candidates {
		conf := 0.0
		switch {
		case header == f.Name:
			conf = ConfidenceExact
		case lower == strings.ToLower(strings.TrimSpace(f.Name)):
			conf = ConfidenceNormalized
		default:
			for _, syn := range f.Synonyms {
				if trimmed == syn || lower == strings.ToLower(syn) {
					conf = ConfidenceSynonym
					break
				}
			}
		}
		if conf > bestConf {
			bestField, bestConf = f.Name, conf
		}
	}
	return bestField, bestConf
}

// SuggestType guesses the resource type of a sheet from its name: the first
// spec (in Specs order) whose SheetHint is a substring of the name wins, so
// 设备清单→device, 地址池/池→ip_pool, 电路→circuit; anything else returns
// false.
func SuggestType(sheetName string) (domain.ResourceType, bool) {
	for _, spec := range Specs {
		if strings.Contains(sheetName, spec.SheetHint) {
			return spec.Type, true
		}
	}
	return "", false
}

// SuggestTypeFromHeaders infers the resource type from the header ROW when
// the sheet name carries no hint (issue #4: system exports name their sheets
// Sheet1 / 导出结果 / 查询结果, and SuggestMapping used to silently skip
// them — "imported 0 rows" with no warning).
//
// Ladder: for every spec, count the spec fields whose Name or synonyms
// appear among the headers (same match ladder as bestMatch, synonym or
// better). A type QUALIFIES only when its complete natural key is present
// (identity without the key is unusable) AND at least half of its required
// fields are matched. The highest qualifying field count wins; ties resolve
// in Specs order so the result is deterministic. No qualifying type →
// ("", false): skip stays the safe default for genuinely unknown sheets.
//
// Worked example (the issue's repro): a Sheet1 with headers 设备名称/
// 端口名称/设备IP/带宽Mbps scores port 4+ (natural key 设备名称+端口名称
// complete) while device's natural key 设备编码 is absent → device is
// disqualified, port wins.
func SuggestTypeFromHeaders(headers []string) (domain.ResourceType, bool) {
	if len(headers) == 0 {
		return "", false
	}
	normHeaders := normalizeHeaders(headers)

	best := domain.ResourceType("")
	bestScore := 0
	for _, spec := range Specs {
		matched, keyMissing, required := 0, false, 0
		for _, f := range spec.Fields {
			if f.Required {
				required++
			}
			if anyHeaderMatches(normHeaders, f) {
				matched++
			} else if f.NaturalKey {
				keyMissing = true
			}
		}
		// Qualification: full natural key + majority of required fields.
		if keyMissing || matched < (required+1)/2 {
			continue
		}
		if matched > bestScore {
			best, bestScore = spec.Type, matched
		}
	}
	if best == "" || bestScore < 3 {
		return "", false
	}
	return best, true
}

// headerNorm is a header precomputed for the exact/normalized/synonym
// ladder (same forms as bestMatch).
type headerNorm struct{ trimmed, lower string }

func normalizeHeaders(headers []string) []headerNorm {
	out := make([]headerNorm, len(headers))
	for i, h := range headers {
		t := strings.TrimSpace(h)
		out[i] = headerNorm{trimmed: t, lower: strings.ToLower(t)}
	}
	return out
}

// anyHeaderMatches reports whether any header matches field f on the
// exact/normalized/synonym ladder.
func anyHeaderMatches(headers []headerNorm, f FieldSpec) bool {
	for _, h := range headers {
		if h.trimmed == f.Name || h.lower == strings.ToLower(strings.TrimSpace(f.Name)) {
			return true
		}
		for _, syn := range f.Synonyms {
			if h.trimmed == syn || h.lower == strings.ToLower(syn) {
				return true
			}
		}
	}
	return false
}

// SheetInfo names one sheet and its headers; the input of SuggestMapping.
type SheetInfo struct {
	Name    string
	Headers []string
}

// SuggestMapping builds a full suggested mapping: every sheet whose name
// OR HEADERS suggest a resource type (issue #4: Sheet1 with a recognizable
// header row is no longer skipped) gets action import with every suggested
// column mapped and the natural key as keyColumns; all other sheets get
// action skip. The UI prefills from this model and still requires explicit
// user confirmation (unmapped required fields block import — see
// Validate).
func SuggestMapping(sheets []SheetInfo) Mapping {
	m := Mapping{}
	for _, sh := range sheets {
		sm := SheetMapping{Sheet: sh.Name}
		t, ok := SuggestType(sh.Name)
		if !ok {
			t, ok = SuggestTypeFromHeaders(sh.Headers)
		}
		if !ok {
			sm.Action = ActionSkip
			m.Sheets = append(m.Sheets, sm)
			continue
		}
		spec, _ := SpecByType(t)
		sm.Action = ActionImport
		sm.ResourceType = t
		sm.KeyColumns = spec.NaturalKeyNames()
		sm.ColumnMapping = make(map[string]string)
		for _, sug := range Suggest(sh.Name, sh.Headers) {
			if sug.Field != "" {
				sm.ColumnMapping[sug.Field] = sug.Header
			}
		}
		m.Sheets = append(m.Sheets, sm)
	}
	return m
}
