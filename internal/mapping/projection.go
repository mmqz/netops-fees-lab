package mapping

import (
	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// This file is the schema-discovery projection behind GET /api/schema
// (W5-D, v0.4.0): a faithful, mechanically derived view of the Specs
// registry plus the domain rule catalog. Everything below is generated from
// the Go specs — nothing is hand-copied, so the endpoint cannot drift from
// the real mapping engine (the projection tests walk Specs field by field).
// The MCP tool surface stays frozen at its read-only tools: the schema
// endpoint is HTTP-only discovery, not an MCP capability.

// SchemaDocument is the top-level body of GET /api/schema: the resource type
// schemas followed by the validation rule catalog.
type SchemaDocument struct {
	// Types lists the resource type schemas in canonical Specs order.
	Types []SchemaType `json:"types"`
	// Rules lists the validation rule codes with severity and gloss.
	Rules []SchemaRule `json:"rules"`
}

// SchemaType is the projection of one TypeSpec.
type SchemaType struct {
	// Type is the resource type (device, ip_pool, …).
	Type domain.ResourceType `json:"type"`
	// Label is the human-readable (Chinese) type name.
	Label string `json:"label"`
	// Layer is the seven-layer model position (§10.2 as amended by
	// §12-B5; W19/issue #26). Exposed so schema consumers (AI included)
	// can reason about same-layer/cross-layer without a second table.
	Layer int `json:"layer"`
	// LayerName is the canonical display name of Layer.
	LayerName string `json:"layerName"`
	// Domain is the professional-domain axis (space/device/optical/
	// transport/access/logic/service).
	Domain string `json:"domain"`
	// SheetHint is the sheet-name substring that suggests this type.
	SheetHint string `json:"sheetHint"`
	// NameField is the field whose value becomes the resource Name.
	NameField string `json:"nameField"`
	// NameFieldOverride is the spec's explicit display-name override;
	// omitted when the default heuristic applies.
	NameFieldOverride string `json:"nameFieldOverride,omitempty"`
	// NaturalKeyFields lists the primary natural-key fields (spec order =
	// join order, '|'-joined by the importer).
	NaturalKeyFields []string `json:"naturalKeyFields"`
	// KeyFallbackFields lists the conditional-key fallback fields
	// (v0.4: base_station); omitted for unconditional-key types.
	KeyFallbackFields []string `json:"keyFallbackFields,omitempty"`
	// Fields lists the target fields in canonical spec order.
	Fields []SchemaField `json:"fields"`
}

// SchemaField is the projection of one FieldSpec. Kind is the field's value
// type; Enum/IntMin/IntMax/RefType/Synonyms appear only when the spec sets
// them (IntMin/IntMax 0 means "no bound" and is omitted — W9-D added IntMax:
// the vlan_id upper bound 4094 used to be invisible to schema consumers).
type SchemaField struct {
	Name       string              `json:"name"`
	Label      string              `json:"label"`
	Required   bool                `json:"required"`
	NaturalKey bool                `json:"naturalKey"`
	Kind       FieldKind           `json:"kind"`
	Enum       []string            `json:"enum,omitempty"`
	IntMin     int                 `json:"intMin,omitempty"`
	IntMax     int                 `json:"intMax,omitempty"`
	RefType    domain.ResourceType `json:"refType,omitempty"`
	Synonyms   []string            `json:"synonyms,omitempty"`
}

// SchemaRule is the projection of one rule code of the domain catalog.
type SchemaRule struct {
	Code        string          `json:"code"`
	Severity    domain.Severity `json:"severity"`
	Description string          `json:"description,omitempty"`
}

// SchemaDocument builds the full projection of the Specs registry plus the
// domain rule catalog. Callers may serialize it directly; the result is
// deterministic (Specs order and AllRuleCodes order).
func BuildSchemaDocument() SchemaDocument {
	types := make([]SchemaType, 0, len(Specs))
	for _, spec := range Specs {
		types = append(types, projectType(spec))
	}
	codes := domain.AllRuleCodes()
	rules := make([]SchemaRule, 0, len(codes))
	for _, code := range codes {
		r := SchemaRule{Code: code}
		if sev, ok := domain.SeverityForCode(code); ok {
			r.Severity = sev
		}
		if desc, ok := domain.RuleDescriptionFor(code); ok {
			r.Description = desc
		}
		rules = append(rules, r)
	}
	return SchemaDocument{Types: types, Rules: rules}
}

// projectType derives the SchemaType of one TypeSpec. NaturalKeyFields comes
// from NaturalKeyNames (spec field order), NameField from the spec's own
// NameField() (override-aware heuristic).
func projectType(spec TypeSpec) SchemaType {
	fields := make([]SchemaField, 0, len(spec.Fields))
	for _, f := range spec.Fields {
		fields = append(fields, SchemaField{
			Name:       f.Name,
			Label:      f.Label,
			Required:   f.Required,
			NaturalKey: f.NaturalKey,
			Kind:       f.Kind,
			Enum:       f.Enum,
			IntMin:     f.IntMin,
			IntMax:     f.IntMax,
			RefType:    f.RefType,
			Synonyms:   f.Synonyms,
		})
	}
	return SchemaType{
		Type:              spec.Type,
		Label:             spec.Label,
		Layer:             spec.Layer,
		LayerName:         LayerName(spec.Layer),
		Domain:            spec.Domain,
		SheetHint:         spec.SheetHint,
		NameField:         spec.NameField(),
		NameFieldOverride: spec.NameFieldOverride,
		NaturalKeyFields:  spec.NaturalKeyNames(),
		KeyFallbackFields: spec.KeyFallbackFields,
		Fields:            fields,
	}
}
