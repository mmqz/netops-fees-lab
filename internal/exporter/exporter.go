// Package exporter projects the CURRENT resource inventory of a database
// back into an .xlsx workbook that is structurally isomorphic to an import
// workbook (W12-D, v0.6): one sheet per resource type (mapping.Specs order,
// only types that carry resources), the header row of each sheet is the
// spec's field Labels, and one row per resource is the attribute snapshot
// of its current state (including values that only became current through
// a W005 attribute-change apply or a W006 rename apply — the export is a
// state snapshot, not a reconstruction of any source workbook).
//
// Round-trip idempotency (回源幂等) is the acceptance core: the exported
// workbook re-enters the existing analyze → suggest → import pipeline on a
// FRESH database and reproduces an equivalent resource set (type, natural
// key, attributes) with the relations re-derived from the ref columns. The
// design decisions that make this work:
//
//   - Sheet names are pinned per type (sheetNames) such that
//     mapping.SuggestType(sheetName) == the type — the exported workbook is
//     analyzable with zero manual mapping (a test pins the invariant).
//   - Headers are field Labels; every Label is (case-insensitively) a
//     synonym of its own field, so the suggestion engine maps each column
//     back to its field with the synonym confidence 0.75 (NOT 1.0 — the
//     exact-match tier only fires on English field names; a test pins the
//     actual value).
//   - Enum columns (status / delay_mechanism) are backfilled from
//     Resource.Status; key-component columns are backfilled from the
//     natural key (see keyColumnValues for the exact reconstruction and its
//     known precision limits).
//
// The package is pure: it consumes []domain.Resource plus mapping.Specs and
// the standard library (excelize for writing); the HTTP API and the CLI
// load the resources and call Build/BuildBytes/WriteFile. Byte
// determinism: the same resource set (regardless of input order) yields
// byte-identical xlsx output — rows are sorted by natural key, sheets by
// Specs order, and excelize writes fixed zip timestamps (a test pins both
// bytes-equal and order-insensitivity).
package exporter

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
)

// SheetName returns the worksheet name of t in the exported workbook. The
// names are pinned so mapping.SuggestType(name) == t (the sheet-name hint
// ladder of mapping.Specs, evaluated in Specs order): 网元设备→设备,
// IP地址池→池, 传输电路→电路, 端口资源表→端口 (NOT the type Label 设备端口 —
// it contains 设备 and would suggest device first), 基站资源表→基站,
// 局站资源表→局站, 机房资源表→机房, PTP配置→PTP (the same name the PTP
// script channel synthesizes, ptpconfig.ScriptSheetName). The analyze of an
// exported workbook therefore suggests the right type for every sheet.
//
// The E003 round-trip note: the Specs order of the exported sheets puts
// device before ip_pool/circuit (their ref target) and site before
// machine_room (its ref target), so a single-batch re-import of the
// exported workbook resolves every ref — no manual sheet reordering.
func SheetName(t domain.ResourceType) string {
	if name, ok := sheetNames[t]; ok {
		return name
	}
	return t.String()
}

// sheetNames is the pinned sheet-name table behind SheetName.
var sheetNames = map[domain.ResourceType]string{
	domain.ResourceTypeDevice:      "网元设备",
	domain.ResourceTypeIPPool:      "IP地址池",
	domain.ResourceTypeCircuit:     "传输电路",
	domain.ResourceTypePort:        "端口资源表",
	domain.ResourceTypeBaseStation: "基站资源表",
	domain.ResourceTypeSite:        "局站资源表",
	domain.ResourceTypeMachineRoom: "机房资源表",
	domain.ResourceTypePTPPort:     "PTP配置",
}

// emptySheetName is the placeholder sheet of an empty export (a database
// with no resources of any selected type). excelize requires at least one
// worksheet per workbook, so a "zero-sheet" export is impossible; the
// placeholder carries no SuggestType hint, no headers and no rows, and is
// skipped by any re-import attempt (the workbook reader reports
// ErrEmptyWorkbook long before the mapping stage). The sheet is NOT named
// after any resource type so it can never be mistaken for an empty type.
const emptySheetName = "资源导出"

// Summary counts the exported rows: one entry per written sheet (type
// string → row count) and the total across sheets.
type Summary struct {
	// Types maps resource type (string form) → number of data rows in its
	// sheet. Only types that produced a sheet are present.
	Types map[string]int
	// TotalRows is the sum of the Types values.
	TotalRows int
}

