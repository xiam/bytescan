//go:build race

package streamtest

// defaultBudget is the default stream budget under the race detector, which
// lexes about 0.1 MiB/s: four cursor buffers, so that about a dozen samples
// follow the baseline that Run takes after the first buffer.
const defaultBudget = 256 << 10

// raceDetector reports whether the test binary runs with the race detector.
const raceDetector = true
