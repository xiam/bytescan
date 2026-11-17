package core_test

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/internal/lextest"
)

const lexTypeR = bytescan.TokenType("R")

// The stream helpers of this package are the shared ones in internal/lextest,
// under short local names.
var (
	lexStream      = lextest.LexStream
	collectStream  = lextest.Collect
	token          = lextest.Token
	unknowns       = lextest.Unknowns
	runStreamCases = lextest.RunStreamCases[streamCase]
)

// streamCase is one input of a hand-written stream test and its complete
// expected token stream.
type streamCase struct {
	Input    string
	Expected []string
}

// raceDetector reports whether the test binary runs with the race detector.
const raceDetector = lextest.RaceDetector
