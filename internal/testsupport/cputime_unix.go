//go:build !windows

package testsupport

import (
	"fmt"
	"syscall"
	"time"
)

// CPUTime returns the total CPU time (user + sys) the current process
// has consumed so far. Unlike wall time it does not inflate when
// sibling processes steal the cores (#82: wall-clock gates false-red
// under go test's package-parallelism on shared boxes — CPU seconds
// stay flat for the same work).
func CPUTime() (time.Duration, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, fmt.Errorf("getrusage: %w", err)
	}
	u := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	s := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return u + s, nil
}
