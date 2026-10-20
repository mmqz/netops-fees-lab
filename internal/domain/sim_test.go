package domain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stringEnum is the common shape of all typed-string enums (P1 style:
// typed string + String + IsValid + Parse).
type stringEnum interface {
	~string
	String() string
	IsValid() bool
}

// assertStringEnum checks the P1 enum contract for one enum type: String
// round-trip, IsValid, Parse success on every valid value and Parse error on
// invalid ones.
func assertStringEnum[E stringEnum](t *testing.T, name string, parse func(string) (E, error), valid ...string) {
	t.Helper()
	for _, v := range valid {
		e, err := parse(v)
		if err != nil {
			t.Errorf("%s: parse %q: %v", name, v, err)
			continue
		}
		if e.String() != v {
			t.Errorf("%s: String() = %q, want %q", name, e.String(), v)
		}
		if !E(v).IsValid() {
			t.Errorf("%s: IsValid(%q) = false, want true", name, v)
		}
	}
	for _, v := range []string{"", "bogus", "UP"} {
		if _, err := parse(v); err == nil {
			t.Errorf("%s: parse %q succeeded, want error", name, v)
		}
		if E(v).IsValid() {
			t.Errorf("%s: IsValid(%q) = true, want false", name, v)
		}
	}
}

func TestSimEnums(t *testing.T) {
	assertStringEnum(t, "EntityStatus", ParseEntityStatus,
		"up", "down")
	assertStringEnum(t, "WorkspaceStatus", ParseWorkspaceStatus,
		"active", "archived")
	assertStringEnum(t, "ChangeSetTrigger", ParseChangeSetTrigger,
		"user", "undo", "checkpoint_restore")
	assertStringEnum(t, "ChangeSetStatus", ParseChangeSetStatus,
		"applied", "reverted", "failed")
	assertStringEnum(t, "CheckTrigger", ParseCheckTrigger,
		"manual", "api", "mcp", "auto_after_ops")
	assertStringEnum(t, "OpResult", ParseOpResult,
		"applied", "failed", "not_attempted")
}

func TestOpCodes(t *testing.T) {
	forward := map[string]string{
		"create_device":         OpCreateDevice,
		"set_device_status":     OpSetDeviceStatus,
		"delete_device":         OpDeleteDevice,
		"create_interface":      OpCreateInterface,
		"set_interface_status":  OpSetInterfaceStatus,
		"config_interface_ipv4": OpConfigInterfaceIPv4,
		"delete_interface":      OpDeleteInterface,
		"create_link":           OpCreateLink,
		"set_link_status":       OpSetLinkStatus,
		"delete_link":           OpDeleteLink,
	}
	if len(forward) != 10 {
		t.Fatalf("forward op catalog size = %d, want 10", len(forward))
	}
	for want, got := range forward {
		if got != want {
			t.Errorf("forward op constant = %q, want %q", got, want)
		}
		if !IsForwardOp(got) {
			t.Errorf("IsForwardOp(%q) = false, want true", got)
		}
		if IsRestoreOp(got) {
			t.Errorf("IsRestoreOp(%q) = true, want false", got)
		}
	}

	restore := map[string]string{
		"restore_device":    OpRestoreDevice,
		"restore_interface": OpRestoreInterface,
		"restore_link":      OpRestoreLink,
	}
	if len(restore) != 3 {
		t.Fatalf("restore op catalog size = %d, want 3", len(restore))
	}
	for want, got := range restore {
		if got != want {
			t.Errorf("restore op constant = %q, want %q", got, want)
		}
		if !IsRestoreOp(got) {
			t.Errorf("IsRestoreOp(%q) = false, want true", got)
		}
		if IsForwardOp(got) {
			t.Errorf("IsForwardOp(%q) = true, want false", got)
		}
	}

	for _, unknown := range []string{"", "create", "restore", "delete_workspace"} {
		if IsForwardOp(unknown) || IsRestoreOp(unknown) {
			t.Errorf("unknown op %q classified as forward or restore", unknown)
		}
	}
}

func TestCheckCodes(t *testing.T) {
	want := []string{"CHK-001", "CHK-002", "CHK-003", "CHK-004", "CHK-005"}
	if len(CheckCodes) != len(want) {
		t.Fatalf("len(CheckCodes) = %d, want %d", len(CheckCodes), len(want))
	}
	for i, c := range want {
		if CheckCodes[i] != c {
			t.Errorf("CheckCodes[%d] = %q, want %q", i, CheckCodes[i], c)
		}
	}
	consts := map[string]string{
		"CHK-001": CheckDuplicateHostAddress,
		"CHK-002": CheckSubnetOverlap,
		"CHK-003": CheckIsolatedDevice,
		"CHK-004": CheckPartialPartition,
		"CHK-005": CheckUnconfiguredInterface,
	}
	for code, constVal := range consts {
		if constVal != code {
			t.Errorf("check code constant = %q, want %q", constVal, code)
		}
	}
	if SimRuleVersion != "1.0.0" {
		t.Errorf("SimRuleVersion = %q, want 1.0.0", SimRuleVersion)
	}
}

func TestSeqKindsAndFormatSimID(t *testing.T) {
	kinds := map[string]string{
		"vd":  SeqDevice,
		"if":  SeqInterface,
		"ln":  SeqLink,
		"cs":  SeqChangeSet,
		"run": SeqCheckRun,
		"cp":  SeqCheckpoint,
	}
	for want, got := range kinds {
		if got != want {
			t.Errorf("sequence kind constant = %q, want %q", got, want)
		}
	}
	if got := FormatSimID(SeqDevice, 1); got != "vd-1" {
		t.Errorf("FormatSimID(vd,1) = %q, want vd-1", got)
	}
	if got := FormatSimID(SeqCheckRun, 42); got != "run-42" {
		t.Errorf("FormatSimID(run,42) = %q, want run-42", got)
	}
	if got := FormatSimID(SeqCheckpoint, 0); got != "cp-0" {
		t.Errorf("FormatSimID(cp,0) = %q, want cp-0", got)
	}
}

func TestSimSentinelErrors(t *testing.T) {
	for _, sentinel := range []error{ErrInvalidOp, ErrNotLatest} {
		if !errors.Is(sentinel, sentinel) {
			t.Fatal("sentinel errors must be comparable with errors.Is")
		}
	}
	wrapped := fmt.Errorf("engine: apply op 2: %w", ErrInvalidOp)
	if !errors.Is(wrapped, ErrInvalidOp) {
		t.Error("wrapping must preserve ErrInvalidOp identity")
	}
	wrapped = fmt.Errorf("undo cs-3: %w", ErrNotLatest)
	if !errors.Is(wrapped, ErrNotLatest) {
		t.Error("wrapping must preserve ErrNotLatest identity")
	}
	// The Phase 1 sentinels stay reusable and distinct.
	if errors.Is(ErrInvalidOp, ErrNotFound) || errors.Is(ErrInvalidOp, ErrConflict) {
		t.Error("ErrInvalidOp must not alias an existing sentinel")
	}
	if errors.Is(ErrNotLatest, ErrNotFound) || errors.Is(ErrNotLatest, ErrConflict) {
		t.Error("ErrNotLatest must not alias an existing sentinel")
	}
}

func TestAppVersionP2(t *testing.T) {
	// P2: the version constant is a single pinned fact (the changelog-head
	// cross-check below guards the other copy).
	if AppVersion != "v0.15.2" {
		t.Errorf("AppVersion = %q, want v0.15.2 (v0.15.2 release-channel restoration + cross-platform path fix — CHANGELOG 0.15.2)", AppVersion)
	}
}

// TestAppVersionMatchesChangelogHead enforces the single-fact-source rule
// (review R1-D1 / R3-C1): the domain constant and the CHANGELOG head section
// must always agree, otherwise the health endpoint, the MCP handshake, the
// frontend and the docs report different versions — exactly the v0.3.0 /
// v0.3.1 drift this test makes impossible to reintroduce silently.
func TestAppVersionMatchesChangelogHead(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Skipf("CHANGELOG.md not readable (package tested outside the repo tree): %v", err)
	}
	head := firstChangelogHeading(string(raw))
	want := "## [" + strings.TrimPrefix(AppVersion, "v") + "]"
	if !strings.HasPrefix(head, want) {
		t.Errorf("CHANGELOG head section %q does not match AppVersion %q — bump the constant and the CHANGELOG together", head, AppVersion)
	}
}

