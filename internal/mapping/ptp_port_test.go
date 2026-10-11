package mapping

// ptp_port spec tests (v0.5 W8-D, PTP configuration-script import channel):
// the composite model-scoped key, the closed 23-field contract (kinds and
// synonym-as-label headers), the SuggestType sheet-hint wiring of the
// virtual sheet name, the synonym-ladder suggestions of the synthesized
// headers, and the mapping validation rules.

import (
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestPTPPortSpec pins the structural contract of the ptp_port schema.
func TestPTPPortSpec(t *testing.T) {
	spec, ok := SpecByType(domain.ResourceTypePTPPort)
	if !ok {
		t.Fatal("ptp_port spec missing from Specs")
	}
	if spec.Type != domain.ResourceTypePTPPort || spec.Label != "PTP端口" || spec.SheetHint != "PTP" {
		t.Errorf("spec identity = %s/%q/%q, want ptp_port/PTP端口/PTP", spec.Type, spec.Label, spec.SheetHint)
	}
	if got := spec.NameField(); got != "port_name" {
		t.Errorf("NameField(ptp_port) = %q, want port_name (override; device_model is the key scope)", got)
	}
	if got := spec.NaturalKeyNames(); !reflect.DeepEqual(got, []string{"device_model", "port_name"}) {
		t.Errorf("NaturalKeyNames = %v, want [device_model port_name] (composite, spec order = join order)", got)
	}
	if len(spec.KeyFallbackFields) != 0 {
		t.Errorf("KeyFallbackFields = %v, want none (unconditional composite key)", spec.KeyFallbackFields)
	}
	if !spec.RenameDetection {
		t.Error("RenameDetection must be ON: both key components are name-class (W006)")
	}
	if len(spec.Fields) != 23 {
		t.Fatalf("fields = %d, want 23", len(spec.Fields))
	}
	// Kind contract: the int parameters, the two textual-suffix strings,
	// the enum, and the absence of ref fields (model-level template — no
	// device-instance identity to resolve).
	for _, tc := range []struct {
		field string
		want  FieldKind
	}{
		{"device_model", KindString}, {"port_name", KindString}, {"annotation", KindString},
		{"portnum", KindInt}, {"port_state", KindString}, {"free_mode", KindString},
		{"pdelay_req_interval", KindInt}, {"unsymmetrical_offset", KindString},
		{"delay_mechanism", KindEnum}, {"domain_num", KindInt}, {"clock_mode", KindString},
		{"state_algorithm", KindString}, {"priority1", KindInt}, {"priority2", KindInt},
		{"local_priority", KindInt}, {"clock_class", KindInt}, {"clock_accuracy", KindInt},
		{"announce_interval", KindInt}, {"announce_timeout", KindInt}, {"utc_trace", KindString},
		{"ssm_mode", KindString}, {"ssm_sec_quality", KindInt}, {"ssm_wtr_time", KindInt},
	} {
		f, found := spec.Field(tc.field)
		if !found {
			t.Fatalf("spec misses field %s", tc.field)
		}
		if f.Kind != tc.want {
			t.Errorf("%s kind = %q, want %q", tc.field, f.Kind, tc.want)
		}
	}
	if f, _ := spec.Field("delay_mechanism"); !reflect.DeepEqual(f.Enum, []string{"e2e", "p2p"}) {
		t.Errorf("delay_mechanism enum = %v, want [e2e p2p]", f.Enum)
	}
	if f, _ := spec.Field("device_model"); !f.Required || !f.NaturalKey {
		t.Error("device_model must be Required + NaturalKey")
	}
	if f, _ := spec.Field("port_name"); !f.Required || !f.NaturalKey {
		t.Error("port_name must be Required + NaturalKey")
	}
	for _, f := range spec.Fields {
		if f.Kind == KindRef {
			t.Errorf("ptp_port must not carry ref fields (found %s)", f.Name)
		}
		if f.IntMin != 0 || f.IntMax != 0 {
			t.Errorf("field %s declares an int range, want none (no documented bounds in the material)", f.Name)
		}
		// The virtual sheet's column headers are the Labels; every Label
		// must be a Synonym (the first) so the ladder suggests the field.
		if len(f.Synonyms) == 0 || f.Synonyms[0] != f.Label {
			t.Errorf("field %s: Label %q must be Synonyms[0] (the synthesized header)", f.Name, f.Label)
		}
	}
}

// TestSuggestTypePTPPort pins the sheet-hint wiring of the virtual sheet
// name and the Specs-order rule against the port hint.
func TestSuggestTypePTPPort(t *testing.T) {
	if rt, ok := SuggestType("PTP配置"); !ok || rt != domain.ResourceTypePTPPort {
		t.Errorf(`SuggestType("PTP配置") = %q,%v, want ptp_port`, rt, ok)
	}
	// A sheet name carrying BOTH 端口 and PTP suggests port (earlier spec
	// wins) — the deterministic tie-break of Specs order.
	if rt, ok := SuggestType("PTP端口监控表"); !ok || rt != domain.ResourceTypePort {
		t.Errorf(`SuggestType("PTP端口监控表") = %q,%v, want port (order rule)`, rt, ok)
	}
	if _, ok := SuggestType("配置模板"); ok {
		t.Error(`SuggestType("配置模板") = ok, want no hint`)
	}
}

// TestPTPPortSuggest pins the synonym-ladder behaviour on the synthesized
// headers: every Label header suggests its own field at synonym confidence.
func TestPTPPortSuggest(t *testing.T) {
	spec, _ := SpecByType(domain.ResourceTypePTPPort)
	headers := make([]string, len(spec.Fields))
	for i, f := range spec.Fields {
		headers[i] = f.Label
	}
	sug := Suggest("PTP配置", headers)
	if len(sug) != len(headers) {
		t.Fatalf("suggestions = %d, want %d", len(sug), len(headers))
	}
	for i, s := range sug {
		if s.Field != spec.Fields[i].Name {
			t.Errorf("header %q suggested %q, want its own field %q", s.Header, s.Field, spec.Fields[i].Name)
		}
		if s.Confidence != ConfidenceSynonym {
			t.Errorf("header %q confidence = %v, want %v (synonym ladder)", s.Header, s.Confidence, ConfidenceSynonym)
		}
	}
}

// TestPTPPortValidate pins the mapping validation of the script channel:
// keyColumns must be exactly the composite pair, both Required key fields
// must be mapped, and the full suggested mapping of the virtual sheet
// validates cleanly.
func TestPTPPortValidate(t *testing.T) {
	spec, _ := SpecByType(domain.ResourceTypePTPPort)
	headers := make([]string, len(spec.Fields))
	for i, f := range spec.Fields {
		headers[i] = f.Label
	}
	headersBySheet := map[string][]string{"PTP配置": headers}

	full := SuggestMapping([]SheetInfo{{Name: "PTP配置", Headers: headers}})
	if err := Validate(full, headersBySheet); err != nil {
		t.Fatalf("Validate(suggested mapping) = %v, want nil (the zero-touch prefill is executable)", err)
	}

	// keyColumns must be exactly [device_model port_name].
	badKeys := Mapping{Sheets: []SheetMapping{{
		Sheet: "PTP配置", Action: ActionImport, ResourceType: domain.ResourceTypePTPPort,
		KeyColumns: []string{"port_name"},
		ColumnMapping: map[string]string{
			"device_model": "设备型号", "port_name": "接口名称",
		},
	}}}
	if err := Validate(badKeys, headersBySheet); err == nil {
		t.Fatal("Validate(port_name-only keyColumns) = nil, want an error")
	}

	// A missing required key mapping blocks the import (E002 anchor).
	missing := SuggestMapping([]SheetInfo{{Name: "PTP配置", Headers: headers}})
	delete(missing.Sheets[0].ColumnMapping, "port_name")
	if err := Validate(missing, headersBySheet); err == nil {
		t.Fatal("Validate(missing port_name mapping) = nil, want an error")
	}
}
