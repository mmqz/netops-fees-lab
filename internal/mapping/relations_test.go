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
//   - every spec's FromTypes/ToTypes are valid resource types with specs;
//   - CrossLayer is the UNANIMOUS verdict over every from×to layer pair —
//     the flag is computed FROM the layer registry, never hand-decided per
//     relation;
//   - the verdict is well-defined: no relation may mix same-layer and
//     cross-layer pairs (a relation whose ends span layers in a way that
//     makes "cross layer" ambiguous fails here).
//
// W21 note on the "single-valued side" rule: the W19 form required the
// from-side layers to be single-valued. The three §4.2 multi-end relations
// make a strict per-side rule impossible — serves starts from device (L1)
// OR base_station (L4), depends_on ends at device (L1) OR circuit (L3) —
// so the rule generalizes to pair-unanimity: for every (from, to) layer
// pair the same/cross verdict must be identical. When both sides are
// single-valued (26 of the 29 relations) this reduces exactly to the W19
// rule; the multi-end relations must merely stay unanimous (serves:
// L1→L6 and L4→L6 both cross; depends_on: L6→L1 and L6→L3 both cross;
// carried_by: L3→L3 and L3→L3 both same).
func TestRelationRegistryConsistency(t *testing.T) {
	for _, spec := range RelationSpecs() {
		if !spec.Type.IsValid() {
			t.Errorf("%s: relation type not valid per domain.IsValid", spec.Type)
		}
		if len(spec.FromTypes) == 0 {
			t.Errorf("%s: empty FromTypes", spec.Type)
			continue
		}
		if len(spec.ToTypes) == 0 {
			t.Errorf("%s: empty ToTypes", spec.Type)
			continue
		}
		for _, ft := range spec.FromTypes {
			if !ft.IsValid() {
				t.Errorf("%s: FromType %q not a valid resource type", spec.Type, ft)
			}
		}
		for _, tt := range spec.ToTypes {
			if !tt.IsValid() {
				t.Errorf("%s: ToType %q not a valid resource type", spec.Type, tt)
			}
		}
		// Pair-unanimity: the same-layer verdict must be identical over
		// every from×to combination, else CrossLayer is ill-defined.
		verdict := -1 // -1 unknown, 0 same-layer, 1 cross-layer
		for _, ft := range spec.FromTypes {
			lFrom := MustLayerOf(ft)
			for _, tt := range spec.ToTypes {
				lTo := MustLayerOf(tt)
				v := 0
				if lFrom != lTo {
					v = 1
				}
				if verdict == -1 {
					verdict = v
				} else if verdict != v {
					t.Errorf("%s: layer pairs disagree (%d vs %d) — cross-layer flag becomes ill-defined", spec.Type, verdict, v)
				}
			}
		}
		if verdict == -1 {
			continue // empty sides already reported above
		}
		if want := verdict == 1; spec.CrossLayer != want {
			t.Errorf("%s: CrossLayer = %v, want %v (unanimous layer verdict of %v→%v — flag must be computed from the layer registry)", spec.Type, spec.CrossLayer, want, spec.FromTypes, spec.ToTypes)
		}
	}
}

// TestRelationRegistryCoversDomainEnums pins the closed-set completeness:
// the domain's IsValid relation set and the registry are the same set. This
// is the guard that makes "未登记方向的关系不得使用" (§10.2) enforceable:
// adding a relation to domain/enums.go without a registry entry fails here.
func TestRelationRegistryCoversDomainEnums(t *testing.T) {
	valid := map[domain.RelationType]bool{}
	for _, rt := range domain.AllRelationTypes() {
		if !rt.IsValid() {
			t.Fatalf("AllRelationTypes lists %q which IsValid rejects", rt)
		}
		valid[rt] = true
	}
	if len(valid) != 29 {
		t.Fatalf("domain relation enum enumeration = %d, want 29 (W21 closed set)", len(valid))
	}
	for rt := range valid {
		if _, ok := SpecForRelation(rt); !ok {
			t.Errorf("relation %q is valid per domain but has no RelationSpec (§10.2: unregistered relations must not exist)", rt)
		}
	}
	seen := map[domain.RelationType]bool{}
	for _, spec := range RelationSpecs() {
		if !valid[spec.Type] {
			t.Errorf("relation %q has a RelationSpec but is not in the domain valid set (registry drift)", spec.Type)
		}
		if seen[spec.Type] {
			t.Errorf("relation %q has more than one RelationSpec", spec.Type)
		}
		seen[spec.Type] = true
	}
	if len(seen) != len(valid) {
		t.Errorf("registry covers %d relations, domain declares %d", len(seen), len(valid))
	}
}

