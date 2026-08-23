//go:build race

package memory

// raceEnabled is true when the test binary was built with -race, whose
// instrumentation makes wall-clock budgets meaningless.
const raceEnabled = true
