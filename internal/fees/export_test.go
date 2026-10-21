package fees

// setMoveFault installs the rollback file-move fault seam (tests only —
// the importer's setAfterSheetHook precedent): the hook fires before
// every quarantine/restore rename of the rollback dance with the step's
// op (moveQuarantine | moveRestore) and the live path it operates on; a
// non-nil answer aborts that exact move with the injected error. This is
// how #107's regression tests place an IO fault BETWEEN two renames of
// the same call — a read-only dir cannot (the fault must hit the second
// move while the first already succeeded), and nothing chmod-based bites
// when tests run as root. Returns the reset func.
func setMoveFault(svc *Service, hook func(op, path string) error) (reset func()) {
	prev := svc.testMoveFault
	svc.testMoveFault = hook
	return func() { svc.testMoveFault = prev }
}