// ParseTypes parses a comma-separated resource type list (CLI --types /
// HTTP ?types=). Empty entries and surrounding whitespace are skipped, so
// "", " " and "device,,ip_pool" behave as "no filter", "no filter" and
// [device, ip_pool]. An unknown type value returns an error naming it and
// listing the valid values — the HTTP layer maps it to 400 bad_request,
// the CLI to a usage error (exit 2). Duplicates are collapsed (first
// occurrence wins); the returned order follows the input.
func ParseTypes(raw string) ([]domain.ResourceType, error) {
	seen := make(map[domain.ResourceType]bool)
	var types []domain.ResourceType
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		t, err := domain.ParseResourceType(part)
		if err != nil {
			return nil, fmt.Errorf("types 参数包含未知资源类型 %q（可用：%s）",
				part, strings.Join(validTypeNames(), ", "))
		}
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	return types, nil
}

// validTypeNames lists the resource type names in Specs order (for error
// messages).
func validTypeNames() []string {
	names := make([]string, 0, len(mapping.Specs))
	for _, spec := range mapping.Specs {
		names = append(names, spec.Type.String())
	}
	return names
}

// selectedSpecs returns the type specs to export in Specs order: every
// spec when types is empty, else the specs of the selected types (the
// input order is normalized to Specs order for determinism).
func selectedSpecs(types []domain.ResourceType) []mapping.TypeSpec {
	if len(types) == 0 {
		return mapping.Specs
	}
	wanted := make(map[domain.ResourceType]bool, len(types))
	for _, t := range types {
		wanted[t] = true
	}
	var out []mapping.TypeSpec
	for _, spec := range mapping.Specs {
		if wanted[spec.Type] {
			out = append(out, spec)
		}
	}
	return out
}

// BuildBytes builds the export workbook of resources (filtered to types,
// empty filter = all types) and returns its xlsx bytes plus the row
// summary. See Build for the workbook shape; the bytes are what the HTTP
// handler streams and what WriteFile persists.
func BuildBytes(resources []domain.Resource, types []domain.ResourceType) ([]byte, Summary, error) {
	return buildBytes(resources, types)
}

