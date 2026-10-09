package domain

import (
	"errors"
	"regexp"
	"testing"
)

func TestRuleCodes(t *testing.T) {
	got := map[string]string{
		"E001": RuleDuplicateKey,
		"E002": RuleMissingRequiredField,
		"E003": RuleInvalidReference,
		"E004": RuleInvalidFormat,
		"E005": RuleSubnetOverlap,
		"E006": RuleDuplicateAddress,
		"W001": RuleOrphanRecord,
		"W002": RuleUnknownStatusValue,
		"W003": RuleCircuitSelfLoop,
		"I001": RuleUnlinkedDevice,
	}
	for code, constVal := range got {
		if constVal != code {
			t.Errorf("rule code constant = %q, want %q", constVal, code)
		}
	}
	if RuleVersion != "1.0.0" {
		t.Errorf("RuleVersion = %q, want 1.0.0", RuleVersion)
	}
	wantSet := []string{"E005", "E006", "I001"}
	if len(SetLevelRuleCodes) != len(wantSet) {
		t.Fatalf("len(SetLevelRuleCodes) = %d, want %d", len(SetLevelRuleCodes), len(wantSet))
	}
	for i, c := range wantSet {
		if SetLevelRuleCodes[i] != c {
			t.Errorf("SetLevelRuleCodes[%d] = %q, want %q", i, SetLevelRuleCodes[i], c)
		}
	}
}

func TestNewID(t *testing.T) {
	re := regexp.MustCompile(`^res_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	for _, prefix := range []string{"res", "rel", "src", "bat", "iss"} {
		id := NewID(prefix)
		if !regexp.MustCompile(`^` + prefix + `_`).MatchString(id) {
			t.Errorf("NewID(%q) = %q, missing prefix", prefix, id)
		}
		if prefix == "res" && !re.MatchString(id) {
			t.Errorf("NewID(res) = %q, unexpected shape", id)
		}
	}
	a, b := NewID("bat"), NewID("bat")
	if a == b {
		t.Errorf("NewID not unique: %q", a)
	}
}

func TestEnumRoundTrips(t *testing.T) {
	type testCase struct {
		name    string
		valid   []string
		invalid string
	}
	cases := []testCase{
		{"ResourceType", []string{"device", "ip_pool", "circuit"}, "vlan"},
		{"RelationType", []string{"assigned_to", "a_end", "z_end"}, "peer_of"},
		{"BatchStatus", []string{"analyzing", "awaiting_mapping", "importing", "completed", "failed", "rolled_back"}, "paused"},
		{"RowStatus", []string{"imported", "skipped", "failed"}, "pending"},
		{"Severity", []string{"error", "warning", "info"}, "fatal"},
		{"EntityKind", []string{"resource", "relation"}, "batch"},
	}
	for _, c := range cases {
		for _, v := range c.valid {
			switch c.name {
			case "ResourceType":
				if got := ResourceType(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !ResourceType(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseResourceType(v); err != nil {
					t.Errorf("ParseResourceType(%q): %v", v, err)
				}
			case "RelationType":
				if got := RelationType(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !RelationType(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseRelationType(v); err != nil {
					t.Errorf("ParseRelationType(%q): %v", v, err)
				}
			case "BatchStatus":
				if got := BatchStatus(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !BatchStatus(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseBatchStatus(v); err != nil {
					t.Errorf("ParseBatchStatus(%q): %v", v, err)
				}
			case "RowStatus":
				if got := RowStatus(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !RowStatus(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseRowStatus(v); err != nil {
					t.Errorf("ParseRowStatus(%q): %v", v, err)
				}
			case "Severity":
				if got := Severity(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !Severity(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseSeverity(v); err != nil {
					t.Errorf("ParseSeverity(%q): %v", v, err)
				}
			case "EntityKind":
				if got := EntityKind(v).String(); got != v {
					t.Errorf("%s.String() = %q, want %q", c.name, got, v)
				}
				if !EntityKind(v).IsValid() {
					t.Errorf("%s(%q).IsValid() = false, want true", c.name, v)
				}
				if _, err := ParseEntityKind(v); err != nil {
					t.Errorf("ParseEntityKind(%q): %v", v, err)
				}
			}
		}
		// Invalid values must error.
		switch c.name {
		case "ResourceType":
			if _, err := ParseResourceType(c.invalid); err == nil {
				t.Errorf("ParseResourceType(%q) succeeded, want error", c.invalid)
			}
		case "RelationType":
			if _, err := ParseRelationType(c.invalid); err == nil {
				t.Errorf("ParseRelationType(%q) succeeded, want error", c.invalid)
			}
		case "BatchStatus":
			if _, err := ParseBatchStatus(c.invalid); err == nil {
				t.Errorf("ParseBatchStatus(%q) succeeded, want error", c.invalid)
			}
		case "RowStatus":
			if _, err := ParseRowStatus(c.invalid); err == nil {
				t.Errorf("ParseRowStatus(%q) succeeded, want error", c.invalid)
			}
		case "Severity":
			if _, err := ParseSeverity(c.invalid); err == nil {
				t.Errorf("ParseSeverity(%q) succeeded, want error", c.invalid)
			}
		case "EntityKind":
			if _, err := ParseEntityKind(c.invalid); err == nil {
				t.Errorf("ParseEntityKind(%q) succeeded, want error", c.invalid)
			}
		}
	}
}

func TestBatchStatusIsTerminal(t *testing.T) {
	terminal := map[BatchStatus]bool{
		BatchStatusCompleted:  true,
		BatchStatusFailed:     true,
		BatchStatusRolledBack: true,
		BatchStatusAnalyzing:  false, BatchStatusAwaitingMapping: false, BatchStatusImporting: false,
	}
	for s, want := range terminal {
		if got := s.IsTerminal(); got != want {
			t.Errorf("(%s).IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	if !errors.Is(ErrNotFound, ErrNotFound) || !errors.Is(ErrConflict, ErrConflict) {
		t.Fatal("sentinel errors must be comparable with errors.Is")
	}
	wrapped := errors.Join(ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Join must preserve sentinel identity")
	}
}
