package importer

// Test-only extension points of Service. This file has a _test.go suffix, so
// it never compiles into production builds.

// setAfterSheetHook installs the failure-injection hook used by the atomic
// rollback test (PROJECT_BRIEF §12 S0-6): fn runs after each import sheet's
// writes inside the execution transaction; an error returned by fn rolls
// back the whole import.
func (s *Service) setAfterSheetHook(fn func(sheet string) error) {
	s.afterSheet = fn
}

// setRollbackCheckHook installs the observation hook used by the rollback
// TOCTOU regression test (R2-P2-1): fn runs inside the rollback transaction
// immediately after the in-use check, while the BEGIN IMMEDIATE write lock
// is held; an error returned by fn aborts (and rolls back) the rollback.
func (s *Service) setRollbackCheckHook(fn func() error) {
	s.rollbackCheckHook = fn
}