// WriteFile writes the export workbook of resources (filtered to types) to
// path, creating or TRUNCATING it (an existing file is overwritten —
// script-friendly, documented in the CLI help and docs), and returns the
// row summary.
func WriteFile(path string, resources []domain.Resource, types []domain.ResourceType) (Summary, error) {
	data, summary, err := buildBytes(resources, types)
	if err != nil {
		return Summary{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Summary{}, fmt.Errorf("exporter: write %s: %w", path, err)
	}
	return summary, nil
}

// Build builds the export workbook of resources (filtered to types, empty
// filter = all types) as a fresh *excelize.File the CALLER must Close. The
// workbook shape:
//
//   - one worksheet per selected type WITH at least one resource, in
//     mapping.Specs order (SheetName(t) as the name); types with no
//     resources get NO sheet (the re-import of such a workbook simply has
//     nothing to import for them);
//   - row 1 of every sheet = the spec's field Labels (spec field order);
//   - one data row per resource, ordered by natural key (byte order,
//     input-order independent);
//   - cell values: attributes snapshot — r.Attributes for ordinary
//     fields, r.Status for the (single) enum field, and the key-component
//     reconstruction of keyColumnValues for natural-key fields.
//
// A database with no resources of any selected type yields a workbook with
// the single empty placeholder sheet (emptySheetName).
func Build(resources []domain.Resource, types []domain.ResourceType) (*excelize.File, Summary, error) {
	f := excelize.NewFile()
	summary := Summary{Types: map[string]int{}}
	byType := groupByType(resources)

	specs := selectedSpecs(types)
	usedDefaultSheet := false
	for _, spec := range specs {
		rows := byType[spec.Type]
		if len(rows) == 0 {
			continue
		}
		name := SheetName(spec.Type)
		if !usedDefaultSheet {
			// Reuse the initial "Sheet1" instead of creating and deleting
			// a sheet: zero Rename/Delete churn, deterministic output.
			if err := f.SetSheetName("Sheet1", name); err != nil {
				_ = f.Close()
				return nil, Summary{}, fmt.Errorf("exporter: name sheet %q: %w", name, err)
			}
			usedDefaultSheet = true
		} else if _, err := f.NewSheet(name); err != nil {
			_ = f.Close()
			return nil, Summary{}, fmt.Errorf("exporter: create sheet %q: %w", name, err)
		}
		if err := writeSheet(f, name, spec, rows); err != nil {
			_ = f.Close()
			return nil, Summary{}, err
		}
		summary.Types[spec.Type.String()] = len(rows)
		summary.TotalRows += len(rows)
	}
	if !usedDefaultSheet {
		// Empty export: keep the container valid (excelize requires one
		// worksheet) with the neutral, hint-free placeholder sheet.
		if err := f.SetSheetName("Sheet1", emptySheetName); err != nil {
			_ = f.Close()
			return nil, Summary{}, fmt.Errorf("exporter: name placeholder sheet: %w", err)
		}
	}
	return f, summary, nil
}

// buildBytes is the byte form of Build (single writer pass).
func buildBytes(resources []domain.Resource, types []domain.ResourceType) ([]byte, Summary, error) {
	f, summary, err := Build(resources, types)
	if err != nil {
		return nil, Summary{}, err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, Summary{}, fmt.Errorf("exporter: write workbook bytes: %w", err)
	}
	return buf.Bytes(), summary, nil
}

// groupByType buckets resources by type. Types are keyed by the domain
// enum; resources of unknown types are ignored silently (they cannot be
// projected without a spec — unreachable through the importer).
func groupByType(resources []domain.Resource) map[domain.ResourceType][]domain.Resource {
	byType := make(map[domain.ResourceType][]domain.Resource)
	for _, r := range resources {
		if !r.Type.IsValid() {
			continue
		}
		byType[r.Type] = append(byType[r.Type], r)
	}
	return byType
}

// writeSheet writes the header row and the sorted data rows of one type's
// sheet. Row order = natural key byte order (sort.Strings), independent of
// the input order.
func writeSheet(f *excelize.File, name string, spec mapping.TypeSpec, rows []domain.Resource) error {
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.NaturalKey
	}
	sort.Strings(keys)

	// Header row: spec field order, field Label text.
	for col, field := range spec.Fields {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			return fmt.Errorf("exporter: sheet %q header cell: %w", name, err)
		}
		if err := f.SetCellValue(name, cell, field.Label); err != nil {
			return fmt.Errorf("exporter: sheet %q header %s: %w", name, cell, err)
		}
	}

	// Data rows, natural-key order.
	keyed := make(map[string][]domain.Resource, len(rows))
	for _, r := range rows {
		keyed[r.NaturalKey] = append(keyed[r.NaturalKey], r)
	}
	for rowIdx, key := range keys {
		// Duplicate natural keys are impossible in the database (unique
		// per type); the bucket guards the sheet writer anyway.
		r := keyed[key][0]
		values := rowValues(spec, r)
		for col, field := range spec.Fields {
			cell, err := excelize.CoordinatesToCellName(col+1, rowIdx+2)
			if err != nil {
				return fmt.Errorf("exporter: sheet %q row cell: %w", name, err)
			}
			if err := f.SetCellValue(name, cell, values[field.Name]); err != nil {
				return fmt.Errorf("exporter: sheet %q cell %s: %w", name, cell, err)
			}
		}
	}
	return nil
}

// rowValues projects one resource into field-name → cell value:
//
//   - natural key fields: the reconstruction of keyColumnValues (the
//     attributes never store key fields);
//   - the enum field: r.Status (the importer stored the ONE valid enum
//     value of the row there — status for device/ip_pool/circuit,
//     delay_mechanism for ptp_port);
//   - every other field: r.Attributes[name] ("" when absent — attributes
//     only store non-empty values).
//
// Attributes with names outside the spec are ignored (unreachable through
// the importer, which only gathers spec fields).
func rowValues(spec mapping.TypeSpec, r domain.Resource) map[string]string {
	attrs := r.Attributes
	values := make(map[string]string, len(spec.Fields))
	enumDone := false
	keyCols := keyColumnValues(spec, r)
	for _, field := range spec.Fields {
		switch {
		case field.NaturalKey:
			values[field.Name] = keyCols[field.Name]
		case field.Kind == mapping.KindEnum && !enumDone:
			// Exactly one enum field per current spec; the defensive
			// enumDone keeps a hypothetical second enum column empty
			// instead of duplicating Status into it.
			values[field.Name] = r.Status
			enumDone = true
		default:
			values[field.Name] = attrs[field.Name]
		}
	}
	return values
}

