package special_test

import "github.com/xiam/bytescan/internal/lextest"

// The stream helpers of this package are the shared ones in internal/lextest,
// under short local names.
var (
	lexStream      = lextest.LexStream
	token          = lextest.Token
	unknowns       = lextest.Unknowns
	runStreamCases = lextest.RunStreamCases[streamCase]
)

// streamCase is one input of a stream test and its complete expected token
// stream.
type streamCase struct {
	Input    string
	Expected []string
}
