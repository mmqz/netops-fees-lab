// Wiring tests of the W9-D composition root: the App, the HTTP API server
// and the MCP server consume ONE *Services instance — the "single assembly"
// guarantee is asserted here by POINTER IDENTITY (Go comparability of
// pointers), because behavior alone cannot distinguish one shared service
// instance from two coincidentally identical ones.
package services_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/services"
)

// TestAppSharesOneServiceComposition pins the single-instance pass-through:
// every App service field aliases an instance of the SAME *Services
// composition (no dead second assembly), the HTTP API server was wired from
// that very instance (a.API.Services() == a.Services, so the handlers'
// query/importer/validation/… instances are the App's), and the composition
// is bound to the App's repository. Before W9-D the API built its own service
// set from the bare db and these identities did not hold.
func TestAppSharesOneServiceComposition(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("app.Close: %v", err)
		}
	})

	svc := a.Services
	if svc == nil {
		t.Fatal("a.Services is nil — the composition root must be exposed for the CLI/MCP pass-through")
	}
	// The App fields alias the composition's instances (not copies, not
	// second constructions — pointer identity is the point).
	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"Importer", a.Importer, svc.Importer},
		{"Validation", a.Validation, svc.Validation},
		{"Query", a.Query, svc.Query},
		{"Changes", a.Changes, svc.Changes},
		{"Renames", a.Renames, svc.Renames},
		{"Lab", a.Lab, svc.Lab},
		{"Report", a.Report, svc.Report},
	} {
		if tc.got != tc.want {
			t.Errorf("App.%s is NOT the composition's instance (different pointers) — a second assembly exists", tc.name)
		}
	}

	// The single-instance pass-through the task named explicitly: the query
	// service the HTTP API uses is the App's query service.
	if apiSvc := a.API.Services(); apiSvc != svc {
		t.Fatalf("a.API was wired from a DIFFERENT *services.Services instance than a.Services — the dead assembly is back")
	}

	// The composition is bound to the App's repository (handlers with thin
	// repo-passthrough read paths fetch it via Services.DB()).
	if svc.DB() != a.DB {
		t.Errorf("Services.DB() is not the App's repository (different pointers)")
	}
}

// TestNewWiresClockOption pins the WithClock knob: the injected clock
// propagates to both confirmation services' Now hooks (deterministic
// decided-at stamps in tests); the default wiring keeps time.Now.
func TestNewWiresClockOption(t *testing.T) {
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fixed := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	svc := services.New(db, services.WithClock(func() time.Time { return fixed }))
	if got := svc.Changes.Now(); !got.Equal(fixed) {
		t.Errorf("Changes.Now() = %v, want the injected fixed clock %v", got, fixed)
	}
	if got := svc.Renames.Now(); !got.Equal(fixed) {
		t.Errorf("Renames.Now() = %v, want the injected fixed clock %v", got, fixed)
	}

	// Default wiring: the hooks are live clocks (non-nil, advancing service
	// of time.Now — a nil hook would also fall back, but New installs one).
	def := services.New(db)
	if def.Changes.Now == nil || def.Renames.Now == nil {
		t.Error("default wiring left a nil Now hook on a confirmation service")
	}
}
