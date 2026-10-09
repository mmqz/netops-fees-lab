// Package importer implements the import pipeline of PROJECT_BRIEF §8:
// workbook analysis with deterministic column suggestions, execution of the
// confirmed mapping with row-level accounting (imported / skipped / failed),
// idempotent re-import (existing resources are skipped, never duplicated),
// atomic all-or-nothing writes for every import sheet, and batch rollback.
//
// Set-level validation (E005/E006/I001) is NOT part of this package — it is
// the job of the validation engine, which recomputes those issues over the
// whole database after import.
package importer

import (
	"time"

	"github.com/mmqz/netops-lab/internal/repo"
)

// Service is the import pipeline service. All persistence goes through the
// repo layer; multi-statement atomicity uses DB.Do.
type Service struct {
	db *repo.DB

	// Now is the clock hook; tests may replace it for deterministic
	// timestamps. New installs time.Now; a nil hook also falls back to
	// time.Now.
	Now func() time.Time

	// afterSheet, when non-nil, is invoked after each import sheet's writes
	// inside the execution transaction. It exists exclusively for
	// failure-injection in the atomicity test (S0-6) and is nil in
	// production; it is only settable from within the package (see
	// export_test.go).
	afterSheet func(sheet string) error
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db, Now: time.Now}
}

// clock returns the current UTC time, honoring the Now hook.
func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
