package mapping

import (
	"reflect"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// TestSchemaProjectionMirrorsSpecs walks the projection field by field
// against the Specs registry — the anti-drift guard of GET /api/schema
// (W5-D): the endpoint body must be a faithful view of the Go specs, never a
// hand-copied parallel table.
func TestSchemaProjectionMirrorsSpecs(t *testing.T) {
	doc := BuildSchemaDocument()
	if len(doc.Types) != len(Specs) {
		t.Fatalf("projection has %d types, want %d (len of Specs)", len(doc.Types), len(Specs))
	}
	for i, spec := range Specs {
		got := doc.Types[i]
		if got.Type != spec.Type {
			t.Errorf("types[%d].type = %q, want %q", i, got.Type, spec.Type)
		}
		if got.Label != spec.Label {
			t.Errorf("%s: label = %q, want %q", spec.Type, got.Label, spec.Label)
		}
		if got.Layer != spec.Layer {
			t.Errorf("%s: layer = %d, want %d (spec.Layer)", spec.Type, got.Layer, spec.Layer)
		}
		if got.LayerName != LayerName(spec.Layer) {
			t.Errorf("%s: layerName = %q, want %q (LayerName of %d)", spec.Type, got.LayerName, LayerName(spec.Layer), spec.Layer)
		}
		if got.Domain != spec.Domain {
			t.Errorf("%s: domain = %q, want %q (spec.Domain)", spec.Type, got.Domain, spec.Domain)
		}
		if got.SheetHint != spec.SheetHint {
			t.Errorf("%s: sheetHint = %q, want %q", spec.Type, got.SheetHint, spec.SheetHint)
		}
		if want := spec.NameField(); got.NameField != want {
			t.Errorf("%s: nameField = %q, want %q (spec.NameField)", spec.Type, got.NameField, want)
		}
		if got.NameFieldOverride != spec.NameFieldOverride {
			t.Errorf("%s: nameFieldOverride = %q, want %q", spec.Type, got.NameFieldOverride, spec.NameFieldOverride)
		}
		if want := spec.NaturalKeyNames(); !reflect.DeepEqual(got.NaturalKeyFields, want) {
			t.Errorf("%s: naturalKeyFields = %v, want %v (spec order)", spec.Type, got.NaturalKeyFields, want)
		}
		if !reflect.DeepEqual(got.KeyFallbackFields, spec.KeyFallbackFields) {
			t.Errorf("%s: keyFallbackFields = %v, want %v", spec.Type, got.KeyFallbackFields, spec.KeyFallbackFields)
		}
		if len(got.Fields) != len(spec.Fields) {
			t.Fatalf("%s: projection has %d fields, want %d", spec.Type, len(got.Fields), len(spec.Fields))
		}
		for j, f := range spec.Fields {
			gf := got.Fields[j]
			if gf.Name != f.Name || gf.Label != f.Label || gf.Required != f.Required ||
				gf.NaturalKey != f.NaturalKey || gf.Kind != f.Kind {
				t.Errorf("%s: fields[%d] = %+v, want name/label/required/naturalKey/kind of %+v", spec.Type, j, gf, f)
			}
			if gf.IntMin != f.IntMin {
				t.Errorf("%s.%s: intMin = %d, want %d", spec.Type, f.Name, gf.IntMin, f.IntMin)
			}
			if gf.IntMax != f.IntMax {
				t.Errorf("%s.%s: intMax = %d, want %d", spec.Type, f.Name, gf.IntMax, f.IntMax)
			}
			if !reflect.DeepEqual(gf.Enum, f.Enum) {
				t.Errorf("%s.%s: enum = %v, want %v", spec.Type, f.Name, gf.Enum, f.Enum)
			}
			if gf.RefType != f.RefType {
				t.Errorf("%s.%s: refType = %q, want %q", spec.Type, f.Name, gf.RefType, f.RefType)
			}
			if !reflect.DeepEqual(gf.Synonyms, f.Synonyms) {
				t.Errorf("%s.%s: synonyms = %v, want %v", spec.Type, f.Name, gf.Synonyms, f.Synonyms)
			}
		}
	}
}

// TestSchemaProjectionRules pins the rules half of the projection: every
// AllRuleCodes entry appears once, in order, with the severity and
// description the domain catalog derives.
func TestSchemaProjectionRules(t *testing.T) {
	doc := BuildSchemaDocument()
	codes := domain.AllRuleCodes()
	if len(doc.Rules) != len(codes) {
		t.Fatalf("projection has %d rules, want %d", len(doc.Rules), len(codes))
	}
	for i, code := range codes {
		got := doc.Rules[i]
		if got.Code != code {
			t.Errorf("rules[%d].code = %q, want %q", i, got.Code, code)
		}
		wantSev, okSev := domain.SeverityForCode(code)
		if !okSev {
			t.Fatalf("rule %s: SeverityForCode does not know it (catalog drift)", code)
		}
		if got.Severity != wantSev {
			t.Errorf("rule %s: severity = %q, want %q", code, got.Severity, wantSev)
		}
		wantDesc, okDesc := domain.RuleDescriptionFor(code)
		if !okDesc || wantDesc == "" {
			t.Errorf("rule %s: description missing (every catalog code needs a gloss)", code)
		}
		if got.Description != wantDesc {
			t.Errorf("rule %s: description = %q, want %q", code, got.Description, wantDesc)
		}
	}
}

// TestSchemaProjectionSpotPins pins load-bearing details the generic mirror
// walk cannot see: the composite port key order, the base_station
// conditional key, the display-name overrides and the enum/int bounds of
// representative fields.
func TestSchemaProjectionSpotPins(t *testing.T) {
	doc := BuildSchemaDocument()
	byType := map[domain.ResourceType]SchemaType{}
	for _, st := range doc.Types {
		byType[st.Type] = st
	}

	port := byType[domain.ResourceTypePort]
	if want := []string{"device_name", "port_name"}; !reflect.DeepEqual(port.NaturalKeyFields, want) {
		t.Errorf("port naturalKeyFields = %v, want %v", port.NaturalKeyFields, want)
	}
	if port.NameField != "port_name" || port.NameFieldOverride != "port_name" {
		t.Errorf("port nameField/override = %q/%q, want port_name/port_name", port.NameField, port.NameFieldOverride)
	}

	bs := byType[domain.ResourceTypeBaseStation]
	if want := []string{"city", "base_station_name"}; !reflect.DeepEqual(bs.KeyFallbackFields, want) {
		t.Errorf("base_station keyFallbackFields = %v, want %v", bs.KeyFallbackFields, want)
	}
	if bs.NameField != "base_station_name" {
		t.Errorf("base_station nameField = %q, want base_station_name", bs.NameField)
	}

	// device has no override and a single-key natural key.
	dev := byType[domain.ResourceTypeDevice]
	if dev.NameField != "device_name" || dev.NameFieldOverride != "" {
		t.Errorf("device nameField/override = %q/%q, want device_name/\"\"", dev.NameField, dev.NameFieldOverride)
	}
	if want := []string{"device_code"}; !reflect.DeepEqual(dev.NaturalKeyFields, want) {
		t.Errorf("device naturalKeyFields = %v, want %v", dev.NaturalKeyFields, want)
	}

	// Field-level spot pins: enum, refType and int bounds survive projection.
	ipPool := byType[domain.ResourceTypeIPPool]
	fields := map[string]SchemaField{}
	for _, f := range ipPool.Fields {
		fields[f.Name] = f
	}
	if !reflect.DeepEqual(fields["status"].Enum, StatusEnum) {
		t.Errorf("ip_pool.status enum = %v, want %v", fields["status"].Enum, StatusEnum)
	}
	if fields["vlan_id"].IntMin != 1 {
		t.Errorf("ip_pool.vlan_id intMin = %d, want 1", fields["vlan_id"].IntMin)
	}
	// W9-D: the upper bound is projected too (the 4094 ceiling of VLAN ids —
	// it was the one IntMax the endpoint used to hide).
	if fields["vlan_id"].IntMax != 4094 {
		t.Errorf("ip_pool.vlan_id intMax = %d, want 4094", fields["vlan_id"].IntMax)
	}
	if fields["assigned_device"].RefType != domain.ResourceTypeDevice {
		t.Errorf("ip_pool.assigned_device refType = %q, want device", fields["assigned_device"].RefType)
	}

	// machine_room's site_id ref is the belongs_to source (W3-D).
	mr := byType[domain.ResourceTypeMachineRoom]
	mrFields := map[string]SchemaField{}
	for _, f := range mr.Fields {
		mrFields[f.Name] = f
	}
	if mrFields["site_id"].RefType != domain.ResourceTypeSite {
		t.Errorf("machine_room.site_id refType = %q, want site", mrFields["site_id"].RefType)
	}
}
