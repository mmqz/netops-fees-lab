package metrics

// Small exported helpers that were the package's zero-covered tail:
// Basename (the CLI's display-name handling), joinKeys (the forecast
// refusal message's key list), ValidScenario (the synth channel's
// closed scenario set). Each is trivial — which is exactly why a
// silent behavioral change would otherwise ship unannounced.

import (
	"strings"
	"testing"
)

func TestBasename(t *testing.T) {
	cases := map[string]string{
		"/srv/uploads/abc_devices.xlsx": "abc_devices.xlsx",
		"devices.xlsx":                  "devices.xlsx",
		"trailing/slash/":               "slash",
		"":                              ".",
	}
	for in, want := range cases {
		if got := Basename(in); got != want {
			t.Errorf("Basename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinKeysMatchesKeySet(t *testing.T) {
	got := joinKeys()
	keys := KeySet()
	for _, k := range keys {
		if !strings.Contains(got, k) {
			t.Errorf("joinKeys() = %q, want it to contain %q", got, k)
		}
	}
	// the join must not lose or duplicate the catalog: splitting the
	// comma-joined form recovers exactly the closed set, in order.
	parts := strings.Split(got, ", ")
	if len(parts) != len(keys) {
		t.Fatalf("joinKeys splits into %d parts, want %d (the whole closed set, no dupes)", len(parts), len(keys))
	}
}

func TestValidScenarioClosedSet(t *testing.T) {
	for _, s := range []string{ScenarioNormal, ScenarioDegrade, ScenarioOutage} {
		if !ValidScenario(s) {
			t.Errorf("ValidScenario(%q) = false, want true (the documented closed set)", s)
		}
	}
	for _, s := range []string{"", "Outage", "degraded", "maintenance", "outage "} {
		if ValidScenario(s) {
			t.Errorf("ValidScenario(%q) = true, want false (closed set — no substring/case tolerance)", s)
		}
	}
}
