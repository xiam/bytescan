//go:build race

package streamtest

// defaultBudget is the default stream budget under the race detector, which
// lexes about 0.1 MiB/s.
const defaultBudget = 64 << 10

// raceDetector reports whether the test binary runs with the race detector.
const raceDetector = true
