//go:build windows

package testsupport

import (
	"fmt"
	"syscall"
	"time"
)

// CPUTime returns the total CPU time (user + kernel) the current
// process has consumed so far. Unlike wall time it does not inflate
// when sibling processes steal the cores (#82: wall-clock gates
// false-red under go test's package-parallelism on shared boxes —
// CPU seconds stay flat for the same work).
func CPUTime() (time.Duration, error) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, fmt.Errorf("GetCurrentProcess: %w", err)
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("GetProcessTimes: %w", err)
	}
	return time.Duration(kernel.Nanoseconds() + user.Nanoseconds()), nil
}
