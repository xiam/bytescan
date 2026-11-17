//go:build !race

package streamtest

// defaultBudget is the default stream budget without the race detector.
const defaultBudget = 512 << 10

// raceDetector reports whether the test binary runs with the race detector.
const raceDetector = false
