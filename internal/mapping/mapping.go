package mapping

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mmqz/netops-lab/internal/domain"
)

// Sheet actions of the mapping model (PROJECT_BRIEF §8).
const (
	// ActionImport marks a sheet to be imported.
	ActionImport = "import"
	// ActionSkip marks a sheet to be ignored by the import.
	ActionSkip = "skip"
)

// SheetMapping is the confirmed mapping of one sheet: import the sheet as
// ResourceType using ColumnMapping, or skip it entirely. Sheets absent from
// the mapping are skipped implicitly and contribute nothing to the import.
type SheetMapping struct {
	// Sheet is the worksheet name.
	Sheet string `json:"sheet"`
	// Action is "import" or "skip".
	Action string `json:"action"`
	// ResourceType is the import target type (import sheets only).
	ResourceType domain.ResourceType `json:"resourceType,omitempty"`
	// KeyColumns names the natural key field(s) of the target type.
	KeyColumns []string `json:"keyColumns,omitempty"`
	// ColumnMapping maps target field name → source header.
	ColumnMapping map[string]string `json:"columnMapping,omitempty"`
}

// Mapping is the confirmed mapping model stored on the batch (§8). Its JSON
// shape is exactly the body of POST /api/imports/{id}/mapping.
type Mapping struct {
	Sheets []SheetMapping `json:"sheets"`
}

// Parse decodes a mapping JSON document.
func Parse(data []byte) (Mapping, error) {
	var m Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		return Mapping{}, fmt.Errorf("mapping: parse: %w", err)
	}
	return m, nil
}

// Marshal encodes m as mapping JSON.
func Marshal(m Mapping) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("mapping: marshal: %w", err)
	}
	return b, nil
}

// FieldError is the typed validation error returned by Validate: it names
// the offending sheet and, when known, the offending target field (or a
// mapping-level key such as "keyColumns"), so the UI can point the user at
// the exact entry to fix. Use errors.As to extract it.
type FieldError struct {
	// Sheet is the sheet name; empty for mapping-level problems.
	Sheet string
	// Field is the target field name or mapping-level key; empty for
	// sheet-level problems.
	Field string
	// Msg describes the problem.
	Msg string
}

// Error implements the error interface.
func (e *FieldError) Error() string {
	switch {
	case e.Sheet != "" && e.Field != "":
		return fmt.Sprintf("mapping: sheet %q field %q: %s", e.Sheet, e.Field, e.Msg)
	case e.Sheet != "":
		return fmt.Sprintf("mapping: sheet %q: %s", e.Sheet, e.Msg)
	default:
		return fmt.Sprintf("mapping: %s", e.Msg)
	}
}

// Validate checks m against the actual workbook headers (headersBySheet,
// sheet name → header list). Rules (PROJECT_BRIEF §8):
//
//   - every action=import sheet must exist in the workbook;
//   - the action must be "import" or "skip" and sheet entries must be
//     unique;
//   - the resource type must be a known schema;
//   - keyColumns must be exactly the natural key field(s) of the type;
//   - every columnMapping source header must exist in that sheet and every
//     target field must be valid for the type;
//   - the natural key and all required fields must be mapped — an unmapped
//     required field blocks import (the UI forces user confirmation).
//
// The first problem found (deterministic order: sheets in mapping order,
// columnMapping keys sorted) is returned as *FieldError; nil means the
// mapping is executable.
func Validate(m Mapping, headersBySheet map[string][]string) error {
	seenSheets := make(map[string]bool)
	for i := range m.Sheets {
		sm := &m.Sheets[i]
		if sm.Sheet == "" {
			return &FieldError{Msg: "sheet name must not be empty"}
		}
		if seenSheets[sm.Sheet] {
			return &FieldError{Sheet: sm.Sheet, Msg: "duplicate sheet entry"}
		}
		seenSheets[sm.Sheet] = true
		switch sm.Action {
		case ActionImport, ActionSkip:
		default:
			return &FieldError{Sheet: sm.Sheet, Msg: fmt.Sprintf("invalid action %q (want %q or %q)",
				sm.Action, ActionImport, ActionSkip)}
		}
		if sm.Action == ActionSkip {
			continue
		}
		headers, ok := headersBySheet[sm.Sheet]
		if !ok {
			return &FieldError{Sheet: sm.Sheet, Msg: "sheet not found in workbook"}
		}
		if !sm.ResourceType.IsValid() {
			return &FieldError{Sheet: sm.Sheet, Field: string(sm.ResourceType),
				Msg: fmt.Sprintf("unknown resource type %q", sm.ResourceType)}
		}
		spec, _ := SpecByType(sm.ResourceType)
		if !equalNames(sm.KeyColumns, spec.NaturalKeyNames()) {
			return &FieldError{Sheet: sm.Sheet, Field: "keyColumns",
				Msg: fmt.Sprintf("keyColumns %v must be the natural key field(s) %v of type %s",
					sm.KeyColumns, spec.NaturalKeyNames(), sm.ResourceType)}
		}
		headerSet := make(map[string]bool, len(headers))
		for _, h := range headers {
			headerSet[h] = true
		}
		fields := make([]string, 0, len(sm.ColumnMapping))
		for field := range sm.ColumnMapping {
			fields = append(fields, field)
		}
		sort.Strings(fields) // deterministic error order
		for _, field := range fields {
			if _, ok := spec.Field(field); !ok {
				return &FieldError{Sheet: sm.Sheet, Field: field,
					Msg: fmt.Sprintf("unknown target field %q for type %s", field, sm.ResourceType)}
			}
			if !headerSet[sm.ColumnMapping[field]] {
				return &FieldError{Sheet: sm.Sheet, Field: field,
					Msg: fmt.Sprintf("source header %q not found in sheet", sm.ColumnMapping[field])}
			}
		}
		for _, f := range spec.Fields {
			if !f.Required {
				continue
			}
			if _, mapped := sm.ColumnMapping[f.Name]; !mapped {
				if f.NaturalKey {
					return &FieldError{Sheet: sm.Sheet, Field: f.Name, Msg: "natural key field must be mapped"}
				}
				return &FieldError{Sheet: sm.Sheet, Field: f.Name, Msg: "required field must be mapped"}
			}
		}
	}
	return nil
}

// equalNames reports whether a and b are equal string slices.
func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
