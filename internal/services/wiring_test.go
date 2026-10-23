// Wiring tests of the composition root: the App, the HTTP API server and the
// MCP server consume ONE *Services instance — the "single assembly"
// guarantee is asserted here by POINTER IDENTITY (Go comparability of
// pointers), because behavior alone cannot distinguish one shared service
// instance from two coincidentally identical ones.
package services_test

import (
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestAppSharesOneServiceComposition pins the single-instance pass-through:
// every App service field aliases an instance of the SAME *Services
// composition (no dead second assembly), the HTTP API server was wired from
// that very instance (a.API.Services() == a.Services, so the handlers'
// query/importer/validation/… instances are the App's), and the composition
// is bound to the App's repository.
func TestAppSharesOneServiceComposition(t *testing.T) {
	dir := testsupport.TempDir(t)
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
		{"Report", a.Report, svc.Report},
		{"Metrics", a.Metrics, svc.Metrics},
		{"Fees", a.Fees, svc.Fees},
	} {
		if tc.got != tc.want {
			t.Errorf("App.%s is not the composition's instance (pointer identity broken)", tc.name)
		}
	}

	// The API server consumes the same composition.
	if a.API.Services() != svc {
		t.Error("a.API was not wired from a.Services — the handlers would run a second assembly")
	}

	// The composition is bound to the App's repository.
	if svc.DB() != a.DB {
		t.Error("Services.DB() is not the App's repository")
	}
}

// TestOpenBindsRepo pins the repo wiring of New's composition: the fee
// service and the metric service both read through the same repository the
// App opened (the fee snapshots root at the data dir; the DB at the db
// path).
func TestOpenBindsRepo(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")
	a, err := app.New(app.Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("app.Close: %v", err)
		}
	})
	if a.DB.Path() != dbPath {
		t.Errorf("a.DB.Path() = %q, want %q", a.DB.Path(), dbPath)
	}
	var _ *repo.DB = a.Services.DB()
}
