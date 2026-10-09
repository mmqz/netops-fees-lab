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
