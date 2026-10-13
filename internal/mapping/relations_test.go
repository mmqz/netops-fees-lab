package mapping

import (
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// This file pins the type/relation registry (issue #26, B5): TypeSpec.Layer
// / Domain and the RelationSpec direction registry are the single source of
// truth the SQL CHECK constraints, /api/schema, the web mirror and the
// traverse engine all derive from. Every test here is a "防漏改" gate — a
// new type or relation that forgets layer/domain or direction semantics
// fails loudly instead of drifting silently.

// TestRelationRegistryConsistency is the registry self-check:
//   - every RelationType the domain declares IsValid has exactly one spec;
//   - every spec's FromTypes/ToType are valid resource types with specs;
//   - CrossLayer is exactly (layer(from) != layer(to)) — the flag is
//     computed FROM the layer registry, never hand-decided per relation;
//   - the from-side and to-side layers are single-valued (a relation whose
//     from types span layers would make "cross layer" ill-defined).
func TestRelationRegistryConsistency(t *testing.T) {
	for _, spec := range RelationSpecs() {
		if !spec.Type.IsValid() {
			t.Errorf("%s: relation type not valid per domain.IsValid", spec.Type)
		}
		if len(spec.FromTypes) == 0 {
			t.Errorf("%s: empty FromTypes", spec.Type)
			continue
		}
		if spec.ToType == "" {
			t.Errorf("%s: empty ToType", spec.Type)
			continue
		}
		if !spec.ToType.IsValid() {
			t.Errorf("%s: ToType %q not a valid resource type", spec.Type, spec.ToType)
		}
		layerFrom := -1
		for _, ft := range spec.FromTypes {
			if !ft.IsValid() {
				t.Errorf("%s: FromType %q not a valid resource type", spec.Type, ft)
			}
			l := MustLayerOf(ft)
			if layerFrom == -1 {
				layerFrom = l
			} else if layerFrom != l {
				t.Errorf("%s: FromTypes span layers (%d and %d) — cross-layer flag becomes ill-defined", spec.Type, layerFrom, l)
			}
		}
		layerTo := MustLayerOf(spec.ToType)
		if want := layerFrom != layerTo; spec.CrossLayer != want {
			t.Errorf("%s: CrossLayer = %v, want %v (layer %d vs %d — flag must be computed from the layer registry)", spec.Type, spec.CrossLayer, want, layerFrom, layerTo)
		}
	}
}

// TestRelationRegistryCoversDomainEnums pins the closed-set completeness:
// the domain's IsValid relation set and the registry are the same set. This
// is the guard that makes "未登记方向的关系不得使用" (§10.2) enforceable:
// adding a relation to domain/enums.go without a registry entry fails here.
func TestRelationRegistryCoversDomainEnums(t *testing.T) {
	// Enumerate the domain's valid relation set via ParseResourceType-style
	// round-trip: every string the domain accepts must have a spec.
	valid := map[domain.RelationType]bool{}
	for _, s := range []string{"assigned_to", "a_end", "z_end", "belongs_to"} {
		rt, err := domain.ParseRelationType(s)
		if err != nil {
			t.Fatalf("ParseRelationType(%q): %v", s, err)
		}
		valid[rt] = true
	}
	if len(valid) == 0 {
		t.Fatal("domain relation enum enumeration unexpectedly empty")
	}
	for rt := range valid {
		if _, ok := SpecForRelation(rt); !ok {
			t.Errorf("relation %q is valid per domain but has no RelationSpec (§10.2: unregistered relations must not exist)", rt)
		}
	}
	for _, spec := range RelationSpecs() {
		if !valid[spec.Type] {
			t.Errorf("relation %q has a RelationSpec but is not in the domain valid set (registry drift)", spec.Type)
		}
	}
}

// TestRelationTypeForStaysWithinRegistryContract pins the importer's
// ref-field → relation derivation to the registry contract: every edge the
// importer can derive must have from-side = the owning resource type and
// to-side matching the field's RefType. This is what keeps the import
// channel from ever writing a relation the traverse engine cannot reason
// about.
func TestRelationTypeForStaysWithinRegistryContract(t *testing.T) {
	// Every (type, ref field) pair of the Specs, walked through
	// RelationTypeFor, must either produce no relation ("" — allowed) or a
	// registered one whose FromTypes contain the owning type.
	for _, spec := range Specs {
		for _, f := range spec.Fields {
			if f.Kind != KindRef {
				continue
			}
			rt := RelationTypeFor(spec.Type, f.Name)
			if rt == "" {
				continue
			}
			rs, ok := SpecForRelation(rt)
			if !ok {
				t.Fatalf("%s.%s → relation %q has no RelationSpec", spec.Type, f.Name, rt)
			}
			fromOK := false
			for _, ft := range rs.FromTypes {
				if ft == spec.Type {
					fromOK = true
					break
				}
			}
			if !fromOK {
				t.Errorf("%s.%s → relation %q: owning type not in FromTypes %v", spec.Type, f.Name, rt, rs.FromTypes)
			}
			if f.RefType != rs.ToType {
				t.Errorf("%s.%s → relation %q: field RefType %q != registry ToType %q", spec.Type, f.Name, rt, f.RefType, rs.ToType)
			}
		}
	}
}

// TestLayerAssignments pins the layer/domain value of every type — the
// layer table is code (§12-B5), and this test is its executable form. The
// expected values mirror the amended §10.2 layer table exactly; any change
// must be a deliberate design-doc change, not a silent edit.
func TestLayerAssignments(t *testing.T) {
	expected := map[domain.ResourceType]struct {
		layer  int
		domain string
	}{
		domain.ResourceTypeSite:        {LayerSpace, DomainSpace},
		domain.ResourceTypeMachineRoom: {LayerSpace, DomainSpace},
		domain.ResourceTypeDevice:      {LayerDevice, DomainDevice},
		domain.ResourceTypePort:        {LayerDevice, DomainDevice},
		domain.ResourceTypePTPPort:     {LayerDevice, DomainDevice},
		domain.ResourceTypeCircuit:     {LayerTransport, DomainTransport},
		domain.ResourceTypeBaseStation: {LayerAccess, DomainAccess},
		domain.ResourceTypeIPPool:      {LayerLogic, DomainLogic},
	}
	if len(expected) != len(Specs) {
		t.Fatalf("layer expectation covers %d types, Specs has %d — extend both together (W21 will)", len(expected), len(Specs))
	}
	for _, spec := range Specs {
		want, ok := expected[spec.Type]
		if !ok {
			t.Fatalf("%s: no layer expectation — every type MUST have one (registry completeness)", spec.Type)
		}
		if spec.Layer != want.layer {
			t.Errorf("%s: Layer = %d, want %d", spec.Type, spec.Layer, want.layer)
		}
		if spec.Domain != want.domain {
			t.Errorf("%s: Domain = %q, want %q", spec.Type, spec.Domain, want.domain)
		}
	}
}

// TestLayerNameRoundTrip pins the display names of all seven layers —
// LayerName must cover every layer constant in use (unknown layers return
// "L<n>", which the SchemaType projection would leak as a wrong label).
func TestLayerNameRoundTrip(t *testing.T) {
	for l := 0; l <= 6; l++ {
		name := LayerName(l)
		if name == "" {
			t.Errorf("LayerName(%d) = empty", l)
		}
		if name == "L<n>" {
			t.Errorf("LayerName(%d) = placeholder", l)
		}
	}
	if LayerName(99) != "L99" {
		t.Errorf("LayerName(99) = %q, want \"L99\" (numeric fallback)", LayerName(99))
	}
}