// keyColumnValues reconstructs the natural-key FIELD values of r from its
// stored natural key (and, for degraded conditional identities, its
// attributes). This is the one projection where the database stores LESS
// than the source sheet did (attributes() excludes key fields), so the
// reconstruction rules are pinned here:
//
//   - Conditional key (KeyFallbackFields, base_station): when the
//     natural key equals the fallback join of the non-empty fallback-field
//     attributes (in KeyFallbackFields order), the row's identity IS the
//     degraded key — the primary key column(s) stay EMPTY and the fallback
//     columns flow from the attributes through the ordinary path, so the
//     re-import re-derives the exact same degraded key (and re-records
//     W004, preserving the identity semantics instead of laundering the
//     station name into the code column). Otherwise the key is the
//     primary one and is written to the primary key column verbatim.
//   - Single primary key field: the whole natural key IS the field value
//     (single-key types never split, so '|' inside a device_code survives
//     the round trip unharmed).
//   - Composite key (port/ptp_port): the LAST key component equals the
//     display name (spec.NameField — port_name / interface name), which
//     the database still stores in Resource.Name. The key is recovered
//     EXACTLY as prefix + name (strings.TrimSuffix of "|"+Name), which is
//     correct even when either component contains '|' — strictly more
//     precise than a plain split. When the name invariant does not hold
//     (defensive; unreachable through the importer, where the name field
//     of a composite-key type is Required), the key is split on '|' and
//     the parts are assigned in order: len(parts) == n → zip; more parts
//     than columns → the surplus joins into the LAST column; fewer → the
//     trailing columns stay empty. That fallback cannot distinguish which
//     component contained the '|' — the documented precision limit.
func keyColumnValues(spec mapping.TypeSpec, r domain.Resource) map[string]string {
	keyNames := spec.NaturalKeyNames()
	values := make(map[string]string, len(keyNames))
	attrs := r.Attributes

	// Conditional identity: degraded (fallback) key detection.
	if len(spec.KeyFallbackFields) > 0 {
		parts := make([]string, 0, len(spec.KeyFallbackFields))
		for _, name := range spec.KeyFallbackFields {
			if v := attrs[name]; v != "" {
				parts = append(parts, v)
			}
		}
		if fb := strings.Join(parts, "|"); fb != "" && fb == r.NaturalKey {
			for _, name := range keyNames {
				values[name] = "" // degraded identity: no primary key value
			}
			return values
		}
	}

	if len(keyNames) == 1 {
		values[keyNames[0]] = r.NaturalKey
		return values
	}

	// Composite key: exact recovery via the display-name suffix.
	last := keyNames[len(keyNames)-1]
	if spec.NameField() == last && r.Name != "" && strings.HasSuffix(r.NaturalKey, "|"+r.Name) {
		values[last] = r.Name
		prefix := strings.TrimSuffix(r.NaturalKey, "|"+r.Name)
		leading := keyNames[:len(keyNames)-1]
		if len(leading) == 1 {
			values[leading[0]] = prefix
			return values
		}
		// More than two key components (no current spec): the prefix is
		// split and assigned in order — the generic precision limit.
		assignSplit(values, leading, strings.Split(prefix, "|"))
		return values
	}

	// Defensive split path (see the doc comment for its limits).
	assignSplit(values, keyNames, strings.Split(r.NaturalKey, "|"))
	return values
}

// assignSplit assigns split key parts to the key field names in order:
// equal counts zip; surplus parts join into the LAST field (the '|'-inside-
// a-component ambiguity is documented as the precision limit); missing
// parts leave the trailing fields empty.
func assignSplit(values map[string]string, names, parts []string) {
	for i, name := range names {
		switch {
		case i >= len(parts):
			values[name] = ""
		case i == len(names)-1 && len(parts) > len(names):
			values[name] = strings.Join(parts[i:], "|")
		default:
			values[name] = parts[i]
		}
	}
}
