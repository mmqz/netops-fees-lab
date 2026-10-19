//go:build race

package fees

// raceEnabled reports that the test binary runs under the race detector —
// performance bounds are meaningless there (the instrumentation multiplies
// wall time several-fold) and the performance tests skip.
const raceEnabled = true