// firstChangelogHeading returns the first "## [x.y.z]" line of the changelog.
func firstChangelogHeading(changelog string) string {
	for _, line := range strings.Split(changelog, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## [") {
			return line
		}
	}
	return ""
}

func TestWorkspaceSource(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"lineage", `{"batchIds":["bat_1","bat_2"]}`, []string{"bat_1", "bat_2"}},
		{"empty object", `{}`, nil},
		{"empty string", "", nil},
		{"null", "null", nil},
		{"null batchIds", `{"batchIds":null}`, nil},
	}
	for _, c := range cases {
		src, err := ParseWorkspaceSource(c.input)
		if err != nil {
			t.Errorf("%s: ParseWorkspaceSource(%q): %v", c.name, c.input, err)
			continue
		}
		if len(src.BatchIDs) != len(c.want) {
			t.Errorf("%s: BatchIDs = %v, want %v", c.name, src.BatchIDs, c.want)
			continue
		}
		for i, id := range c.want {
			if src.BatchIDs[i] != id {
				t.Errorf("%s: BatchIDs[%d] = %q, want %q", c.name, i, src.BatchIDs[i], id)
			}
		}
	}
	for _, bad := range []string{"{", "[]", `"batchIds"`} {
		if _, err := ParseWorkspaceSource(bad); err == nil {
			t.Errorf("ParseWorkspaceSource(%q) succeeded, want error", bad)
		}
	}

	w := Workspace{SourceJSON: `{"batchIds":["bat_9"]}`}
	src, err := w.Source()
	if err != nil {
		t.Fatalf("Workspace.Source(): %v", err)
	}
	if len(src.BatchIDs) != 1 || src.BatchIDs[0] != "bat_9" {
		t.Errorf("Workspace.Source() = %+v, want [bat_9]", src)
	}
}

func TestChangeSetReverted(t *testing.T) {
	cs := ChangeSet{ID: "cs-1", Status: ChangeSetStatusApplied}
	if cs.Reverted() {
		t.Error("fresh changeset Reverted() = true, want false")
	}
	cs.RevertedAt = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if !cs.Reverted() {
		t.Error("changeset with RevertedAt Reverted() = false, want true")
	}
}
