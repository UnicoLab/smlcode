//go:build !race

package memory

// raceEnabled is false in a normal test build.
const raceEnabled = false