// TestRelationTypeForStaysWithinRegistryContract pins the importer's
// ref-field → relation derivation to the registry contract: every edge the
// importer can derive must have from-side = the owning resource type and
// to-side containing the field's RefType. This is what keeps the import
// channel from ever writing a relation the traverse engine cannot reason
// about.
func TestRelationTypeForStaysWithinRegistryContract(t *testing.T) {
	// Every (type, ref field) pair of the Specs, walked through
	// RelationTypeFor, must either produce no relation ("" — allowed) or a
	// registered one whose FromTypes contain the owning type and whose
	// ToTypes contain the field's RefType.
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
			if !rs.AllowsFrom(spec.Type) {
				t.Errorf("%s.%s → relation %q: owning type not in FromTypes %v", spec.Type, f.Name, rt, rs.FromTypes)
			}
			if !rs.AllowsTo(f.RefType) {
				t.Errorf("%s.%s → relation %q: field RefType %q not in registry ToTypes %v", spec.Type, f.Name, rt, f.RefType, rs.ToTypes)
			}
		}
	}
}

// TestLayerAssignments pins the layer/domain value of every type — the
// layer table is code (§12-B5), and this test is its executable form. The
// expected values mirror the amended §10.2 layer table exactly (W21: all
// 33 types); any change must be a deliberate design-doc change, not a
// silent edit.
func TestLayerAssignments(t *testing.T) {
	expected := map[domain.ResourceType]struct {
		layer  int
		domain string
	}{
		// L0 空间层.
		domain.ResourceTypeSite:        {LayerSpace, DomainSpace},
		domain.ResourceTypeMachineRoom: {LayerSpace, DomainSpace},
		domain.ResourceTypeRack:        {LayerSpace, DomainSpace},
		domain.ResourceTypeDuct:        {LayerSpace, DomainSpace},
		domain.ResourceTypeManhole:     {LayerSpace, DomainSpace},
		domain.ResourceTypePole:        {LayerSpace, DomainSpace},
		// L1 物理层（设备）.
		domain.ResourceTypeDevice:  {LayerDevice, DomainDevice},
		domain.ResourceTypeBoard:   {LayerDevice, DomainDevice},
		domain.ResourceTypePort:    {LayerDevice, DomainDevice},
		domain.ResourceTypePTPPort: {LayerDevice, DomainDevice},
		// L2 物理层（光缆）.
		domain.ResourceTypeOpticalCable: {LayerOptical, DomainOptical},
		domain.ResourceTypeCableSegment: {LayerOptical, DomainOptical},
		domain.ResourceTypeFiber:        {LayerOptical, DomainOptical},
		domain.ResourceTypeODF:          {LayerOptical, DomainOptical},
		domain.ResourceTypeODFTerminal:  {LayerOptical, DomainOptical},
		domain.ResourceTypeSpliceBox:    {LayerOptical, DomainOptical},
		// L3 传输层.
		domain.ResourceTypeCircuit:     {LayerTransport, DomainTransport},
		domain.ResourceTypeOTNOch:      {LayerTransport, DomainTransport},
		domain.ResourceTypeSDHTimeslot: {LayerTransport, DomainTransport},
		// L4 接入层.
		domain.ResourceTypeBaseStation: {LayerAccess, DomainAccess},
		domain.ResourceTypeBBU:         {LayerAccess, DomainAccess},
		domain.ResourceTypeRRU:         {LayerAccess, DomainAccess},
		domain.ResourceTypeOLT:         {LayerAccess, DomainAccess},
		domain.ResourceTypeONU:         {LayerAccess, DomainAccess},
		domain.ResourceTypePONPort:     {LayerAccess, DomainAccess},
		// L5 逻辑层.
		domain.ResourceTypeRing:            {LayerLogic, DomainLogic},
		domain.ResourceTypeIGPDomain:       {LayerLogic, DomainLogic},
		domain.ResourceTypeVPNInstance:     {LayerLogic, DomainLogic},
		domain.ResourceTypeProtectionGroup: {LayerLogic, DomainLogic},
		domain.ResourceTypeIPPool:          {LayerLogic, DomainLogic},
		// L6 业务层.
		domain.ResourceTypeCustomer:   {LayerBusiness, DomainService},
		domain.ResourceTypeService:    {LayerBusiness, DomainService},
		domain.ResourceTypeLeasedLine: {LayerBusiness, DomainService},
	}
	if len(expected) != len(Specs) {
		t.Fatalf("layer expectation covers %d types, Specs has %d — extend both together", len(expected), len(Specs))
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

// TestMultiEndRelations pins the three §4.2 multi-end relations: carried_by
// and depends_on carry multiple ToTypes (ToType() returns "" for them),
// serves carries multiple FromTypes (its to side stays single — ToType()
// works). Every other relation is single-ended on both sides.
func TestMultiEndRelations(t *testing.T) {
	want := map[domain.RelationType]struct {
		from []domain.ResourceType
		to   []domain.ResourceType
	}{
		domain.RelationTypeCarriedBy: {
			from: []domain.ResourceType{domain.ResourceTypeCircuit},
			to:   []domain.ResourceType{domain.ResourceTypeOTNOch, domain.ResourceTypeSDHTimeslot},
		},
		domain.RelationTypeDependsOn: {
			from: []domain.ResourceType{domain.ResourceTypeService},
			to:   []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypeCircuit},
		},
		domain.RelationTypeServes: {
			from: []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypeBaseStation},
			to:   []domain.ResourceType{domain.ResourceTypeCustomer},
		},
		// W25（#16 验收补齐）：subscribed_by 扩为多起点端——业务与专线都属客户。
		domain.RelationTypeSubscribedBy: {
			from: []domain.ResourceType{domain.ResourceTypeService, domain.ResourceTypeLeasedLine},
			to:   []domain.ResourceType{domain.ResourceTypeCustomer},
		},
	}
	if len(want) != 4 {
		t.Fatalf("expectation table has %d entries, want 4", len(want))
	}
	for _, spec := range RelationSpecs() {
		w, multi := want[spec.Type]
		if !multi {
			if len(spec.FromTypes) != 1 {
				t.Errorf("%s: FromTypes = %v, want exactly one (only carried_by/depends_on/serves/subscribed_by are multi-end)", spec.Type, spec.FromTypes)
			}
			if len(spec.ToTypes) != 1 {
				t.Errorf("%s: ToTypes = %v, want exactly one (only carried_by/depends_on are multi-target)", spec.Type, spec.ToTypes)
			}
			if spec.ToType() == "" {
				t.Errorf("%s: ToType() = \"\", want the single to-side type", spec.Type)
			}
			continue
		}
		if !equalResourceTypes(spec.FromTypes, w.from) {
			t.Errorf("%s: FromTypes = %v, want %v", spec.Type, spec.FromTypes, w.from)
		}
		if !equalResourceTypes(spec.ToTypes, w.to) {
			t.Errorf("%s: ToTypes = %v, want %v", spec.Type, spec.ToTypes, w.to)
		}
		if len(spec.ToTypes) != 1 && spec.ToType() != "" {
			t.Errorf("%s: ToType() = %q, want \"\" (multi-target relation)", spec.Type, spec.ToType())
		}
	}
}

// equalResourceTypes compares two type slices order-sensitively.
func equalResourceTypes(a, b []domain.ResourceType) bool {
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
